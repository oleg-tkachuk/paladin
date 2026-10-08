package objecttagh

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/capability"
)

// fakeRepo is a configurable in-memory Repository. It records the args the
// handler forwards so tests can assert the tenant-stamping and error-mapping
// contract without a database.
type fakeRepo struct {
	createFn func(ctx context.Context, args CreateArgs) (ObjectTag, error)
	getFn    func(ctx context.Context, tenantID uuid.UUID, slug string) (ObjectTag, error)
	updateFn func(ctx context.Context, args UpdateArgs) (ObjectTag, error)
	deleteFn func(ctx context.Context, tenantID uuid.UUID, slug string, expectedVersion int64) error
	listFn   func(ctx context.Context, tenantID uuid.UUID, pageSize int32, afterSlug string) ([]ObjectTag, string, error)

	lastCreate CreateArgs
	lastUpdate UpdateArgs
	lastDelete struct {
		tenantID uuid.UUID
		slug     string
		version  int64
	}
}

func (f *fakeRepo) Create(ctx context.Context, args CreateArgs) (ObjectTag, error) {
	f.lastCreate = args
	return f.createFn(ctx, args)
}

func (f *fakeRepo) Get(ctx context.Context, tenantID uuid.UUID, slug string) (ObjectTag, error) {
	return f.getFn(ctx, tenantID, slug)
}

func (f *fakeRepo) Update(ctx context.Context, args UpdateArgs) (ObjectTag, error) {
	f.lastUpdate = args
	return f.updateFn(ctx, args)
}

func (f *fakeRepo) Delete(ctx context.Context, tenantID uuid.UUID, slug string, expectedVersion int64) error {
	f.lastDelete.tenantID = tenantID
	f.lastDelete.slug = slug
	f.lastDelete.version = expectedVersion
	return f.deleteFn(ctx, tenantID, slug, expectedVersion)
}

func (f *fakeRepo) List(ctx context.Context, tenantID uuid.UUID, pageSize int32, afterSlug string) ([]ObjectTag, string, error) {
	return f.listFn(ctx, tenantID, pageSize, afterSlug)
}

func authedCtx(tid uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tid})
}

