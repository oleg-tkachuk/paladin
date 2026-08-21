//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// TestPolicyChangedNotify proves the trigger baseline (003_triggers.sql): writes to the two Cedar
// policy columns (tenants.inherited_cedar_policy, collections.cedar_policy)
// fire NOTIFY "policy_changed" in exactly the payload shape
// cedar.PostgresStore.Watch parses, and non-policy writes stay silent — so
// the engine's compiled-policy cache invalidates on the event path, not
// just via its TTL.
func TestPolicyChangedNotify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	events, err := cedar.NewPostgresStore(pool).Watch(ctx)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	next := func(what string) cedar.ChangeEvent {
		t.Helper()
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("%s: watch channel closed", what)
			}
			return ev
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: no ChangeEvent within 10s", what)
		}
		panic("unreachable")
	}
	quiet := func(what string) {
		t.Helper()
		select {
		case ev := <-events:
			t.Fatalf("%s: unexpected ChangeEvent %+v", what, ev)
		case <-time.After(300 * time.Millisecond):
		}
	}

	// Tenant-level policy UPDATE → bare-uuid payload (Collection empty).
	mustExec(t, ctx, pool,
		`UPDATE tenants SET inherited_cedar_policy = 'permit(principal, action, resource);' WHERE id = $1`,
		f.tenantID)
	if ev := next("tenant policy update"); ev.TenantID != f.tenantID || ev.Collection != "" {
		t.Fatalf("tenant policy update event = %+v, want {%s \"\"}", ev, f.tenantID)
	}

	// collection policy UPDATE → "uuid:collection" payload.
	mustExec(t, ctx, pool,
		`UPDATE collections SET cedar_policy = 'forbid(principal, action, resource);' WHERE tenant_id = $1 AND name = $2`,
		f.tenantID, f.collection)
	if ev := next("collection policy update"); ev.TenantID != f.tenantID || ev.Collection != f.collection {
		t.Fatalf("collection policy update event = %+v, want {%s %q}", ev, f.tenantID, f.collection)
	}

	// Same-value policy write (IS DISTINCT FROM guard) and a non-policy
	// column write must both stay silent.
	mustExec(t, ctx, pool,
		`UPDATE tenants SET inherited_cedar_policy = inherited_cedar_policy WHERE id = $1`, f.tenantID)
	mustExec(t, ctx, pool,
		`UPDATE tenants SET display_name = display_name || '+' WHERE id = $1`, f.tenantID)
	quiet("no-op policy write + non-policy update")

	// INSERT carrying a policy notifies — the engine caches empty Fetch
	// results for scopes that don't exist yet, so row creation must
	// invalidate. DELETE of a policy-bearing row notifies for the same
	// reason in reverse.
	var backendID, bucketName string
	if err := pool.QueryRow(ctx,
		`SELECT sb.name, b.name
		   FROM collections c
		   JOIN buckets b           ON b.id = c.bucket_id
		   JOIN storage_backends sb ON sb.id = b.backend_id
		  WHERE c.tenant_id = $1 AND c.name = $2`,
		f.tenantID, f.collection,
	).Scan(&backendID, &bucketName); err != nil {
		t.Fatalf("read fixture collection binding: %v", err)
	}
	newKey := f.collection + "-b"
	mustExec(t, ctx, pool,
		`INSERT INTO collections (tenant_id, name, bucket_id, cedar_policy)
		 VALUES ($1, $2, (SELECT b.id FROM buckets b
		          JOIN storage_backends sb ON sb.id = b.backend_id
		         WHERE sb.name = $3 AND b.name = $4), 'permit(principal, action, resource);')`,
		f.tenantID, newKey, backendID, bucketName)
	if ev := next("collection insert with policy"); ev.TenantID != f.tenantID || ev.Collection != newKey {
		t.Fatalf("insert event = %+v, want {%s %q}", ev, f.tenantID, newKey)
	}
	mustExec(t, ctx, pool,
		`DELETE FROM collections WHERE tenant_id = $1 AND name = $2`, f.tenantID, newKey)
	if ev := next("collection delete with policy"); ev.TenantID != f.tenantID || ev.Collection != newKey {
		t.Fatalf("delete event = %+v, want {%s %q}", ev, f.tenantID, newKey)
	}
}
