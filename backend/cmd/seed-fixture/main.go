// Package main implements the seed-fixture CLI — populates a dev Paladin
// cluster with realistic, deterministic data so designers / operators
// can exercise UI states (populated tables, time-series, error chips,
// pagination boundaries) without hand-clicking through the API.
//
// One binary, two top-level subcommands:
//
//	seed-fixture up   --flavour=<demo|load|stress>
//	seed-fixture down --flavour=<demo|load|stress>
//
// Flavours:
//
//	demo    1 tenant, 4 EventSubscriptions (HTTP / NATS / disabled / filtered).
//	        Hand-curated so screenshots stay readable. ~1 second to apply.
//
//	load    N objects (default 500) seeded under an existing collection via the
//	        real UploadObject → PUT → CompleteObject flow, so /objects listings
//	        and the object.* event fan-out have realistic data.
//	stress  same, defaulting to 1001 objects — just past a 1000-row page so the
//	        cursor pagination logic gets exercised across a boundary.
//
// load / stress take --object-key (an existing collection bound to a bucket,
// like smoke-upload), --tenant (slug for the parent name), and --count (0 =
// the flavour default). Objects are stamped at the current time — the API
// can't backdate created_at, so the "/billing 24h time-series spread" remains
// a separate concern (see BACKLOG).
//
// Idempotency: every fixture resource is keyed on a stable display name
// prefix (`Fixture: <flavour>:`). Re-running `up` is a no-op for
// resources that already exist; differences in the requested config
// trigger an Update rather than a Create. Tear-down deletes everything
// matching the prefix.
//
// Production guard: the CLI refuses to run unless either
//
//	PALADIN_FIXTURE_OK=1
//
// is set in the environment, OR the --admin-url begins with
// `http://` and resolves to an in-cluster / localhost / *.local /
// *.test name. The check is loose-by-design — operators who want to
// override know to set the env var.
//
// Auth: bootstrap admin credentials by default (admin / password).
// Operators with non-default credentials pass --user / --password.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/mcp"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcmeta"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

const (
	// fixturePrefix marks every display_name / sink config the CLI writes,
	// so tear-down has an unambiguous query: "delete every tenant whose
	// display_name starts with this string". Don't change without bumping
	// any in-cluster fixture data first — the regex is dumb.
	fixturePrefix = "Fixture: "
)

// ─── Top-level command tree ────────────────────────────────────────────────

