package operations

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
)

// limitsCopyRepo serves a batch's sources and a destination bucket with the
// constraints the test chooses.
type limitsCopyRepo struct {
	copyFakeRepo
	sources     []objecth.Object
	constraints uploadpolicy.BucketConstraints
	createdKeys []string
}

func (r *limitsCopyRepo) FindByIDs(context.Context, uuid.UUID, []uuid.UUID) ([]objecth.Object, error) {
	return r.sources, nil
}
func (*limitsCopyRepo) LookupBucket(context.Context, uuid.UUID, string, bool) (string, string, error) {
	return "be", "src-bucket", nil
}
func (r *limitsCopyRepo) LookupBucketMeta(context.Context, uuid.UUID, string, bool) (objecth.BucketMeta, error) {
	return objecth.BucketMeta{BackendID: "be", BucketName: "dst-bucket", Constraints: r.constraints}, nil
}
func (r *limitsCopyRepo) CreateObject(_ context.Context, a objecth.CreateObjectArgs) (objecth.Object, error) {
	r.createdKeys = append(r.createdKeys, a.Key)
	return objecth.Object{ObjectID: uuid.New(), Key: a.Key}, nil
}

var copyLimits = uploadpolicy.Limits{
	MaxObjectSize: 1 << 30, MaxMultipartSize: 1 << 40,
	MinPartSize: uploadpolicy.S3MinPartSize, MaxPartSize: uploadpolicy.S3MaxPartSize, MaxParts: uploadpolicy.S3MaxParts,
}

// BatchCopy created objects in the destination bucket without reading its
// constraints. Each source the destination would refuse is now a per-row
// failure and nothing is created for it; the rest copy as before.
func TestBatchCopyEnforcesDestinationLimits(t *testing.T) {
	tenant := uuid.New()
	png := objecth.Object{ObjectID: uuid.New(), State: statemachine.StateAvailable, Key: "a.png", ContentType: "image/png", SizeBytes: 10}
	exe := objecth.Object{ObjectID: uuid.New(), State: statemachine.StateAvailable, Key: "b.exe", ContentType: "application/x-msdownload", SizeBytes: 10}
	big := objecth.Object{ObjectID: uuid.New(), State: statemachine.StateAvailable, Key: "c.png", ContentType: "image/png", SizeBytes: 1000}
	repo := &limitsCopyRepo{
		sources:     []objecth.Object{png, exe, big},
		constraints: uploadpolicy.BucketConstraints{AllowedContentTypes: []string{"image/png"}, MaxObjectSizeBytes: 100},
	}
	e := &BatchCopyExecutor{
		Objects: repo, Storage: &copyFakeStorage{}, Transitions: &copyFakeTransitioner{},
		PendingTTL: time.Minute, Limits: copyLimits,
	}
	meta, err := json.Marshal(batchh.BatchCopyArgs{
		TenantID: tenant, SrcCollection: "src", DstCollection: "dst",
		ObjectIDs: []uuid.UUID{png.ObjectID, exe.ObjectID, big.ObjectID},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := e.Execute(context.Background(), operationh.Operation{TenantID: tenant, Metadata: meta})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var resp BatchCopyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Succeeded != 1 || resp.Failed != 2 {
		t.Fatalf("succeeded=%d failed=%d, want 1 and 2: %+v", resp.Succeeded, resp.Failed, resp.Failures)
	}
	if len(repo.createdKeys) != 1 || repo.createdKeys[0] != "a.png" {
		t.Fatalf("created %v, want only a.png", repo.createdKeys)
	}
	reasons := map[string]string{}
	for _, f := range resp.Failures {
		reasons[f.ObjectID] = f.Reason
	}
	if !strings.Contains(reasons[exe.ObjectID.String()], "not allowed") {
		t.Errorf("exe failure = %q, want the allowlist named", reasons[exe.ObjectID.String()])
	}
	if !strings.Contains(reasons[big.ObjectID.String()], "maximum object size") {
		t.Errorf("big failure = %q, want the size cap named", reasons[big.ObjectID.String()])
	}
}

func TestExecuteRefusesWithoutLimits(t *testing.T) {
	e := &BatchCopyExecutor{
		Objects: &copyFakeRepo{}, Storage: &copyFakeStorage{}, Transitions: &copyFakeTransitioner{},
		PendingTTL: time.Minute,
	}
	if _, err := e.Execute(context.Background(), operationh.Operation{}); err == nil || !strings.Contains(err.Error(), "limits") {
		t.Fatalf("err = %v, want a limits error", err)
	}
}
