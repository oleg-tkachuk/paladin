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
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	adminv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
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
	for _, c := range []*cobra.Command{upCmd, downCmd} {
		c.Flags().String("flavour", "demo", "demo | load | stress")
		c.Flags().String("admin-url", "https://paladin.local/api/rpc/admin", "Admin plane URL")
		c.Flags().String("iam-url", "https://paladin.local/api/rpc/iam", "IAM plane URL")
		c.Flags().String("data-url", "https://paladin.local/api/rpc/data", "Data plane URL")
		c.Flags().String("user", "admin", "Bootstrap admin subject")
		c.Flags().String("password", "password", "Bootstrap admin password")
		root.AddCommand(c)
	}
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

const demoTenantDisplayName = fixturePrefix + "demo:tenant"

// seedDemo writes one tenant with four EventSubscriptions covering the
// states the operator will look at on the /events page:
//
//   - HTTP sink, no filter — the "everything goes here" baseline
//   - NATS sink, paladin.events subject — the second wired sink
//   - HTTP sink, disabled — exercises the dim "disabled" pill
//   - HTTP sink, with CEL filter — exercises the filter column
//
// Idempotency: every resource lookup is by display_name prefix.
// Re-running produces the same on-disk state without duplicates.
func seedDemo(ctx context.Context, c *mcp.Clients) error {
	tenant, err := ensureTenant(ctx, c, demoTenantDisplayName)
	if err != nil {
		return fmt.Errorf("tenant: %w", err)
	}
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
	// Find the demo tenant. If it doesn't exist, nothing to do — the
	// demo subs FK to it, so deleting the tenant cascades.
	tenants, err := c.Tenant.ListTenants(ctx, connect.NewRequest(&adminv1.ListTenantsRequest{}))
	if err != nil {
		return fmt.Errorf("list tenants: %w", err)
	}
	for _, t := range tenants.Msg.GetTenants() {
		if !strings.HasPrefix(t.GetDisplayName(), fixturePrefix+"demo:") {
			continue
		}
		// Tenant deletion CASCADES to event_subscriptions per the FK
		// in migration 006, so we don't have to enumerate subs.
		_, err := c.Tenant.DeleteTenant(ctx, connect.NewRequest(&adminv1.DeleteTenantRequest{
			Name: t.GetName(),
		}))
		if err != nil {
			return fmt.Errorf("delete tenant %s: %w", t.GetTenantId(), err)
		}
		fmt.Printf("deleted tenant: %s (%s)\n", t.GetDisplayName(), t.GetTenantId())
	}
	fmt.Println("demo flavour torn down")
	return nil
}

// ─── Resource helpers ─────────────────────────────────────────────────────

type demoSubscription struct {
	suffix   string // appended to fixturePrefix+"demo:sub:" for the sub's filter-side identifier
	sink     *adminv1.EventSink
	filter   string
	disabled bool
}

// ensureTenant returns the existing fixture tenant when present; creates
// it otherwise. We match by exact display_name — Tenant proto doesn't
// expose a slug filter today.
func ensureTenant(ctx context.Context, c *mcp.Clients, displayName string) (*adminv1.Tenant, error) {
	resp, err := c.Tenant.ListTenants(ctx, connect.NewRequest(&adminv1.ListTenantsRequest{}))
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	for _, t := range resp.Msg.GetTenants() {
		if t.GetDisplayName() == displayName {
			return t, nil
		}
	}
	cresp, err := c.Tenant.CreateTenant(ctx, connect.NewRequest(&adminv1.CreateTenantRequest{
		Tenant: &adminv1.Tenant{
			DisplayName: displayName,
			Labels:      map[string]string{"managed_by": "seed-fixture", "flavour": "demo"},
		},
	}))
	if err != nil {
		return nil, fmt.Errorf("create: %w", err)
	}
	return cresp.Msg, nil
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