func main() {
	root := &cobra.Command{
		Use:   "seed-fixture",
		Short: "Populate a dev Paladin cluster with realistic UI/UX fixture data",
		Long: "Seed-fixture is a dev tool. It refuses to run against any " +
			"deployment that doesn't smell like a dev cluster (admin URL must " +
			"contain `local`, `test`, `cluster.local`, or `127.`/`localhost` — " +
			"or set PALADIN_FIXTURE_OK=1 to override).",
	}
	upCmd := &cobra.Command{
		Use:   "up",
		Short: "Apply the chosen flavour (idempotent)",
		RunE:  func(cmd *cobra.Command, _ []string) error { return runUp(cmd) },
	}
	downCmd := &cobra.Command{
		Use:   "down",
		Short: "Remove every resource the chosen flavour created",
		RunE:  func(cmd *cobra.Command, _ []string) error { return runDown(cmd) },
	}
	smokeCmd := &cobra.Command{
		Use:   "smoke-upload",
		Short: "Trigger a data-plane UploadObject + presigned PUT, then DON'T call CompleteObject",
		Long: "Exercises the SF→NATS→ingest pipeline end-to-end: data-plane " +
			"UploadObject creates a PENDING row, the PUT writes bytes through " +
			"the SF S3 gateway which fires a filer event on `seaweedfs.filer`, " +
			"the ingest pod decodes it and promotes the row to AVAILABLE. " +
			"Operators verify by re-running with --verify (or by tailing " +
			"paladin-ingest logs).",
		RunE: func(cmd *cobra.Command, _ []string) error { return runSmokeUpload(cmd) },
	}
	for _, c := range []*cobra.Command{upCmd, downCmd, smokeCmd} {
		c.Flags().String("admin-url", "https://paladin.local/api/rpc/admin", "Admin plane URL")
		c.Flags().String("iam-url", "https://paladin.local/api/rpc/iam", "IAM plane URL")
		c.Flags().String("data-url", "https://paladin.local/api/rpc/data", "Data plane URL")
		c.Flags().String("user", "admin", "Bootstrap admin subject")
		c.Flags().String("password", "password", "Bootstrap admin password")
		// Port-forwarded planes present the cluster's self-signed cert for a
		// Service DNS name while the CLI dials localhost — verification can
		// never pass there. Dev-tool escape hatch; `task seed:up` sets it.
		c.Flags().Bool("insecure-tls", false, "Skip TLS certificate verification (port-forwarded self-signed dev planes)")
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{upCmd, downCmd} {
		c.Flags().String("flavour", "demo", "demo | load | stress")
		// load / stress seed objects under an existing collection (like
		// smoke-upload). demo ignores these.
		c.Flags().String("object-key", "test",
			"load/stress: existing collection (bound to a bucket) to seed objects under")
		c.Flags().String("tenant", "platform",
			"load/stress: tenant slug for the parent resource name")
		c.Flags().Int("count", 0,
			"load/stress: number of objects to seed (0 = flavour default: load 500, stress 1001)")
	}
	smokeCmd.Flags().String("object-key", "test",
		"existing collection on the caller's tenant — must already be bound to a bucket")
	smokeCmd.Flags().String("key", "",
		"storage key (server picks when empty)")
	smokeCmd.Flags().String("tenant", "platform",
		"tenant slug for the parent resource name; data plane resolves "+
			"the actual tenant from JWT, slug is just shape")
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "seed-fixture:", err)
		os.Exit(1)
	}
}

// ─── Production guard ─────────────────────────────────────────────────────

// assertDevTarget refuses to run against URLs that don't smell like a dev
// cluster unless the operator explicitly sets `PALADIN_FIXTURE_OK=1`. The
// heuristic is loose — anything matching `local`, `cluster.local`,
// `localhost`, `127.`, or `.test` passes. Production hostnames almost
// never satisfy any of these, so the override is the only way to hit
// real customer data.
func assertDevTarget(adminURL string) error {
	if os.Getenv("PALADIN_FIXTURE_OK") == "1" {
		return nil
	}
	low := strings.ToLower(adminURL)
	for _, marker := range []string{"local", "127.", "localhost", ".test", ".cluster.local"} {
		if strings.Contains(low, marker) {
			return nil
		}
	}
	return fmt.Errorf("refusing to seed against %q — set PALADIN_FIXTURE_OK=1 to override", adminURL)
}

// ─── Auth ────────────────────────────────────────────────────────────────

// login issues an access token for the given audience. The data /
// admin / iam planes each enforce their own audience claim, so a
// caller that touches both (e.g. smoke-upload reads admin for
// resolving tenant + writes data for UploadObject) needs separate
// tokens.
// httpClientFor honours --insecure-tls: a port-forwarded plane presents the
// cluster's self-signed cert for its Service DNS name while the CLI dials
// localhost, so verification can never pass there. Dev-tool escape hatch.
func httpClientFor(cmd *cobra.Command) *http.Client {
	base := http.DefaultTransport
	if insecure, _ := cmd.Flags().GetBool("insecure-tls"); insecure {
		base = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 — guarded dev flag
		}
	}
	// The idempotency middleware rejects Create*/Issue* RPCs without an
	// Idempotency-Key (RequireOnCreate=true on every plane), and MEMOIZES any
	// request that carries one whatever its name.
	//
	// That second half is why this no longer stamps everything. The previous
	// version did, on the belief that "reads ignore the header" — they do not.
	// A List with a key gets its response cached like anything else, so the
	// seeder was writing a row into idempotency_keys for every read it made,
	// and never replaying one of them because each key was fresh. Cost with no
	// benefit; the purger cleaned up after it daily.
	return &http.Client{Timeout: 30 * time.Second, Transport: idempotencyTransport{base: base}}
}

