package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	pb "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/batch"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/operation"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/storagebootstrap"
)

// The same property object_server_test.go pins, for the other five servers: a
// handler failure must reach the caller AS a failure, wrapping the original.
//
// A shim that swallowed one and returned its zero value answers a client with
// 200 and an empty result — "the batch job started, id 00000000-…", "there are
// no parts", "the operation does not exist". Each of those is a lie the caller
// cannot tell from the truth.
//
// Every error branch in these files was unheld before the seams: a mutation
// run over the package produced survivors in each, because standing up a real
// handler needs a database and nothing below the slow gate did.

var errBoom = errors.New("backend unavailable")

type failingBatch struct{}

func (failingBatch) BatchDelete(context.Context, batch.BatchDeleteArgs) (uuid.UUID, error) {
	return uuid.Nil, errBoom
}
func (failingBatch) BatchCopy(context.Context, batch.BatchCopyArgs) (uuid.UUID, error) {
	return uuid.Nil, errBoom
}
func (failingBatch) BatchRestoreObjects(context.Context, batch.BatchRestoreObjectsArgs) (uuid.UUID, error) {
	return uuid.Nil, errBoom
}
func (failingBatch) BatchUpdateTags(context.Context, batch.BatchUpdateTagsArgs) (uuid.UUID, error) {
	return uuid.Nil, errBoom
}

type failingMultipart struct{}

func (failingMultipart) InitiateMultipartUpload(context.Context, multipart.InitiateArgs) (*multipart.Session, error) {
	return nil, errBoom
}
func (failingMultipart) PresignPart(context.Context, string, int32, time.Duration, multipart.SessionRef) (string, map[string]string, time.Time, error) {
	return "", nil, time.Time{}, errBoom
}
func (failingMultipart) ListParts(context.Context, string, int32, string, multipart.SessionRef) ([]multipart.Part, string, error) {
	return nil, "", errBoom
}
func (failingMultipart) CompleteMultipartUpload(context.Context, multipart.CompleteArgs) error {
	return errBoom
}
func (failingMultipart) AbortMultipartUpload(context.Context, string, multipart.SessionRef) error {
	return errBoom
}

type failingTags struct{}

func (failingTags) GetObject(context.Context, string, string) (*object.Object, error) {
	return nil, errBoom
}
func (failingTags) UpdateObject(context.Context, object.UpdateObjectInput) (*object.Object, error) {
	return nil, errBoom
}
func (failingTags) ListDistinctTags(context.Context, string, string, int32) (object.DistinctTagPage, error) {
	return object.DistinctTagPage{}, errBoom
}

type failingPresign struct{}

func (failingPresign) PresignGet(context.Context, string, string, time.Duration, string) (string, map[string]string, time.Time, error) {
	return "", nil, time.Time{}, errBoom
}
func (failingPresign) PresignPut(context.Context, string, string, string, string, time.Duration, int64) (string, map[string]string, time.Time, error) {
	return "", nil, time.Time{}, errBoom
}

type failingOperations struct{}

func (failingOperations) GetOperation(context.Context, uuid.UUID) (*operation.Operation, error) {
	return nil, errBoom
}
func (failingOperations) ListOperations(context.Context, *operation.State, int32, string, string, bool) ([]operation.Operation, string, error) {
	return nil, "", errBoom
}
func (failingOperations) CancelOperation(context.Context, uuid.UUID) error { return errBoom }

type failingBootstrap struct{}

func (failingBootstrap) EnsureTenantStorage(context.Context, string, string, []string) (*storagebootstrap.Result, error) {
	return nil, errBoom
}

