//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// replicationClockSkew bounds how far the database clock the worker reads may
// sit from the test's own: both run on this host.
const replicationClockSkew = time.Minute

// The replication worker asks the gate Read on the source, from the state the
// listing returns, and BytesWrite on the destination, from its state read by
// backend and bucket name. Each case puts the fixture's backend in a state and
// reads both back; the fixture's bucket is its own destination here, which
// the queries do not care about.
func TestReplicationReadsTheBucketGateStateOfBothSides(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		sql  string
		want bucketgate.State
		// readRefused and writeRefused are the gate's answers on the state.
		readRefused, writeRefused bool
	}{
		{"open", "", bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionReady}, false, false},
		{"draining", drainCollectionBackendSQL, bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}, false, true},
		{"disabled", disableCollectionBackendSQL, bucketgate.State{ProvisionState: bucketgate.ProvisionReady}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			pool := startPostgres(t)
			f := seedFixture(t, ctx, pool)
			bucket := fixtureBucket(t, ctx, pool, f)
			backend := fixtureBackend(t, ctx, pool, f)
			mustExec(t, ctx, pool, `UPDATE buckets SET replication_enabled = TRUE, replication_destination = $2 WHERE name = $1`,
				bucket, "storageBackends/"+backend+"/buckets/"+bucket)
			if tc.sql != "" {
				mustExec(t, ctx, pool, tc.sql, f.collectionID)
			}
			src := adapters.NewLifecycleSource(sqlc.New(pool))

			listed, err := src.ListBucketsWithReplication(ctx)
			if err != nil {
				t.Fatalf("ListBucketsWithReplication: %v", err)
			}
			var found bool
			for _, b := range listed {
				if b.BucketName != bucket {
					continue
				}
				found = true
				if b.State != tc.want || !b.ID.Valid || b.BackendID != backend {
					t.Errorf("listed = (state %+v, id valid %v, backend %s), want (%+v, true, %s)",
						b.State, b.ID.Valid, b.BackendID, tc.want, backend)
				}
				if refused := bucketgate.Check(bucketgate.Read, b.State) != nil; refused != tc.readRefused {
					t.Errorf("source read refused = %v, want %v", refused, tc.readRefused)
				}
			}
			if !found {
				t.Fatalf("bucket %s not listed", bucket)
			}

			dst, err := src.BucketGateState(ctx, backend, bucket)
			if err != nil {
				t.Fatalf("BucketGateState: %v", err)
			}
			if dst.State != tc.want || !dst.ID.Valid {
				t.Errorf("destination state = %+v (id valid %v), want %+v", dst.State, dst.ID.Valid, tc.want)
			}
			if refused := bucketgate.Check(bucketgate.BytesWrite, dst.State) != nil; refused != tc.writeRefused {
				t.Errorf("destination write refused = %v, want %v", refused, tc.writeRefused)
			}
		})
	}
}

// A destination that names no bucket, an object that is not there, and the
// database clock answer as the worker expects.
func TestReplicationSourceEdges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	backend := fixtureBackend(t, ctx, pool, f)
	q := sqlc.New(pool)
	src := adapters.NewLifecycleSource(q)

	if _, err := src.BucketGateState(ctx, backend, "no-such-bucket"); !errors.Is(err, worker.ErrReplicationDestinationUnknown) {
		t.Errorf("BucketGateState of an unknown bucket = %v, want ErrReplicationDestinationUnknown", err)
	}
	st, err := src.ObjectReplicationState(ctx, uuid.New(), uuid.New())
	if err != nil || st.Found {
		t.Errorf("ObjectReplicationState of no row = (%+v, %v), want not found", st, err)
	}
	now, err := adapters.NewReplicationWatermarkRepo(q).Now(ctx)
	if err != nil {
		t.Fatalf("Now: %v", err)
	}
	if d := time.Since(now); d > replicationClockSkew || d < -replicationClockSkew {
		t.Errorf("database clock %v is %v from the test's", now, d)
	}
}