type idempotencyTransport struct{ base http.RoundTripper }

// wantsIdempotencyKey asks the contract, via internal/rpcmeta.
//
// It was a prefix list duplicated here, in internal/mcp and in the console's
// transport. See rpcmeta's doc comment for why a name was never able to answer
// this question.
func wantsIdempotencyKey(procedure string) bool {
	return rpcmeta.NeedsIdempotencyKey(procedure)
}

func (t idempotencyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if wantsIdempotencyKey(req.URL.Path) && req.Header.Get("Idempotency-Key") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("Idempotency-Key", uuid.NewString())
	}
	return t.base.RoundTrip(req)
}

func login(ctx context.Context, httpc *http.Client, iamURL, user, password, audience string) (string, error) {
	authClient := mcp.NewClients(httpc, "", "", iamURL, "").Auth
	resp, err := authClient.Login(ctx, connect.NewRequest(&iamv1.LoginRequest{
		Subject:           user,
		Password:          password,
		RequestedAudience: audience,
	}))
	if err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	tok := resp.Msg.GetTokens().GetAccessToken()
	if tok == "" {
		return "", errors.New("login: empty access token")
	}
	return tok, nil
}

// ─── load / stress planning (pure, unit-tested) ────────────────────────────

// fixtureObjectPrefix is the user-key prefix every load/stress object gets, so
// tear-down can find them and re-runs can count existing ones. Per-flavour so
// load and stress don't clobber each other under the same collection.
func fixtureObjectPrefix(flavour string) string {
	return "fixture/" + flavour + "/"
}

// fixtureCollection builds the deterministic, zero-padded user key for the i-th
// (0-based) seeded object. Zero-padding keeps lexical == numeric order so the
// UI's cursor pagination walks them predictably.
func fixtureCollection(flavour string, i int) string {
	return fmt.Sprintf("%s%06d.txt", fixtureObjectPrefix(flavour), i)
}

// isFixtureCollection reports whether a user key was written by this seeder for
// the flavour — the tear-down predicate.
func isFixtureCollection(flavour, key string) bool {
	return strings.HasPrefix(key, fixtureObjectPrefix(flavour))
}

// flavourObjectCount resolves the requested object count: an explicit override
// (>0) wins, else the per-flavour default. load favours volume; stress sits
// just past a 1000-row page so the cursor logic gets exercised across a
// boundary. Any other flavour → 0 (no objects).
func flavourObjectCount(flavour string, override int) int {
	if override > 0 {
		return override
	}
	switch flavour {
	case "load":
		return 500
	case "stress":
		return 1001
	default:
		return 0
	}
}

// ─── up / down dispatch ───────────────────────────────────────────────────

func runUp(cmd *cobra.Command) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()
	flavour, _ := cmd.Flags().GetString("flavour")
	adminURL, _ := cmd.Flags().GetString("admin-url")
	iamURL, _ := cmd.Flags().GetString("iam-url")
	dataURL, _ := cmd.Flags().GetString("data-url")
	httpc := httpClientFor(cmd)
	user, _ := cmd.Flags().GetString("user")
	password, _ := cmd.Flags().GetString("password")

	if err := assertDevTarget(adminURL); err != nil {
		return err
	}
	tok, err := login(ctx, httpc, iamURL, user, password, string(auth.AudienceAdmin))
	if err != nil {
		return err
	}
	clients := mcp.NewClients(httpc, adminURL, dataURL, iamURL, tok)

	switch flavour {
	case "demo":
		return seedDemo(ctx, clients)
	case "load", "stress":
		return runObjectSeed(cmd, ctx, iamURL, dataURL, user, password, flavour, true)
	default:
		return fmt.Errorf("unknown flavour %q (use demo, load, or stress)", flavour)
	}
}

