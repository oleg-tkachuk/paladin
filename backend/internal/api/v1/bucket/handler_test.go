package bucket

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin-private/internal/auth"
)

// fakeRepo is a configurable in-memory Repository. It records the args the
// handler forwards so tests can assert the forwarding + error-mapping
// contract without a database. A nil *Fn defaults to a benign success so a
// test only has to wire the branch it exercises.
type fakeRepo struct {
	createFn func(ctx context.Context, args CreateArgs) (Bucket, error)
	getFn    func(ctx context.Context, backendID, bucketName string) (Bucket, error)
	updateFn func(ctx context.Context, args UpdateArgs) (Bucket, error)
	deleteFn func(ctx context.Context, backendID, bucketName string, expectedVersion int64) error
	listFn   func(ctx context.Context, args ListArgs) ([]Bucket, string, error)
	countFn  func(ctx context.Context, backendID, bucketName string) (int64, error)

	lastCreate CreateArgs
	lastUpdate UpdateArgs
	lastList   ListArgs
	lastDelete struct {
		backendID  string
		bucketName string
		version    int64
	}
	lastCount struct {
		backendID  string
		bucketName string
	}
}

func (f *fakeRepo) Create(ctx context.Context, args CreateArgs) (Bucket, error) {
	f.lastCreate = args
	if f.createFn == nil {
		return Bucket{BackendID: args.BackendID, BucketName: args.BucketName}, nil
	}
	return f.createFn(ctx, args)
}

func (f *fakeRepo) Get(ctx context.Context, backendID, bucketName string) (Bucket, error) {
	if f.getFn == nil {
		return Bucket{BackendID: backendID, BucketName: bucketName}, nil
	}
	return f.getFn(ctx, backendID, bucketName)
}

func (f *fakeRepo) Update(ctx context.Context, args UpdateArgs) (Bucket, error) {
	f.lastUpdate = args
	if f.updateFn == nil {
		return Bucket{BackendID: args.BackendID, BucketName: args.BucketName}, nil
	}
	return f.updateFn(ctx, args)
}

func (f *fakeRepo) Delete(ctx context.Context, backendID, bucketName string, expectedVersion int64) error {
	f.lastDelete.backendID = backendID
	f.lastDelete.bucketName = bucketName
	f.lastDelete.version = expectedVersion
	if f.deleteFn == nil {
		return nil
	}
	return f.deleteFn(ctx, backendID, bucketName, expectedVersion)
}

func (f *fakeRepo) List(ctx context.Context, args ListArgs) ([]Bucket, string, error) {
	f.lastList = args
	if f.listFn == nil {
		return nil, "", nil
	}
	return f.listFn(ctx, args)
}

func (f *fakeRepo) CountObjectKeys(ctx context.Context, backendID, bucketName string) (int64, error) {
	f.lastCount.backendID = backendID
	f.lastCount.bucketName = bucketName
	if f.countFn == nil {
		return 0, nil
	}
	return f.countFn(ctx, backendID, bucketName)
}

// fakeProvisioner records the AWS-side calls the handler forwards.
type fakeProvisioner struct {
	createFn func(ctx context.Context, backendID, bucketName, region string) error
	deleteFn func(ctx context.Context, backendID, bucketName string) error

	lastCreate struct{ backendID, bucketName, region string }
	lastDelete struct{ backendID, bucketName string }
	createN    int
	deleteN    int
}

func (p *fakeProvisioner) CreateBucket(ctx context.Context, backendID, bucketName, region string) error {
	p.createN++
	p.lastCreate.backendID = backendID
	p.lastCreate.bucketName = bucketName
	p.lastCreate.region = region
	if p.createFn == nil {
		return nil
	}
	return p.createFn(ctx, backendID, bucketName, region)
}

func (p *fakeProvisioner) DeleteBucket(ctx context.Context, backendID, bucketName string) error {
	p.deleteN++
	p.lastDelete.backendID = backendID
	p.lastDelete.bucketName = bucketName
	if p.deleteFn == nil {
		return nil
	}
	return p.deleteFn(ctx, backendID, bucketName)
}

func authedCtx() context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: uuid.New()})
}

func wantCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %v, got nil", want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("error code: got %v, want %v (err=%v)", got, want, err)
	}
}