// authedCtxWithCap authorises exactly `ops`; a missing op drives the
// PermissionDenied branch of auth.AssertCapabilityOp.
func authedCtxWithCap(tid uuid.UUID, ops ...capability.Op) context.Context {
	return auth.WithCapability(authedCtx(tid), &capability.Capability{
		Caveats: capability.Caveats{Ops: ops},
	})
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

func TestCreateObjectTag(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{})
		_, err := h.CreateObjectTag(context.Background(), CreateArgs{Slug: "s"})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("capability lacks tag op → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{})
		ctx := authedCtxWithCap(tid, capability.OpGet) // no OpTag
		_, err := h.CreateObjectTag(ctx, CreateArgs{Slug: "s"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	// Same shape as collection.CreateCollection: the repository classifies a
	// duplicate slug, and the handler used to discard it by wrapping every
	// failure in a hardcoded CodeInternal. The "repo error → internal" case
	// below is the other half — MapError's fallback still has to hold, or
	// this change would have turned every unclassified failure into something
	// softer than a 500.
	t.Run("duplicate slug → already exists", func(t *testing.T) {
		h := NewHandler(&fakeRepo{createFn: func(context.Context, CreateArgs) (ObjectTag, error) {
			return ObjectTag{}, fmt.Errorf("create object tag: %w", ErrObjectTagExists)
		}})
		_, err := h.CreateObjectTag(authedCtx(tid), CreateArgs{TenantID: tid, Slug: "s"})
		wantCode(t, err, connect.CodeAlreadyExists)
	})

	t.Run("repo error → internal", func(t *testing.T) {
		h := NewHandler(&fakeRepo{createFn: func(context.Context, CreateArgs) (ObjectTag, error) {
			return ObjectTag{}, errors.New("boom")
		}})
		_, err := h.CreateObjectTag(authedCtx(tid), CreateArgs{Slug: "s"})
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("ok stamps tenant from context", func(t *testing.T) {
		fr := &fakeRepo{createFn: func(_ context.Context, a CreateArgs) (ObjectTag, error) {
			return ObjectTag{TenantID: a.TenantID, Slug: a.Slug}, nil
		}}
		got, err := NewHandler(fr).CreateObjectTag(authedCtx(tid), CreateArgs{Slug: "docs", TenantID: uuid.New()})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastCreate.TenantID != tid {
			t.Fatalf("tenant not stamped from ctx: got %v want %v", fr.lastCreate.TenantID, tid)
		}
		if got.Slug != "docs" {
			t.Fatalf("slug: got %q want docs", got.Slug)
		}
	})
}

func TestGetObjectTag(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}).GetObjectTag(context.Background(), "s")
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("repo error → not found", func(t *testing.T) {
		h := NewHandler(&fakeRepo{getFn: func(context.Context, uuid.UUID, string) (ObjectTag, error) {
			return ObjectTag{}, errors.New("missing")
		}})
		_, err := h.GetObjectTag(authedCtx(tid), "s")
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("ok forwards tenant + slug", func(t *testing.T) {
		var gotTenant uuid.UUID
		var gotSlug string
		h := NewHandler(&fakeRepo{getFn: func(_ context.Context, tenant uuid.UUID, slug string) (ObjectTag, error) {
			gotTenant, gotSlug = tenant, slug
			return ObjectTag{TenantID: tenant, Slug: slug}, nil
		}})
		got, err := h.GetObjectTag(authedCtx(tid), "docs")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if gotTenant != tid || gotSlug != "docs" {
			t.Fatalf("forwarded (%v,%q), want (%v,docs)", gotTenant, gotSlug, tid)
		}
		if got.Slug != "docs" {
			t.Fatalf("slug: got %q", got.Slug)
		}
	})
}

func TestUpdateObjectTag(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		_, err := NewHandler(&fakeRepo{}).UpdateObjectTag(context.Background(), UpdateArgs{Slug: "s"})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("version mismatch maps to aborted", func(t *testing.T) {
		h := NewHandler(&fakeRepo{updateFn: func(context.Context, UpdateArgs) (ObjectTag, error) {
			return ObjectTag{}, ErrVersionMismatch
		}})
		_, err := h.UpdateObjectTag(authedCtx(tid), UpdateArgs{Slug: "s", ExpectedVersion: 3})
		wantCode(t, err, connect.CodeAborted)
	})

	t.Run("ok stamps tenant", func(t *testing.T) {
		fr := &fakeRepo{updateFn: func(_ context.Context, a UpdateArgs) (ObjectTag, error) {
			return ObjectTag{TenantID: a.TenantID, Slug: a.Slug}, nil
		}}
		_, err := NewHandler(fr).UpdateObjectTag(authedCtx(tid), UpdateArgs{Slug: "docs"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastUpdate.TenantID != tid {
			t.Fatalf("tenant not stamped: got %v want %v", fr.lastUpdate.TenantID, tid)
		}
	})
}

func TestDeleteObjectTag(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		err := NewHandler(&fakeRepo{}).DeleteObjectTag(context.Background(), "s", 0)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("version mismatch maps to aborted", func(t *testing.T) {
		h := NewHandler(&fakeRepo{deleteFn: func(context.Context, uuid.UUID, string, int64) error {
			return ErrVersionMismatch
		}})
		err := h.DeleteObjectTag(authedCtx(tid), "s", 2)
		wantCode(t, err, connect.CodeAborted)
	})

	t.Run("ok forwards args", func(t *testing.T) {
		fr := &fakeRepo{deleteFn: func(context.Context, uuid.UUID, string, int64) error { return nil }}
		if err := NewHandler(fr).DeleteObjectTag(authedCtx(tid), "docs", 7); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastDelete.tenantID != tid || fr.lastDelete.slug != "docs" || fr.lastDelete.version != 7 {
			t.Fatalf("forwarded %+v", fr.lastDelete)
		}
	})
}

func TestListObjectTags(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		_, _, err := NewHandler(&fakeRepo{}).ListObjectTags(context.Background(), 10, "")
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("ok passes through repo result + next token", func(t *testing.T) {
		h := NewHandler(&fakeRepo{listFn: func(_ context.Context, tenant uuid.UUID, pageSize int32, after string) ([]ObjectTag, string, error) {
			return []ObjectTag{{TenantID: tenant, Slug: "a"}}, "next", nil
		}})
		tags, next, err := h.ListObjectTags(authedCtx(tid), 25, "cursor")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(tags) != 1 || tags[0].Slug != "a" || next != "next" {
			t.Fatalf("got tags=%v next=%q", tags, next)
		}
	})
}