// runObjectSeed applies (seed=true) or removes (seed=false) the load / stress
// object fixture. Objects live on the data plane, so this logs in for a data
// token separately from the admin token runUp/runDown already hold (mirrors
// smoke-upload's rationale).
func runObjectSeed(cmd *cobra.Command, ctx context.Context, iamURL, dataURL, user, password, flavour string, seed bool) error {
	collection, _ := cmd.Flags().GetString("object-key")
	tenant, _ := cmd.Flags().GetString("tenant")
	countOverride, _ := cmd.Flags().GetInt("count")
	httpc := httpClientFor(cmd)

	dataTok, err := login(ctx, httpc, iamURL, user, password, string(auth.AudienceData))
	if err != nil {
		return fmt.Errorf("data login: %w", err)
	}
	dc := mcp.NewClients(httpc, "", dataURL, iamURL, dataTok)
	parent := fmt.Sprintf("tenants/%s/collections/%s", tenant, collection)

	if !seed {
		return teardownObjects(ctx, dc, parent, flavour)
	}
	return seedObjects(ctx, dc, parent, flavour, flavourObjectCount(flavour, countOverride))
}

func runDown(cmd *cobra.Command) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()
	flavour, _ := cmd.Flags().GetString("flavour")
	adminURL, _ := cmd.Flags().GetString("admin-url")
	iamURL, _ := cmd.Flags().GetString("iam-url")
	dataURL, _ := cmd.Flags().GetString("data-url")
	httpc := httpClientFor(cmd)
	user, _ := cmd.Flags().GetString("user")
	password, _ := cmd.Flags().GetString("password")

	if err := assertDevTarget(adminURL); err != nil {
		return err
	}
	tok, err := login(ctx, httpc, iamURL, user, password, string(auth.AudienceAdmin))
	if err != nil {
		return err
	}
	clients := mcp.NewClients(httpc, adminURL, dataURL, iamURL, tok)

	switch flavour {
	case "demo":
		return teardownDemo(ctx, clients)
	case "load", "stress":
		return runObjectSeed(cmd, ctx, iamURL, dataURL, user, password, flavour, false)
	default:
		return fmt.Errorf("unknown flavour %q (use demo, load, or stress)", flavour)
	}
}

// ─── Demo flavour ─────────────────────────────────────────────────────────

// seedDemo writes four EventSubscriptions on the bootstrap admin's
// tenant covering the states the operator will look at on the /events
// page:
//
//   - HTTP sink, no filter — the "everything goes here" baseline
//   - NATS sink, paladin.events subject — the second wired sink
//   - HTTP sink, disabled — exercises the dim "disabled" pill
//   - HTTP sink, with CEL filter — exercises the filter column
//
// We seed onto the caller's tenant (platform tenant for the
// bootstrap admin) rather than minting a new fixture tenant —
// `event_subscriptions` is RLS-enforced and the producer-side
// INSERT runs as `paladin_app` with a session GUC pinned to the
// caller's tenant_id. A cross-tenant create from
// platform.admin would hit a WITH CHECK violation. Operators
// who want fixture data on a non-platform tenant should run
// the CLI with --user / --password for that tenant's admin.
//
// Idempotency: every sink is keyed by sink shape (URL or
// subject). Re-running produces the same on-disk state.
func seedDemo(ctx context.Context, c *mcp.Clients) error {
	// Look up the caller's own tenant via Login response side-effect:
	// the JWT carries it, but we don't decode JWTs here. Use ListTenants
	// instead — bootstrap admin sees only their tenant unless they're
	// platform.admin (in which case the first hit is fine because we
	// only need ANY tenant scoped to the caller for RLS).
	resp, err := c.Tenant.ListTenants(ctx, connect.NewRequest(&adminv1.ListTenantsRequest{}))
	if err != nil {
		return fmt.Errorf("list tenants: %w", err)
	}
	if len(resp.Msg.GetTenants()) == 0 {
		return errors.New("no tenants visible — bootstrap admin should at least see the Platform tenant")
	}
	tenant := resp.Msg.GetTenants()[0]
	fmt.Printf("tenant: %s (%s)\n", tenant.GetDisplayName(), tenant.GetTenantId())

	subscriptions := []demoSubscription{
		{
			suffix: "http-baseline",
			sink: &adminv1.EventSink{Target: &adminv1.EventSink_Http{Http: &adminv1.HttpSink{
				Url: "https://example.test/webhook/baseline",
			}}},
			filter: "",
		},
		{
			suffix: "nats-events",
			sink: &adminv1.EventSink{Target: &adminv1.EventSink_Nats{Nats: &adminv1.NatsSink{
				Url:     "nats://nats.nats.svc.cluster.local:4222",
				Subject: "paladin.events",
			}}},
			filter: "",
		},
		{
			suffix: "http-disabled",
			sink: &adminv1.EventSink{Target: &adminv1.EventSink_Http{Http: &adminv1.HttpSink{
				Url: "https://example.test/webhook/disabled",
			}}},
			disabled: true,
			filter:   "",
		},
		{
			suffix: "http-filtered",
			sink: &adminv1.EventSink{Target: &adminv1.EventSink_Http{Http: &adminv1.HttpSink{
				Url: "https://example.test/webhook/filtered",
			}}},
			filter: "event.kind == 'paladin.object.uploaded'",
		},
	}
	for _, s := range subscriptions {
		if err := ensureSubscription(ctx, c, tenant.GetTenantId(), s); err != nil {
			return fmt.Errorf("subscription %s: %w", s.suffix, err)
		}
		fmt.Printf("subscription: %s%s\n", fixturePrefix+"demo:sub:", s.suffix)
	}
	fmt.Println("demo flavour applied — visit /events to see the populated table")
	return nil
}