func TestCreateBucket(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, &fakeProvisioner{}).
			CreateBucket(context.Background(), CreateArgs{BackendID: "b1", BucketName: "bk"})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("empty backend_id → invalid argument", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, &fakeProvisioner{}).
			CreateBucket(authedCtx(), CreateArgs{BucketName: "bk"})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("provisioner error → internal, repo not called", func(t *testing.T) {
		fr := &fakeRepo{createFn: func(context.Context, CreateArgs) (Bucket, error) {
			t.Fatalf("repo.Create must not run after provisioner failure")
			return Bucket{}, nil
		}}
		fp := &fakeProvisioner{createFn: func(context.Context, string, string, string) error {
			return errors.New("aws down")
		}}
		_, err := NewHandler(fr, fp).CreateBucket(authedCtx(), CreateArgs{BackendID: "b1", BucketName: "bk"})
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("repo error → internal", func(t *testing.T) {
		fr := &fakeRepo{createFn: func(context.Context, CreateArgs) (Bucket, error) {
			return Bucket{}, errors.New("insert failed")
		}}
		_, err := NewHandler(fr, &fakeProvisioner{}).
			CreateBucket(authedCtx(), CreateArgs{BackendID: "b1", BucketName: "bk"})
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("ok forwards args to provisioner then repo", func(t *testing.T) {
		fr := &fakeRepo{}
		fp := &fakeProvisioner{}
		args := CreateArgs{BackendID: "b1", BucketName: "bk", DisplayName: "My Bucket", Region: "us-east-1"}
		got, err := NewHandler(fr, fp).CreateBucket(authedCtx(), args)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fp.createN != 1 {
			t.Fatalf("provisioner.CreateBucket calls: got %d want 1", fp.createN)
		}
		if fp.lastCreate.backendID != "b1" || fp.lastCreate.bucketName != "bk" || fp.lastCreate.region != "us-east-1" {
			t.Fatalf("provisioner got %+v", fp.lastCreate)
		}
		if fr.lastCreate.BackendID != "b1" || fr.lastCreate.BucketName != "bk" ||
			fr.lastCreate.DisplayName != "My Bucket" || fr.lastCreate.Region != "us-east-1" {
			t.Fatalf("repo got %+v want %+v", fr.lastCreate, args)
		}
		if got.BackendID != "b1" || got.BucketName != "bk" {
			t.Fatalf("returned bucket %+v", got)
		}
	})

	t.Run("nil provisioner skips remote provision, still records", func(t *testing.T) {
		fr := &fakeRepo{}
		got, err := NewHandler(fr, nil).
			CreateBucket(authedCtx(), CreateArgs{BackendID: "b1", BucketName: "bk"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastCreate.BackendID != "b1" || got.BucketName != "bk" {
			t.Fatalf("repo=%+v got=%+v", fr.lastCreate, got)
		}
	})
}

func TestGetBucket(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, &fakeProvisioner{}).
			GetBucket(context.Background(), "b1", "bk")
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("repo error → not found", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(context.Context, string, string) (Bucket, error) {
			return Bucket{}, errors.New("no row")
		}}
		_, err := NewHandler(fr, &fakeProvisioner{}).GetBucket(authedCtx(), "b1", "bk")
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("ok forwards backend + name", func(t *testing.T) {
		var gotBackend, gotName string
		fr := &fakeRepo{getFn: func(_ context.Context, backendID, bucketName string) (Bucket, error) {
			gotBackend, gotName = backendID, bucketName
			return Bucket{BackendID: backendID, BucketName: bucketName}, nil
		}}
		got, err := NewHandler(fr, &fakeProvisioner{}).GetBucket(authedCtx(), "b1", "bk")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if gotBackend != "b1" || gotName != "bk" {
			t.Fatalf("forwarded (%q,%q), want (b1,bk)", gotBackend, gotName)
		}
		if got.BucketName != "bk" {
			t.Fatalf("returned %+v", got)
		}
	})
}

func TestUpdateBucket(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}, &fakeProvisioner{}).
			UpdateBucket(context.Background(), UpdateArgs{BackendID: "b1", BucketName: "bk"})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("version mismatch maps to aborted", func(t *testing.T) {
		fr := &fakeRepo{updateFn: func(context.Context, UpdateArgs) (Bucket, error) {
			return Bucket{}, ErrVersionMismatch
		}}
		_, err := NewHandler(fr, &fakeProvisioner{}).
			UpdateBucket(authedCtx(), UpdateArgs{BackendID: "b1", BucketName: "bk", ExpectedVersion: 3})
		wantCode(t, err, connect.CodeAborted)
	})

	t.Run("generic repo error → internal (MapError default)", func(t *testing.T) {
		fr := &fakeRepo{updateFn: func(context.Context, UpdateArgs) (Bucket, error) {
			return Bucket{}, errors.New("boom")
		}}
		_, err := NewHandler(fr, &fakeProvisioner{}).
			UpdateBucket(authedCtx(), UpdateArgs{BackendID: "b1", BucketName: "bk"})
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("ok forwards args", func(t *testing.T) {
		fr := &fakeRepo{}
		args := UpdateArgs{BackendID: "b1", BucketName: "bk", ExpectedVersion: 9}
		got, err := NewHandler(fr, &fakeProvisioner{}).UpdateBucket(authedCtx(), args)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastUpdate.BackendID != "b1" || fr.lastUpdate.ExpectedVersion != 9 {
			t.Fatalf("forwarded %+v", fr.lastUpdate)
		}
		if got.BucketName != "bk" {
			t.Fatalf("returned %+v", got)
		}
	})
}

func TestDeleteBucket(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		err := NewHandler(&fakeRepo{}, &fakeProvisioner{}).
			DeleteBucket(context.Background(), "b1", "bk", 0, false)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("count error → internal", func(t *testing.T) {
		fr := &fakeRepo{countFn: func(context.Context, string, string) (int64, error) {
			return 0, errors.New("count failed")
		}}
		err := NewHandler(fr, &fakeProvisioner{}).DeleteBucket(authedCtx(), "b1", "bk", 0, false)
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("still-referenced → failed precondition", func(t *testing.T) {
		fr := &fakeRepo{
			countFn: func(context.Context, string, string) (int64, error) { return 3, nil },
			deleteFn: func(context.Context, string, string, int64) error {
				t.Fatalf("Delete must not run while ObjectKeys reference the bucket")
				return nil
			},
		}
		err := NewHandler(fr, &fakeProvisioner{}).DeleteBucket(authedCtx(), "b1", "bk", 0, false)
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("delete version mismatch maps to aborted", func(t *testing.T) {
		fr := &fakeRepo{deleteFn: func(context.Context, string, string, int64) error {
			return ErrVersionMismatch
		}}
		err := NewHandler(fr, &fakeProvisioner{}).DeleteBucket(authedCtx(), "b1", "bk", 2, false)
		wantCode(t, err, connect.CodeAborted)
	})

	t.Run("deleteRemote false skips provisioner", func(t *testing.T) {
		fr := &fakeRepo{}
		fp := &fakeProvisioner{}
		if err := NewHandler(fr, fp).DeleteBucket(authedCtx(), "b1", "bk", 7, false); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fp.deleteN != 0 {
			t.Fatalf("provisioner.DeleteBucket must not run when deleteRemote=false (got %d)", fp.deleteN)
		}
		if fr.lastDelete.backendID != "b1" || fr.lastDelete.bucketName != "bk" || fr.lastDelete.version != 7 {
			t.Fatalf("forwarded %+v", fr.lastDelete)
		}
	})

	t.Run("deleteRemote true forwards to provisioner", func(t *testing.T) {
		fp := &fakeProvisioner{}
		if err := NewHandler(&fakeRepo{}, fp).DeleteBucket(authedCtx(), "b1", "bk", 0, true); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fp.deleteN != 1 || fp.lastDelete.backendID != "b1" || fp.lastDelete.bucketName != "bk" {
			t.Fatalf("provisioner got n=%d %+v", fp.deleteN, fp.lastDelete)
		}
	})

	t.Run("deleteRemote true with remote failure → internal", func(t *testing.T) {
		fp := &fakeProvisioner{deleteFn: func(context.Context, string, string) error {
			return errors.New("s3 delete failed")
		}}
		err := NewHandler(&fakeRepo{}, fp).DeleteBucket(authedCtx(), "b1", "bk", 0, true)
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("deleteRemote true with nil provisioner is a no-op", func(t *testing.T) {
		if err := NewHandler(&fakeRepo{}, nil).DeleteBucket(authedCtx(), "b1", "bk", 0, true); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	})
}

func TestListBuckets(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		_, _, err := NewHandler(&fakeRepo{}, &fakeProvisioner{}).
			ListBuckets(context.Background(), ListArgs{PageSize: 10})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("ok passes through result, next token and forwards args", func(t *testing.T) {
		backend := "b1"
		fr := &fakeRepo{listFn: func(_ context.Context, args ListArgs) ([]Bucket, string, error) {
			return []Bucket{{BackendID: "b1", BucketName: "bk"}}, "next", nil
		}}
		args := ListArgs{BackendID: &backend, PageSize: 25, PageToken: "cursor"}
		buckets, next, err := NewHandler(fr, &fakeProvisioner{}).ListBuckets(authedCtx(), args)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(buckets) != 1 || buckets[0].BucketName != "bk" || next != "next" {
			t.Fatalf("got buckets=%v next=%q", buckets, next)
		}
		if fr.lastList.PageSize != 25 || fr.lastList.PageToken != "cursor" || fr.lastList.BackendID == nil || *fr.lastList.BackendID != "b1" {
			t.Fatalf("forwarded %+v", fr.lastList)
		}
	})

	t.Run("repo error propagates unwrapped", func(t *testing.T) {
		sentinel := errors.New("list boom")
		fr := &fakeRepo{listFn: func(context.Context, ListArgs) ([]Bucket, string, error) {
			return nil, "", fmt.Errorf("wrap: %w", sentinel)
		}}
		_, _, err := NewHandler(fr, &fakeProvisioner{}).ListBuckets(authedCtx(), ListArgs{})
		if !errors.Is(err, sentinel) {
			t.Fatalf("ListBuckets should return the repo error unmodified, got %v", err)
		}
	})
}
