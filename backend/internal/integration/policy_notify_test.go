//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin-private/internal/policy/cedar"
)

// TestPolicyChangedNotify proves migration 051: writes to the two Cedar
// policy columns (tenants.inherited_cedar_policy, object_keys.cedar_policy)
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

	// Tenant-level policy UPDATE → bare-uuid payload (ObjectKey empty).
	mustExec(t, ctx, pool,
		`UPDATE tenants SET inherited_cedar_policy = 'permit(principal, action, resource);' WHERE tenant_id = $1`,
		f.tenantID)
	if ev := next("tenant policy update"); ev.TenantID != f.tenantID || ev.ObjectKey != "" {
		t.Fatalf("tenant policy update event = %+v, want {%s \"\"}", ev, f.tenantID)
	}

	// objectKey policy UPDATE → "uuid:objectKey" payload.
	mustExec(t, ctx, pool,
		`UPDATE object_keys SET cedar_policy = 'forbid(principal, action, resource);' WHERE tenant_id = $1 AND object_key = $2`,
		f.tenantID, f.objectKey)
	if ev := next("object_key policy update"); ev.TenantID != f.tenantID || ev.ObjectKey != f.objectKey {
		t.Fatalf("object_key policy update event = %+v, want {%s %q}", ev, f.tenantID, f.objectKey)
	}

	// Same-value policy write (IS DISTINCT FROM guard) and a non-policy
	// column write must both stay silent.
	mustExec(t, ctx, pool,
		`UPDATE tenants SET inherited_cedar_policy = inherited_cedar_policy WHERE tenant_id = $1`, f.tenantID)
	mustExec(t, ctx, pool,
		`UPDATE tenants SET display_name = display_name || '+' WHERE tenant_id = $1`, f.tenantID)
	quiet("no-op policy write + non-policy update")

	// INSERT carrying a policy notifies — the engine caches empty Fetch
	// results for scopes that don't exist yet, so row creation must
	// invalidate. DELETE of a policy-bearing row notifies for the same
	// reason in reverse.
	var backendID, bucketName string
	if err := pool.QueryRow(ctx,
		`SELECT backend_id, bucket_name FROM object_keys WHERE tenant_id = $1 AND object_key = $2`,
		f.tenantID, f.objectKey,
	).Scan(&backendID, &bucketName); err != nil {
		t.Fatalf("read fixture object_key binding: %v", err)
	}
	newKey := f.objectKey + "-b"
	mustExec(t, ctx, pool,
		`INSERT INTO object_keys (tenant_id, object_key, backend_id, bucket_name, cedar_policy)
		 VALUES ($1, $2, $3, $4, 'permit(principal, action, resource);')`,
		f.tenantID, newKey, backendID, bucketName)
	if ev := next("object_key insert with policy"); ev.TenantID != f.tenantID || ev.ObjectKey != newKey {
		t.Fatalf("insert event = %+v, want {%s %q}", ev, f.tenantID, newKey)
	}
	mustExec(t, ctx, pool,
		`DELETE FROM object_keys WHERE tenant_id = $1 AND object_key = $2`, f.tenantID, newKey)
	if ev := next("object_key delete with policy"); ev.TenantID != f.tenantID || ev.ObjectKey != newKey {
		t.Fatalf("delete event = %+v, want {%s %q}", ev, f.tenantID, newKey)
	}
}