func TestEveryShimPropagatesHandlerErrors(t *testing.T) {
	ctx := ctxTenant(tenantA)
	parent := "tenants/" + tenantA.String() + "/collections/c1"
	objName := parent + "/objects/" + objUUID.String()
	opName := "operations/" + objUUID.String()

	batchSrv := &BatchServer{H: failingBatch{}}
	mpSrv := &MultipartServer{H: failingMultipart{}}
	tagSrv := &ObjectTagServer{H: failingTags{}}
	psSrv := &PresignServer{H: failingPresign{}}
	opSrv := &OperationServer{H: failingOperations{}}
	sbSrv := &StorageBootstrapServer{H: failingBootstrap{}}

	cases := []struct {
		name string
		call func() error
	}{
		{"BatchDeleteObjects", func() error {
			_, err := batchSrv.BatchDeleteObjects(ctx, connect.NewRequest(&pb.BatchDeleteObjectsRequest{
				Parent:   parent,
				Selector: &pb.ObjectSelector{Names: []string{objName}},
			}))
			return err
		}},
		{"InitiateMultipartUpload", func() error {
			_, err := mpSrv.InitiateMultipartUpload(ctx, connect.NewRequest(&pb.InitiateMultipartUploadRequest{
				Parent: parent, Key: "k", ContentType: "text/plain",
			}))
			return err
		}},
		{"GetObjectTags", func() error {
			_, err := tagSrv.GetObjectTags(ctx, connect.NewRequest(&pb.GetObjectTagsRequest{Name: objName}))
			return err
		}},
		{"PresignDownload", func() error {
			_, err := psSrv.PresignDownload(ctx, connect.NewRequest(&pb.PresignDownloadRequest{Name: objName}))
			return err
		}},
		{"GetOperation", func() error {
			_, err := opSrv.GetOperation(ctx, connect.NewRequest(&pb.GetOperationRequest{Name: opName}))
			return err
		}},
		{"BatchCopyObjects", func() error {
			_, err := batchSrv.BatchCopyObjects(ctx, connect.NewRequest(&pb.BatchCopyObjectsRequest{
				SourceParent: parent, DestinationCollection: parent,
				Selector: &pb.ObjectSelector{Names: []string{objName}},
			}))
			return err
		}},
		{"BatchRestoreObjects", func() error {
			_, err := batchSrv.BatchRestoreObjects(ctx, connect.NewRequest(&pb.BatchRestoreObjectsRequest{
				Parent: parent, Selector: &pb.ObjectSelector{Names: []string{objName}},
			}))
			return err
		}},
		{"BatchUpdateTags", func() error {
			_, err := batchSrv.BatchUpdateTags(ctx, connect.NewRequest(&pb.BatchUpdateTagsRequest{
				Parent: parent, Selector: &pb.ObjectSelector{Names: []string{objName}},
				Tags: map[string]string{"k": "v"},
			}))
			return err
		}},
		{"PresignPart", func() error {
			_, err := mpSrv.PresignPart(ctx, connect.NewRequest(&pb.PresignPartRequest{
				ObjectName: objName, UploadId: "u1", PartNumber: 1,
			}))
			return err
		}},
		{"CompleteMultipartUpload", func() error {
			_, err := mpSrv.CompleteMultipartUpload(ctx, connect.NewRequest(&pb.CompleteMultipartUploadRequest{
				ObjectName: objName, UploadId: "u1",
			}))
			return err
		}},
		{"AbortMultipartUpload", func() error {
			_, err := mpSrv.AbortMultipartUpload(ctx, connect.NewRequest(&pb.AbortMultipartUploadRequest{
				ObjectName: objName, UploadId: "u1",
			}))
			return err
		}},
		{"ListParts", func() error {
			_, err := mpSrv.ListParts(ctx, connect.NewRequest(&pb.ListPartsRequest{
				ObjectName: objName, UploadId: "u1",
			}))
			return err
		}},
		{"PutObjectTags", func() error {
			_, err := tagSrv.PutObjectTags(ctx, connect.NewRequest(&pb.PutObjectTagsRequest{
				Name: objName, ResourceVersion: "1", Tags: map[string]string{"k": "v"},
			}))
			return err
		}},
		{"DeleteObjectTags", func() error {
			_, err := tagSrv.DeleteObjectTags(ctx, connect.NewRequest(&pb.DeleteObjectTagsRequest{
				Name: objName, ResourceVersion: "1", Keys: []string{"k"},
			}))
			return err
		}},
		{"ListDistinctTags", func() error {
			_, err := tagSrv.ListDistinctTags(ctx, connect.NewRequest(&pb.ListDistinctTagsRequest{
				Parent: parent,
			}))
			return err
		}},
		{"RegenerateUploadUrl", func() error {
			_, err := psSrv.RegenerateUploadUrl(ctx, connect.NewRequest(&pb.RegenerateUploadUrlRequest{
				Name: objName,
			}))
			return err
		}},
		{"ListOperations", func() error {
			_, err := opSrv.ListOperations(ctx, connect.NewRequest(&pb.ListOperationsRequest{}))
			return err
		}},
		{"CancelOperation", func() error {
			_, err := opSrv.CancelOperation(ctx, connect.NewRequest(&pb.CancelOperationRequest{Name: opName}))
			return err
		}},
		{"EnsureTenantStorage", func() error {
			_, err := sbSrv.EnsureTenantStorage(ctx, connect.NewRequest(&pb.EnsureTenantStorageRequest{
				BackendId: "primary", Bucket: "b",
			}))
			return err
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.call()
			if err == nil {
				t.Fatal("the handler failed and the shim answered success — a " +
					"client reads that as an empty result rather than an outage")
			}
			if !errors.Is(err, errBoom) {
				t.Errorf("error %v does not wrap the handler's — the cause has to "+
					"survive the shim or the log line names the wrong layer", err)
			}
		})
	}
}