func teardownDemo(ctx context.Context, c *mcp.Clients) error {
	// Walk the caller's tenants and delete every subscription whose
	// sink target matches a fixture-shaped URL or subject. We can't
	// delete-by-display-name because EventSubscription has no
	// display name in v1 — discriminating by sink config is what
	// the seedDemo idempotency check uses too.
	tenants, err := c.Tenant.ListTenants(ctx, connect.NewRequest(&adminv1.ListTenantsRequest{}))
	if err != nil {
		return fmt.Errorf("list tenants: %w", err)
	}
	deleted := 0
	for _, t := range tenants.Msg.GetTenants() {
		parent := "tenants/" + t.GetTenantId()
		listResp, err := c.EventSub.ListSubscriptions(ctx,
			connect.NewRequest(&adminv1.ListSubscriptionsRequest{Parent: parent}),
		)
		if err != nil {
			return fmt.Errorf("list subs %s: %w", t.GetTenantId(), err)
		}
		for _, sub := range listResp.Msg.GetSubscriptions() {
			if !isFixtureSink(sub.GetSink()) {
				continue
			}
			if _, err := c.EventSub.DeleteSubscription(ctx,
				connect.NewRequest(&adminv1.DeleteSubscriptionRequest{Name: sub.GetName()}),
			); err != nil {
				return fmt.Errorf("delete sub %s: %w", sub.GetName(), err)
			}
			deleted++
		}
	}
	fmt.Printf("demo flavour torn down (deleted %d subscriptions)\n", deleted)
	return nil
}

// ─── Load / stress flavours: object seeding ───────────────────────────────

// seedObjects seeds `count` fixture objects under `parent` (an existing
// collection resource name) through the real UploadObject → PUT → CompleteObject
// flow, so /objects listings + cursor pagination + the object.* event fan-out
// see realistic data. Idempotent: it counts the fixture objects already present
// and creates only the remainder, so re-running converges to `count`.
//
// NOTE: objects are stamped at the current time — the public API can't backdate
// created_at, so the "/billing time-series spread across 24h" part of the DoD
// is out of reach here (it needs a server test-hook or a direct-SQL seeder).
func seedObjects(ctx context.Context, dc *mcp.Clients, parent, flavour string, count int) error {
	if count <= 0 {
		return fmt.Errorf("flavour %q resolved to a non-positive object count", flavour)
	}
	existing, err := countFixtureObjects(ctx, dc, parent, flavour)
	if err != nil {
		return fmt.Errorf("count existing fixture objects: %w", err)
	}
	if existing >= count {
		fmt.Printf("%s: %d fixture objects already present (target %d) — nothing to do\n",
			flavour, existing, count)
		return nil
	}
	fmt.Printf("%s: seeding objects [%d, %d) under %s\n", flavour, existing, count, parent)
	for i := existing; i < count; i++ {
		if err := uploadFixtureObject(ctx, dc, parent, fixtureCollection(flavour, i)); err != nil {
			return fmt.Errorf("object %d: %w", i, err)
		}
		if (i+1)%100 == 0 {
			fmt.Printf("  … %d/%d\n", i+1, count)
		}
	}
	fmt.Printf("%s flavour applied — %d objects under %s (visit /objects)\n", flavour, count, parent)
	return nil
}

