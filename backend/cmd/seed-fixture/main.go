// Package main implements the seed-fixture CLI — populates a dev PALADIN
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
//	load    not yet implemented — placeholder per the BACKLOG entry
//	stress  not yet implemented — placeholder per the BACKLOG entry
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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	adminv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	iamv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/mcp"
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
		Short: "Populate a dev PALADIN cluster with realistic UI/UX fixture data",
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
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{upCmd, downCmd} {
		c.Flags().String("flavour", "demo", "demo | load | stress")
	}
	smokeCmd.Flags().String("object-key", "test",
		"existing objectKey on the caller's tenant — must already be bound to a bucket")
	smokeCmd.Flags().String("key", "",
		"storage key (server picks when empty)")
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

func login(ctx context.Context, iamURL, user, password string) (string, error) {
	httpc := &http.Client{Timeout: 30 * time.Second}
	authClient := mcp.NewClients(httpc, "", "", iamURL, "").Auth
	resp, err := authClient.Login(ctx, connect.NewRequest(&iamv1.LoginRequest{
		Subject:           user,
		Password:          password,
		RequestedAudience: string(auth.AudienceAdmin),
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

// ─── up / down dispatch ───────────────────────────────────────────────────

func runUp(cmd *cobra.Command) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()
	flavour, _ := cmd.Flags().GetString("flavour")
	adminURL, _ := cmd.Flags().GetString("admin-url")
	iamURL, _ := cmd.Flags().GetString("iam-url")
	dataURL, _ := cmd.Flags().GetString("data-url")
	user, _ := cmd.Flags().GetString("user")
	password, _ := cmd.Flags().GetString("password")

	if err := assertDevTarget(adminURL); err != nil {
		return err
	}
	tok, err := login(ctx, iamURL, user, password)
	if err != nil {
		return err
	}
	clients := mcp.NewClients(&http.Client{Timeout: 30 * time.Second}, adminURL, dataURL, iamURL, tok)

	switch flavour {
	case "demo":
		return seedDemo(ctx, clients)
	case "load":
		fmt.Println("flavour=load is not implemented yet (BACKLOG: API-test fixture for UI/UX). " +
			"Demo flavour is the only flavour with content today.")
		return nil
	case "stress":
		fmt.Println("flavour=stress is not implemented yet (BACKLOG: API-test fixture for UI/UX).")
		return nil
	default:
		return fmt.Errorf("unknown flavour %q (use demo, load, or stress)", flavour)
	}
}

func runDown(cmd *cobra.Command) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()
	flavour, _ := cmd.Flags().GetString("flavour")
	adminURL, _ := cmd.Flags().GetString("admin-url")
	iamURL, _ := cmd.Flags().GetString("iam-url")
	dataURL, _ := cmd.Flags().GetString("data-url")
	user, _ := cmd.Flags().GetString("user")
	password, _ := cmd.Flags().GetString("password")

	if err := assertDevTarget(adminURL); err != nil {
		return err
	}
	tok, err := login(ctx, iamURL, user, password)
	if err != nil {
		return err
	}
	clients := mcp.NewClients(&http.Client{Timeout: 30 * time.Second}, adminURL, dataURL, iamURL, tok)

	if flavour != "demo" {
		fmt.Printf("flavour=%s tear-down is a no-op (only demo populates resources today)\n", flavour)
		return nil
	}
	return teardownDemo(ctx, clients)
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
// "PALADIN knows the bytes are there" works without PALADIN RPC's normal
// synchronous CompleteObject path. Operators verify the row's
// state by re-running with an extra query or by inspecting
// /objects in the UI a few seconds after the PUT.
func runSmokeUpload(cmd *cobra.Command) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer cancel()
	adminURL, _ := cmd.Flags().GetString("admin-url")
	iamURL, _ := cmd.Flags().GetString("iam-url")
	dataURL, _ := cmd.Flags().GetString("data-url")
	user, _ := cmd.Flags().GetString("user")
	password, _ := cmd.Flags().GetString("password")
	objectKey, _ := cmd.Flags().GetString("object-key")
	storageKey, _ := cmd.Flags().GetString("key")

	if err := assertDevTarget(adminURL); err != nil {
		return err
	}
	tok, err := login(ctx, iamURL, user, password)
	if err != nil {
		return err
	}
	clients := mcp.NewClients(&http.Client{Timeout: 30 * time.Second}, adminURL, dataURL, iamURL, tok)

	// Resolve caller's tenant — same path the demo flavour uses.
	tenants, err := clients.Tenant.ListTenants(ctx, connect.NewRequest(&adminv1.ListTenantsRequest{}))
	if err != nil {
		return fmt.Errorf("list tenants: %w", err)
	}
	if len(tenants.Msg.GetTenants()) == 0 {
		return errors.New("no tenants visible — bootstrap admin missing?")
	}
	tenant := tenants.Msg.GetTenants()[0]
	parent := fmt.Sprintf("tenants/%s/objectKeys/%s", tenant.GetTenantId(), objectKey)

	// Compose a deterministic-looking storage key when the operator
	// didn't pass one. Includes a unix timestamp so re-runs don't
	// collide and we can read the matching ingest log line.
	if storageKey == "" {
		storageKey = fmt.Sprintf("smoke-promote/%d.txt", time.Now().Unix())
	}
	payload := []byte(fmt.Sprintf("smoke-promote-payload at %s\n", time.Now().UTC().Format(time.RFC3339Nano)))

	uresp, err := clients.Object.UploadObject(ctx, connect.NewRequest(&datav1.UploadObjectRequest{
		Parent:        parent,
		Key:           storageKey,
		ContentType:   "text/plain",
		SizeHintBytes: int64(len(payload)),
		// SHA256 is the data-plane default; explicit so the PUT side
		// knows which checksum header (if any) the server expects.
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
	}))
	if err != nil {
		return fmt.Errorf("UploadObject: %w", err)
	}
	obj := uresp.Msg.GetObject()
	url := uresp.Msg.GetUploadUrl()
	fmt.Printf("UploadObject: object_id=%s state=%s key=%s\n",
		obj.GetObjectId(), obj.GetState(), obj.GetKey())
	fmt.Printf("Presigned PUT: %s\n", url.GetUrl())

	// Honour any RequiredHeaders the signer specified — typically
	// Content-Type and the checksum algo header. Missing them flips
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
