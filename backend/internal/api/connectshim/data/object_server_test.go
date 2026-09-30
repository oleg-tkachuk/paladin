package data

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/object"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
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

type failingVersions struct{ err error }

func (f failingVersions) ListVersions(context.Context, object.ListVersionsInput) ([]object.ObjectVersion, string, error) {
	return nil, "", f.err
}
func (f failingVersions) GetVersion(context.Context, string) (*object.ObjectVersion, error) {
	return nil, f.err
}
func (f failingVersions) RestoreVersion(context.Context, string, string) (*object.Object, error) {
	return nil, f.err
}

type failingLocks struct{ err error }

func (f failingLocks) SetRetention(context.Context, object.SetRetentionInput) (object.ObjectLock, error) {
	return object.ObjectLock{}, f.err
}
func (f failingLocks) SetLegalHold(context.Context, string, string, bool) (object.ObjectLock, error) {
	return object.ObjectLock{}, f.err
}
func (f failingLocks) GetLock(context.Context, string, string) (object.ObjectLock, error) {
	return object.ObjectLock{}, f.err
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
	// All three fields, because they are three independently wired handlers
	// and the shim's error branches are spread across them.
	s := &ObjectServer{
		H:        failingHandler{err: boom},
		Versions: failingVersions{err: boom},
		Locks:    failingLocks{err: boom},
	}
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
		{"ListObjectVersions", func() error {
			_, err := s.ListObjectVersions(ctx, connect.NewRequest(&pb.ListObjectVersionsRequest{Parent: objName}))
			return err
		}},
		{"GetObjectVersion", func() error {
			// A version name carries its own /versions/{id} segment; without it
			// the shim refuses before the handler and the test measures the
			// wrong layer.
			_, err := s.GetObjectVersion(ctx, connect.NewRequest(&pb.GetObjectVersionRequest{
				Name: objName + "/versions/44444444-4444-4444-4444-444444444444",
			}))
			return err
		}},
		{"GetObjectLock", func() error {
			_, err := s.GetObjectLock(ctx, connect.NewRequest(&pb.GetObjectLockRequest{Name: objName}))
			return err
		}},
		{"SetObjectLegalHold", func() error {
			_, err := s.SetObjectLegalHold(ctx, connect.NewRequest(&pb.SetObjectLegalHoldRequest{Name: objName, LegalHold: true}))
			return err
		}},
		{"UploadObject", func() error {
			_, err := s.UploadObject(ctx, connect.NewRequest(&pb.UploadObjectRequest{
				Parent: parent, Key: "k", ContentType: "text/plain",
			}))
			return err
		}},
		{"LookupObject", func() error {
			_, err := s.LookupObject(ctx, connect.NewRequest(&pb.LookupObjectRequest{
				Parent: parent, Key: "k",
			}))
			return err
		}},
		{"CompleteObject", func() error {
			_, err := s.CompleteObject(ctx, connect.NewRequest(&pb.CompleteObjectRequest{
				Name: objName, Etag: "e",
			}))
			return err
		}},
		{"RestoreObject", func() error {
			_, err := s.RestoreObject(ctx, connect.NewRequest(&pb.RestoreObjectRequest{
				Name: objName, ResourceVersion: "1",
			}))
			return err
		}},
		{"RestoreObjectVersion", func() error {
			_, err := s.RestoreObjectVersion(ctx, connect.NewRequest(&pb.RestoreObjectVersionRequest{
				Name:            objName + "/versions/44444444-4444-4444-4444-444444444444",
				ResourceVersion: "1",
			}))
			return err
		}},
		{"SetObjectRetention", func() error {
			_, err := s.SetObjectRetention(ctx, connect.NewRequest(&pb.SetObjectRetentionRequest{
				Name: objName, Mode: "GOVERNANCE",
				RetainUntil: timestamppb.New(time.Now().Add(time.Hour)),
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

// A typed nil must not become a live interface.
//
// ProvideVersionHandler and ProvideLockHandler return a nil *object.Handler
// when the feature is off. Assigning one straight into an interface field
// yields a NON-nil interface holding a nil pointer — `s.Locks != nil` is then
// true and the shim calls a method on a nil receiver. Reproduced while adding
// the seam: it panics, which would turn "object lock is disabled, answer
// Unimplemented" into a dead server on every deployment that has it off.
//
// This is the hazard the seam itself introduced, so it is pinned here rather
// than trusted to a comment.
func TestDisabledFeaturesAnswerUnimplementedRatherThanPanicking(t *testing.T) {
	var versions *object.VersionHandler // exactly what the provider returns
	var locks *object.LockHandler

	s := NewObjectServer(nil, versions).WithLocks(locks)
	ctx := ctxTenant(tenantA)
	objName := "tenants/" + tenantA.String() + "/collections/c1/objects/" + objUUID.String()

	t.Run("object lock", func(t *testing.T) {
		_, err := s.GetObjectLock(ctx, connect.NewRequest(&pb.GetObjectLockRequest{Name: objName}))
		if err == nil {
			t.Fatal("a disabled feature answered success")
		}
		if got := connect.CodeOf(err); got != connect.CodeUnimplemented {
			t.Errorf("code = %v, want unimplemented", got)
		}
	})

	t.Run("versioning", func(t *testing.T) {
		_, err := s.GetObjectVersion(ctx, connect.NewRequest(&pb.GetObjectVersionRequest{Name: objName}))
		if err == nil {
			t.Fatal("a disabled feature answered success")
		}
		if got := connect.CodeOf(err); got != connect.CodeUnimplemented {
			t.Errorf("code = %v, want unimplemented", got)
		}
	})
}

// The other half of the shim's job: a name that does not parse must be refused
// here, with InvalidArgument, and must never reach the handler.
//
// The error-propagation table above sends well-formed names on purpose, so the
// parse branches never fire in it — mutating them survived. The property is
// different and so is the test: not "the handler's error survives" but "the
// handler is never called".
func TestMalformedNamesAreRefusedBeforeTheHandler(t *testing.T) {
	called := &countingHandler{}
	s := &ObjectServer{H: called, Versions: failingVersions{}, Locks: failingLocks{}}
	ctx := ctxTenant(tenantA)

	cases := []struct {
		name string
		call func() error
	}{
		{"object name missing the objects segment", func() error {
			_, err := s.GetObject(ctx, connect.NewRequest(&pb.GetObjectRequest{
				Name: "tenants/" + tenantA.String() + "/collections/c1",
			}))
			return err
		}},
		{"object id is not a uuid", func() error {
			_, err := s.GetObject(ctx, connect.NewRequest(&pb.GetObjectRequest{
				Name: "tenants/" + tenantA.String() + "/collections/c1/objects/not-a-uuid",
			}))
			return err
		}},
		{"tenant segment is not a uuid", func() error {
			_, err := s.GetObject(ctx, connect.NewRequest(&pb.GetObjectRequest{
				Name: "tenants/nope/collections/c1/objects/" + objUUID.String(),
			}))
			return err
		}},
		{"parent is not a collection", func() error {
			_, err := s.ListObjects(ctx, connect.NewRequest(&pb.ListObjectsRequest{
				Parent: "tenants/" + tenantA.String(),
			}))
			return err
		}},
		{"version name without a version id", func() error {
			_, err := s.GetObjectVersion(ctx, connect.NewRequest(&pb.GetObjectVersionRequest{
				Name: "tenants/" + tenantA.String() + "/collections/c1/objects/" + objUUID.String(),
			}))
			return err
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.call()
			if err == nil {
				t.Fatal("a malformed name was accepted")
			}
			if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
				t.Errorf("code = %v, want invalid_argument", got)
			}
		})
	}
	if called.n != 0 {
		t.Errorf("the handler ran %d times for names that do not parse — the "+
			"shim is passing them through instead of refusing them", called.n)
	}
}

// countingHandler records whether it was reached. Every method returns a zero
// value and no error, so a shim that called it would answer SUCCESS — which is
// what makes the count meaningful rather than incidental.
type countingHandler struct{ n int }

func (c *countingHandler) UploadObject(context.Context, object.UploadObjectInput) (*object.UploadObjectOutput, error) {
	c.n++
	return &object.UploadObjectOutput{}, nil
}
func (c *countingHandler) CompleteObject(context.Context, object.CompleteObjectInput) (*object.Object, error) {
	c.n++
	return &object.Object{}, nil
}
func (c *countingHandler) ListObjects(context.Context, object.ListObjectsInput) ([]object.Object, string, error) {
	c.n++
	return nil, "", nil
}
func (c *countingHandler) CountObjects(context.Context, object.CountObjectsInput) (*object.CountObjectsOutput, error) {
	c.n++
	return &object.CountObjectsOutput{}, nil
}
func (c *countingHandler) GetObject(context.Context, string, string) (*object.Object, error) {
	c.n++
	return &object.Object{}, nil
}
func (c *countingHandler) LookupObject(context.Context, string, string) (*object.Object, error) {
	c.n++
	return &object.Object{}, nil
}
func (c *countingHandler) DownloadObject(context.Context, string, string, time.Duration, string) (*object.DownloadObjectOutput, error) {
	c.n++
	return &object.DownloadObjectOutput{}, nil
}
func (c *countingHandler) UpdateObject(context.Context, object.UpdateObjectInput) (*object.Object, error) {
	c.n++
	return &object.Object{}, nil
}
func (c *countingHandler) DeleteObject(context.Context, string, string, string, bool, bool) error {
	c.n++
	return nil
}
func (c *countingHandler) RestoreObject(context.Context, string, string, string) (*object.Object, error) {
	c.n++
	return &object.Object{}, nil
}
func (c *countingHandler) CopyObject(context.Context, object.CopyObjectInput) (*object.Object, error) {
	c.n++
	return &object.Object{}, nil
}