// uploadFixtureObject runs one UploadObject → presigned PUT → CompleteObject
// cycle, materialising an AVAILABLE object (unlike smoke-upload, which skips
// completion and relies on the ingest pod).
func uploadFixtureObject(ctx context.Context, dc *mcp.Clients, parent, key string) error {
	payload := []byte("paladin-fixture " + key + "\n")
	uresp, err := dc.Object.UploadObject(ctx, connect.NewRequest(&datav1.UploadObjectRequest{
		Parent:            parent,
		Key:               key,
		ContentType:       "text/plain",
		SizeHintBytes:     int64(len(payload)),
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
		ChecksumValue:     sha256Base64(payload),
	}))
	if err != nil {
		return fmt.Errorf("UploadObject: %w", err)
	}
	obj := uresp.Msg.GetObject()
	u := uresp.Msg.GetUploadUrl()
	req, err := http.NewRequestWithContext(ctx, u.GetMethod(), u.GetUrl(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("PUT build: %w", err)
	}
	for k, v := range u.GetRequiredHeaders() {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT do: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("PUT %d: %s", resp.StatusCode, string(body))
	}
	if _, err := dc.Object.CompleteObject(ctx, connect.NewRequest(&datav1.CompleteObjectRequest{
		Name: obj.GetName(),
		Etag: strings.Trim(resp.Header.Get("ETag"), `"`),
	})); err != nil {
		return fmt.Errorf("CompleteObject: %w", err)
	}
	return nil
}

// countFixtureObjects pages the collection's objects and counts the ones this
// seeder wrote for the flavour (by user-key prefix).
func countFixtureObjects(ctx context.Context, dc *mcp.Clients, parent, flavour string) (int, error) {
	names, err := listFixtureObjectNames(ctx, dc, parent, flavour)
	return len(names), err
}

// listFixtureObjectNames returns the resource names of every fixture object
// under `parent` for the flavour, walking all pages.
func listFixtureObjectNames(ctx context.Context, dc *mcp.Clients, parent, flavour string) ([]string, error) {
	var names []string
	token := ""
	for {
		resp, err := dc.Object.ListObjects(ctx, connect.NewRequest(&datav1.ListObjectsRequest{
			Parent: parent,
			Page:   &commonv1.PageRequest{PageSize: 1000, PageToken: token},
		}))
		if err != nil {
			return nil, fmt.Errorf("ListObjects: %w", err)
		}
		for _, o := range resp.Msg.GetObjects() {
			if isFixtureCollection(flavour, o.GetKey()) {
				names = append(names, o.GetName())
			}
		}
		token = resp.Msg.GetPage().GetNextPageToken()
		if token == "" {
			break
		}
	}
	return names, nil
}

// teardownObjects permanently deletes every fixture object the flavour wrote
// under `parent`. Collect-then-delete (rather than delete-while-paging) so the
// cursor isn't invalidated mid-walk.
func teardownObjects(ctx context.Context, dc *mcp.Clients, parent, flavour string) error {
	names, err := listFixtureObjectNames(ctx, dc, parent, flavour)
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, err := dc.Object.DeleteObject(ctx, connect.NewRequest(&datav1.DeleteObjectRequest{
			Name:      name,
			Permanent: true, // hard-delete so /objects is actually cleared
		})); err != nil {
			return fmt.Errorf("delete %s: %w", name, err)
		}
	}
	fmt.Printf("%s flavour torn down (deleted %d objects under %s)\n", flavour, len(names), parent)
	return nil
}