// Where a shim makes MORE THAN ONE call, "an error came back" is not the same
// as "the right error came back".
//
// CancelOperation cancels and then re-reads; DeleteObjectTags reads, edits and
// writes. With a double that fails everything, dropping the first check still
// produces an error from the second call, so the mutation survives and the
// test cannot see the difference. These doubles fail exactly one step.
type cancelFailsGetSucceeds struct{ failingOperations }

func (cancelFailsGetSucceeds) GetOperation(context.Context, uuid.UUID) (*operation.Operation, error) {
	return &operation.Operation{}, nil
}

type readOKWriteFails struct{ failingTags }

func (readOKWriteFails) GetObject(context.Context, string, string) (*object.Object, error) {
	return &object.Object{Tags: map[string]string{"k": "v"}}, nil
}

func TestTheFAILINGStepIsTheOneReported(t *testing.T) {
	ctx := ctxTenant(tenantA)
	parent := "tenants/" + tenantA.String() + "/collections/c1"
	objName := parent + "/objects/" + objUUID.String()

	t.Run("cancel fails even though the re-read would succeed", func(t *testing.T) {
		srv := &OperationServer{H: cancelFailsGetSucceeds{}}
		_, err := srv.CancelOperation(ctx, connect.NewRequest(&pb.CancelOperationRequest{
			Name: "operations/" + objUUID.String(),
		}))
		if err == nil {
			t.Fatal("the cancel failed and the shim reported success because the " +
				"following read worked — the caller believes the operation stopped")
		}
		if !errors.Is(err, errBoom) {
			t.Errorf("error %v is not the cancel's", err)
		}
	})

	t.Run("the write fails even though the read succeeded", func(t *testing.T) {
		srv := &ObjectTagServer{H: readOKWriteFails{}}
		_, err := srv.DeleteObjectTags(ctx, connect.NewRequest(&pb.DeleteObjectTagsRequest{
			Name: objName, ResourceVersion: "1", Keys: []string{"k"},
		}))
		if err == nil {
			t.Fatal("the tag write failed and the shim reported success — the " +
				"caller believes the tags are gone")
		}
		if !errors.Is(err, errBoom) {
			t.Errorf("error %v is not the write's", err)
		}
	})

	// resolveObjectIDs parses each name in a batch selector; a bad one must be
	// refused rather than silently dropped from the batch.
	t.Run("a malformed name in a batch selector", func(t *testing.T) {
		srv := &BatchServer{H: failingBatch{}}
		_, err := srv.BatchDeleteObjects(ctx, connect.NewRequest(&pb.BatchDeleteObjectsRequest{
			Parent:   parent,
			Selector: &pb.ObjectSelector{Names: []string{parent + "/objects/not-a-uuid"}},
		}))
		if err == nil {
			t.Fatal("a batch accepted a name that does not parse")
		}
		if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
			t.Errorf("code = %v, want invalid_argument", got)
		}
	})
}
