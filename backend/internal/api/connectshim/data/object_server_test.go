package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
)

// failingHandler fails every call. The seam exists so this can be written at
// all — the field was a concrete *object.Handler, and standing one up needs a
// repo, a storage client and a database.
type failingHandler struct{ err error }

func (f failingHandler) UploadObject(context.Context, object.UploadObjectInput) (*object.UploadObjectOutput, error) {
	return nil, f.err
}
func (f failingHandler) CompleteObject(context.Context, object.CompleteObjectInput) (*object.Object, error) {
	return nil, f.err
}
func (f failingHandler) ListObjects(context.Context, object.ListObjectsInput) ([]object.Object, string, error) {
	return nil, "", f.err
}
func (f failingHandler) CountObjects(context.Context, object.CountObjectsInput) (*object.CountObjectsOutput, error) {
	return nil, f.err
}
func (f failingHandler) GetObject(context.Context, string, string) (*object.Object, error) {
	return nil, f.err
}
func (f failingHandler) LookupObject(context.Context, string, string) (*object.Object, error) {
	return nil, f.err
}
func (f failingHandler) DownloadObject(context.Context, string, string, time.Duration, string) (*object.DownloadObjectOutput, error) {
	return nil, f.err
}
func (f failingHandler) UpdateObject(context.Context, object.UpdateObjectInput) (*object.Object, error) {
	return nil, f.err
}
func (f failingHandler) DeleteObject(context.Context, string, string, string, bool, bool) error {
	return f.err
}
func (f failingHandler) RestoreObject(context.Context, string, string, string) (*object.Object, error) {
	return nil, f.err
}
func (f failingHandler) CopyObject(context.Context, object.CopyObjectInput) (*object.Object, error) {
	return nil, f.err
}

// ONE test, not one per branch.
//
// Every error branch in this file is the same passthrough — `if err != nil {
// return nil, err }` — and a test apiece would be ceremony. What matters is the
// property they share: a handler failure must reach the caller AS a failure.
// A shim that swallowed one and returned its zero value answers a client with
// 200 and an empty object, which reads as "the object exists and is empty".
//
// Driven per RPC through a table so a new one added without the branch is
// caught here rather than in a stack run.
func TestHandlerErrorsReachTheCaller(t *testing.T) {
	boom := errors.New("backend unavailable")
	s := &ObjectServer{H: failingHandler{err: boom}}
	// A principal on the context, because the shim asserts the JWT tenant
	// matches the resource name before it calls the handler — without one the
	// request is refused up front and the handler's error is never reached,
	// which is what the first version of this test measured.
	ctx := ctxTenant(tenantA)

	// Names that PARSE — the point is the handler's error, not a malformed
	// request rejected before the handler is reached.
	objName := "tenants/" + tenantA.String() + "/collections/c1/objects/00000000-0000-0000-0000-000000000001"
	parent := "tenants/" + tenantA.String() + "/collections/c1"

	cases := []struct {
		name string
		call func() error
	}{
		{"GetObject", func() error {
			_, err := s.GetObject(ctx, connect.NewRequest(&pb.GetObjectRequest{Name: objName}))
			return err
		}},
		{"DownloadObject", func() error {
			_, err := s.DownloadObject(ctx, connect.NewRequest(&pb.DownloadObjectRequest{Name: objName}))
			return err
		}},
		{"ListObjects", func() error {
			_, err := s.ListObjects(ctx, connect.NewRequest(&pb.ListObjectsRequest{Parent: parent}))
			return err
		}},
		{"CountObjects", func() error {
			_, err := s.CountObjects(ctx, connect.NewRequest(&pb.CountObjectsRequest{Parent: parent}))
			return err
		}},
		{"UpdateObject", func() error {
			_, err := s.UpdateObject(ctx, connect.NewRequest(&pb.UpdateObjectRequest{
				Name: objName, ResourceVersion: "1",
			}))
			return err
		}},
		{"CopyObject", func() error {
			_, err := s.CopyObject(ctx, connect.NewRequest(&pb.CopyObjectRequest{
				SourceName: objName, DestinationCollection: parent, DestinationKey: "copy",
			}))
			return err
		}},
		{"DeleteObject", func() error {
			_, err := s.DeleteObject(ctx, connect.NewRequest(&pb.DeleteObjectRequest{Name: objName}))
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
			if !errors.Is(err, boom) {
				t.Errorf("error %v does not wrap the handler's — the cause has to "+
					"survive the shim or the log line names the wrong layer", err)
			}
		})
	}
}