// isFixtureSink discriminates on the URLs / subjects seedDemo writes —
// `https://example.test/webhook/...` for HTTP and `paladin.events` on the
// fixture's NATS subject. Anything else is left alone.
func isFixtureSink(sink *adminv1.EventSink) bool {
	if sink == nil {
		return false
	}
	switch t := sink.GetTarget().(type) {
	case *adminv1.EventSink_Http:
		return strings.HasPrefix(t.Http.GetUrl(), "https://example.test/webhook/")
	case *adminv1.EventSink_Nats:
		return t.Nats.GetSubject() == "paladin.events" &&
			strings.Contains(t.Nats.GetUrl(), "nats.nats.svc.cluster.local")
	}
	return false
}

// ─── Resource helpers ─────────────────────────────────────────────────────

type demoSubscription struct {
	suffix   string // appended to fixturePrefix+"demo:sub:" for the sub's filter-side identifier
	sink     *adminv1.EventSink
	filter   string
	disabled bool
}

// ensureSubscription writes (or no-ops when present) one EventSubscription
// targeting the supplied sink. We match on the sink target — for
// idempotency we look at HTTP URL or NATS subject because the proto
// doesn't carry a per-sub display name.
func ensureSubscription(ctx context.Context, c *mcp.Clients, tenantID string, s demoSubscription) error {
	parent := "tenants/" + tenantID
	listResp, err := c.EventSub.ListSubscriptions(ctx,
		connect.NewRequest(&adminv1.ListSubscriptionsRequest{Parent: parent}),
	)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	for _, sub := range listResp.Msg.GetSubscriptions() {
		if subSinkMatches(sub.GetSink(), s.sink) {
			return nil // already present, nothing to do
		}
	}
	_, err = c.EventSub.CreateSubscription(ctx,
		connect.NewRequest(&adminv1.CreateSubscriptionRequest{
			Parent: parent,
			Subscription: &adminv1.EventSubscription{
				TenantId: tenantID,
				Filter:   s.filter,
				Sink:     s.sink,
				Disabled: s.disabled,
			},
		}),
	)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	return nil
}

func subSinkMatches(a, b *adminv1.EventSink) bool {
	if a == nil || b == nil {
		return false
	}
	at, bt := a.GetTarget(), b.GetTarget()
	if at == nil || bt == nil {
		return false
	}
	switch ax := at.(type) {
	case *adminv1.EventSink_Http:
		bx, ok := bt.(*adminv1.EventSink_Http)
		return ok && ax.Http.GetUrl() == bx.Http.GetUrl()
	case *adminv1.EventSink_Nats:
		bx, ok := bt.(*adminv1.EventSink_Nats)
		return ok && ax.Nats.GetSubject() == bx.Nats.GetSubject() && ax.Nats.GetUrl() == bx.Nats.GetUrl()
	}
	return false
}

// ─── Smoke: end-to-end PROMOTE through SF→NATS→ingest ─────────────────────

