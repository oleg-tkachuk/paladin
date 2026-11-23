//go:build integration

package components

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// lifecycleGateExpiry is the expiry rule the fixture's bucket carries.
const lifecycleGateExpiry = time.Hour

// The lifecycle worker asks the bucket gate about each bucket it lists, from
// the state the listing returns with it: the bucket's id, its backend's
// enabled and read_only flags and its provision state. Each case puts the
// fixture's backend in a state and reads the listing back.
func TestLifecycleListingCarriesTheBucketGateState(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		sql  string
		want bucketgate.State
		// refused is whether the gate refuses the expiry, a metadata write.
		refused bool
	}{
		{"open", "", bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionReady}, false},
		{"draining", drainCollectionBackendSQL, bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}, false},
		{"disabled", disableCollectionBackendSQL, bucketgate.State{ProvisionState: bucketgate.ProvisionReady}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			pool := startPostgres(t)
			f := seedFixture(t, ctx, pool)
			rules, err := json.Marshal([]admindomain.LifecycleRule{{
				ID: "expire", Enabled: true, Expiration: &admindomain.LifecycleExpiration{After: lifecycleGateExpiry},
			}})
			if err != nil {
				t.Fatal(err)
			}
			bucket := fixtureBucket(t, ctx, pool, f)
			mustExec(t, ctx, pool, `UPDATE buckets SET lifecycle_rules = $2 WHERE name = $1`, bucket, rules)
			if tc.sql != "" {
				mustExec(t, ctx, pool, tc.sql, f.collectionID)
			}

			listed, err := adapters.NewLifecycleSource(sqlc.New(pool)).ListBucketsWithLifecycle(ctx)
			if err != nil {
				t.Fatalf("ListBucketsWithLifecycle: %v", err)
			}
			var found bool
			for _, b := range listed {
				if b.BucketName != bucket {
					continue
				}
				found = true
				if b.State != tc.want {
					t.Errorf("state = %+v, want %+v", b.State, tc.want)
				}
				if !b.ID.Valid {
					t.Error("the listed bucket carries no id for the gate to defer it by")
				}
				if b.BackendID != fixtureBackend(t, ctx, pool, f) || len(b.LifecycleRules) != 1 {
					t.Errorf("bucket = %+v, want the fixture's backend and its one rule", b.Bucket)
				}
				if refused := bucketgate.Check(bucketgate.MetadataWrite, b.State) != nil; refused != tc.refused {
					t.Errorf("expiry refused = %v, want %v", refused, tc.refused)
				}
			}
			if !found {
				t.Fatalf("bucket %s not listed", bucket)
			}
		})
	}
}
