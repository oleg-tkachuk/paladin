package worker

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// openBucketState is a bucket the gate admits every access on.
var openBucketState = bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionReady}

// lineGateDeferred is the line bucketDeferrals logs a refused bucket with.
const lineGateDeferred = "the bucket gate refuses this bucket; its work is deferred until the gate admits it"

// The lifecycle expiry moves objects to the trash, a metadata write: only a
// disabled backend's bucket is skipped, logged once at Warn with the gate's
// reason, and its objects stay as they are; a draining one, or a bucket of a
// backend the gate admits, is expired. A refused bucket listed first does not
// stop the buckets behind it.
func TestLifecycleExpiryAsksTheGateAsAMetadataWrite(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   bucketgate.State
		refused bool
	}{
		{"open", openBucketState, false},
		{"draining", bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}, false},
		{"disabled", bucketgate.State{Enabled: false, ProvisionState: bucketgate.ProvisionReady}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := []admindomain.LifecycleRule{{
				ID: "expire", Enabled: true,
				Expiration: &admindomain.LifecycleExpiration{After: time.Hour},
			}}
			gated := LifecycleBucket{
				Bucket: admindomain.Bucket{BackendID: "gated", BucketName: "gated-bucket", LifecycleRules: rule},
				ID:     pgtype.UUID{Bytes: uuid.New(), Valid: true}, State: tc.state,
			}
			open := LifecycleBucket{
				Bucket: admindomain.Bucket{BackendID: "open", BucketName: "open-bucket", LifecycleRules: rule},
				ID:     pgtype.UUID{Bytes: uuid.New(), Valid: true}, State: openBucketState,
			}
			gatedTenant, openTenant := uuid.New(), uuid.New()
			src := &fakeBucketSource{
				buckets: []LifecycleBucket{gated, open},
				bindings: map[string][]CollectionBinding{
					"gated/gated-bucket": {{TenantID: gatedTenant, Collection: "k"}},
					"open/open-bucket":   {{TenantID: openTenant, Collection: "k"}},
				},
			}
			objs := &tenantObjIter{rows: map[uuid.UUID][]LifecycleObjectRow{
				gatedTenant: {{ObjectID: uuid.NewV7(), State: string(statemachine.StateAvailable), CreatedAt: time.Now().Add(-2 * time.Hour)}},
				openTenant:  {{ObjectID: uuid.NewV7(), State: string(statemachine.StateAvailable), CreatedAt: time.Now().Add(-2 * time.Hour)}},
			}}
			sd := &fakeSoftDeleter{}
			core, logs := observer.New(zapcore.DebugLevel)
			w := &LifecycleWorker{Buckets: src, Objects: objs, SoftDeleter: sd, Now: time.Now, Logger: zap.New(core)}

			if err := w.tick(context.Background()); err != nil {
				t.Fatalf("tick = %v, want nil: a deferral is not a failure", err)
			}

			gatedID, openID := objs.rows[gatedTenant][0].ObjectID, objs.rows[openTenant][0].ObjectID
			deleted := map[uuid.UUID]bool{}
			for _, id := range sd.deleted {
				deleted[id] = true
			}
			if !deleted[openID] {
				t.Error("the open bucket's object was not expired")
			}
			deferrals := logs.FilterMessage(lineGateDeferred).All()
			if tc.refused {
				if deleted[gatedID] {
					t.Errorf("the object on a %s backend was expired", tc.name)
				}
				if len(deferrals) != 1 || deferrals[0].Level != zapcore.WarnLevel {
					t.Fatalf("deferral lines = %v, want one Warn", deferrals)
				}
				ctx := deferrals[0].ContextMap()
				if ctx["reason"] != bucketgate.ErrBackendDisabled.Error() || ctx["bucket_name"] != "gated-bucket" ||
					ctx["access"] != bucketgate.MetadataWrite.String() {
					t.Errorf("deferral fields = %v", ctx)
				}
				return
			}
			if !deleted[gatedID] {
				t.Errorf("the object on a %s backend was not expired; a metadata write is admitted there", tc.name)
			}
			if len(deferrals) != 0 {
				t.Errorf("deferral lines = %v, want none", deferrals)
			}
		})
	}
}

// tenantObjIter serves each tenant its own rows.
type tenantObjIter struct {
	rows map[uuid.UUID][]LifecycleObjectRow
}

func (f *tenantObjIter) IterateObjects(_ context.Context, tenantID uuid.UUID, _ string, cb func(LifecycleObjectRow) error) error {
	for _, r := range f.rows[tenantID] {
		if err := cb(r); err != nil {
			return err
		}
	}
	return nil
}