// runSmokeUpload exercises the storage-event ingest pipeline:
//
//  1. Authenticate as the bootstrap admin against the data plane.
//  2. UploadObject — server creates a PENDING `objects` row and
//     hands back a presigned PUT URL.
//  3. HTTP PUT bytes to that URL. The PUT lands on the SF S3
//     gateway, which writes the object AND fires a filer event
//     on `seaweedfs.filer`.
//  4. We deliberately SKIP the CompleteObject call. Promotion is
//     supposed to be driven by the ingest pod consuming the
//     filer event, looking up the row by composeKey, and calling
//     PromoteToAvailable.
//
// This proves that everything between "client wrote bytes" and
// "Paladin knows the bytes are there" works without Paladin RPC's normal
// synchronous CompleteObject path. Operators verify the row's
// state by re-running with an extra query or by inspecting
// /objects in the UI a few seconds after the PUT.
func runSmokeUpload(cmd *cobra.Command) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()
	adminURL, _ := cmd.Flags().GetString("admin-url")
	iamURL, _ := cmd.Flags().GetString("iam-url")
	dataURL, _ := cmd.Flags().GetString("data-url")
	httpc := httpClientFor(cmd)
	user, _ := cmd.Flags().GetString("user")
	password, _ := cmd.Flags().GetString("password")
	collection, _ := cmd.Flags().GetString("object-key")
	storageKey, _ := cmd.Flags().GetString("key")

	tenantHint, _ := cmd.Flags().GetString("tenant")
	if err := assertDevTarget(adminURL); err != nil {
		return err
	}
	_ = adminURL // kept for assertDevTarget heuristic; admin RPC unused below
	// Single data-audience token. Earlier iterations used a sibling
	// admin token to resolve the caller's tenant via ListTenants —
	// dropped because (a) the data plane resolves tenant from the JWT
	// at handler time and only uses parent's tenant segment for
	// resource-name shape, (b) double-login amplified port-forward
	// flakiness on round trips.
	dataTok, err := login(ctx, httpc, iamURL, user, password, string(auth.AudienceData))
	if err != nil {
		return fmt.Errorf("data login: %w", err)
	}
	dataClients := mcp.NewClients(httpc, "", dataURL, iamURL, dataTok)

	parent := fmt.Sprintf("tenants/%s/collections/%s", tenantHint, collection)

	// Compose a deterministic-looking storage key when the operator
	// didn't pass one. Includes a unix timestamp so re-runs don't
	// collide and we can read the matching ingest log line.
	if storageKey == "" {
		storageKey = fmt.Sprintf("smoke-promote/%d.txt", time.Now().Unix())
	}
	payload := []byte(fmt.Sprintf("smoke-promote-payload at %s\n", time.Now().UTC().Format(time.RFC3339Nano)))

	uresp, err := dataClients.Object.UploadObject(ctx, connect.NewRequest(&datav1.UploadObjectRequest{
		Parent:        parent,
		Key:           storageKey,
		ContentType:   "text/plain",
		SizeHintBytes: int64(len(payload)),
		// The URL is signed for this checksum: the store refuses any other
		// body.
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
		ChecksumValue:     sha256Base64(payload),
	}))
	if err != nil {
		return fmt.Errorf("UploadObject: %w", err)
	}
	obj := uresp.Msg.GetObject()
	url := uresp.Msg.GetUploadUrl()
	fmt.Printf("UploadObject: object_id=%s state=%s key=%s\n",
		obj.GetObjectId(), obj.GetState(), obj.GetKey())
	fmt.Printf("Presigned PUT: %s\n", url.GetUrl())

	// Honour every RequiredHeader the signer specified — Content-Type,
	// Content-Length, the checksum and If-None-Match. A missing one flips
	// SF's S3 gateway to "SignatureDoesNotMatch" with a confusing
	// message; copy verbatim.
	req, err := http.NewRequestWithContext(ctx, url.GetMethod(), url.GetUrl(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("PUT build: %w", err)
	}
	for k, v := range url.GetRequiredHeaders() {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT do: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("PUT response: %d %s\n", resp.StatusCode, string(body))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("PUT failed with %d", resp.StatusCode)
	}

	fmt.Println("\nSkipping CompleteObject on purpose. Watch the ingest pod:")
	fmt.Println("  kubectl logs -n paladin deploy/paladin-ingest -f")
	fmt.Println("Within a few seconds the row should transition PENDING→AVAILABLE")
	fmt.Println("via PromoteHandler. Verify with the data-plane GetObject RPC or:")
	fmt.Printf("  SELECT state FROM objects WHERE object_id = '%s';\n",
		obj.GetObjectId())
	return nil
}

// sha256Base64 is payload's SHA-256 as an upload's checksum_value.
func sha256Base64(payload []byte) string {
	sum := sha256.Sum256(payload)
	return base64.StdEncoding.EncodeToString(sum[:])
}
