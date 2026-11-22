//go:build integration

package integration

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// The fixture the taint gate test seeds.
const (
	taintGateBackend    = "taint-gate-be"
	taintGateBucket     = "taint-gate-bucket"
	taintGateCollection = "docs"
	taintGateKey        = "flagged.txt"
	// taintGateCalls is how many SetTaint calls each case makes: a set and a
	// clear, each decided by Cedar once.
	taintGateCalls = 2
)

// recordingAllow allows every check and keeps the resources it was asked
// about, so a test can see the binding a write was decided on.
type recordingAllow struct{ resources []cedar.Resource }

func (a *recordingAllow) IsAuthorized(_ context.Context, _ *cedar.Principal, _ cedar.Action, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.resources = append(a.resources, *r)
	return cedar.DecisionAllow, nil
}

// A taint is a protective control: no state of the backend or the bucket
// refuses it. On a draining backend, which still serves the object, on a
// disabled one and on a bucket still provisioning, SetObjectTaint sets and
// clears the flag against the real store, decided by Cedar on the binding the
// resolver read, while a write of bytes stays refused.
func TestSetTaintIgnoresBucketState(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	q := sqlc.New(h.PoolMigrate)
	objects := adapters.NewObjectRepo(q, h.PoolMigrate)
	be := adapters.NewBackendRepoV2(q, h.PoolMigrate)

	tenantID := mustCreateTenant(t, h.PoolMigrate, "taint-gate-tenant")
	seedBackend(t, h.PoolMigrate, taintGateBackend)
	mustSeedBucketAndKey(t, h.PoolMigrate, tenantID, taintGateBackend, taintGateBucket, taintGateCollection)
	mustInsertObject(t, h.PoolMigrate, tenantID, taintGateCollection, taintGateKey)
	var objectID uuid.UUID
	if err := h.PoolMigrate.QueryRow(ctx,
		`SELECT id FROM objects WHERE tenant_id = $1 AND path = $2`, tenantID, taintGateKey).Scan(&objectID); err != nil {
		t.Fatalf("read object id: %v", err)
	}

	setState := func(t *testing.T, enabled, readOnly bool, provision string) {
		t.Helper()
		if _, err := h.PoolMigrate.Exec(ctx,
			`UPDATE buckets SET provision_state = $1 WHERE name = $2`, provision, taintGateBucket); err != nil {
			t.Fatalf("set provision_state=%s: %v", provision, err)
		}
		cur, err := be.Get(ctx, taintGateBackend)
		if err != nil {
			t.Fatalf("get backend: %v", err)
		}
		if err := be.SetReadOnly(ctx, taintGateBackend, readOnly, cur.ResourceVersion); err != nil {
			t.Fatalf("set read_only=%v: %v", readOnly, err)
		}
		cur, err = be.Get(ctx, taintGateBackend)
		if err != nil {
			t.Fatalf("get backend: %v", err)
		}
		if err := be.SetEnabled(ctx, taintGateBackend, enabled, cur.ResourceVersion); err != nil {
			t.Fatalf("set enabled=%v: %v", enabled, err)
		}
	}

	for _, tc := range []struct {
		name      string
		enabled   bool
		readOnly  bool
		provision string
		// bytesWrite is the refusal an upload gets in the same state.
		bytesWrite error
	}{
		{"draining", true, true, bucketgate.ProvisionReady, bucketgate.ErrBackendReadOnly},
		{"disabled", false, false, bucketgate.ProvisionReady, bucketgate.ErrBackendDisabled},
		{"provisioning", true, false, bucketgate.ProvisionPending, bucketgate.ErrBucketProvisioning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setState(t, tc.enabled, tc.readOnly, tc.provision)
			defer setState(t, true, false, bucketgate.ProvisionReady)

			if _, _, err := objects.LookupBucket(ctx, tenantID, taintGateCollection, bucketgate.BytesWrite); !errors.Is(err, tc.bytesWrite) {
				t.Fatalf("bytes-write lookup = %v, want %v", err, tc.bytesWrite)
			}

			authz := &recordingAllow{}
			handler := objecth.NewTaintHandler(objects, adapters.NewObjectTaintRepo(q), authz)
			caller := auth.WithPrincipal(ctx, &auth.Principal{Subject: "u1", TenantID: tenantID})

			set, err := handler.SetTaint(caller, taintGateCollection, objectID.String(), []string{objecth.TaintPII})
			if err != nil {
				t.Fatalf("SetTaint = %v, want the flag set", err)
			}
			if !slices.Equal(set.Taint, []string{objecth.TaintPII}) {
				t.Fatalf("stored taint = %v, want [%s]", set.Taint, objecth.TaintPII)
			}
			cleared, err := handler.SetTaint(caller, taintGateCollection, objectID.String(), nil)
			if err != nil || len(cleared.Taint) != 0 {
				t.Fatalf("clearing SetTaint = %v, %v; want the flag cleared", cleared, err)
			}

			if len(authz.resources) != taintGateCalls {
				t.Fatalf("Cedar was asked %d times, want once per call", len(authz.resources))
			}
			for _, r := range authz.resources {
				if r.BackendID != taintGateBackend || r.BucketName != taintGateBucket {
					t.Fatalf("decided on binding (%q, %q), want (%q, %q)", r.BackendID, r.BucketName, taintGateBackend, taintGateBucket)
				}
			}
		})
	}
}
