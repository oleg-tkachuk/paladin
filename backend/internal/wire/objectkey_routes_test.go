package wire

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/object_key"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
)

type fakeOKLister struct {
	pages   [][]objectkey.ObjectKey
	tokens  []string
	call    int
	gotArgs []objectkey.ListObjectKeysArgs
	err     error
}

func (f *fakeOKLister) ListObjectKeys(_ context.Context, args objectkey.ListObjectKeysArgs) ([]objectkey.ObjectKey, string, error) {
	f.gotArgs = append(f.gotArgs, args)
	if f.err != nil {
		return nil, "", f.err
	}
	i := f.call
	f.call++
	if i >= len(f.pages) {
		return nil, "", nil
	}
	return f.pages[i], f.tokens[i], nil
}

type fakeBindingReader struct {
	db  tenant.DefaultBinding
	err error
}

func (f fakeBindingReader) GetDefaultBinding(context.Context, uuid.UUID) (tenant.DefaultBinding, error) {
	return f.db, f.err
}

func ok(tid uuid.UUID, backend, bucket, key string) objectkey.ObjectKey {
	return objectkey.ObjectKey{TenantID: tid, BackendID: backend, BucketName: bucket, ObjectKey: key}
}

func TestListObjectKeyRoutes_ShapesAndBareAlias(t *testing.T) {
	tid := uuid.New()
	lister := objectKeyRouteLister{
		okH: &fakeOKLister{
			pages:  [][]objectkey.ObjectKey{{ok(tid, "primary", "paladin", "invoices"), ok(tid, "primary", "other", "logs"), ok(tid, "s3", "paladin", "x")}},
			tokens: []string{""},
		},
		// Default route is primary/paladin — only that OK gets a bare alias.
		tenants: fakeBindingReader{db: tenant.DefaultBinding{BackendID: "primary", BucketName: "paladin"}},
	}

	routes, err := lister.ListObjectKeyRoutes(context.Background(), tid)
	if err != nil {
		t.Fatalf("ListObjectKeyRoutes: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("routes = %d, want 3", len(routes))
	}

	r0 := routes[0]
	wantCanon := "storageBackends/primary/buckets/paladin/tenants/" + tid.String() + "/objectKeys/invoices"
	if r0.Canonical != wantCanon {
		t.Errorf("canonical = %q, want %q", r0.Canonical, wantCanon)
	}
	if r0.TenantPath != "tenants/"+tid.String()+"/objectKeys/invoices" {
		t.Errorf("tenant_path = %q", r0.TenantPath)
	}
	if r0.BareAlias != "invoices" {
		t.Errorf("bare_alias = %q, want the default-route OK to be bare-addressable", r0.BareAlias)
	}
	// A different bucket / backend than the default binding → no bare alias.
	if routes[1].BareAlias != "" {
		t.Errorf("bucket mismatch bare = %q, want empty", routes[1].BareAlias)
	}
	if routes[2].BareAlias != "" {
		t.Errorf("backend mismatch bare = %q, want empty", routes[2].BareAlias)
	}
}

func TestListObjectKeyRoutes_NoBindingNoBareAliases(t *testing.T) {
	tid := uuid.New()
	lister := objectKeyRouteLister{
		okH: &fakeOKLister{
			pages:  [][]objectkey.ObjectKey{{ok(tid, "primary", "paladin", "invoices")}},
			tokens: []string{""},
		},
		tenants: fakeBindingReader{err: tenant.ErrNotFound},
	}

	routes, err := lister.ListObjectKeyRoutes(context.Background(), tid)
	if err != nil {
		t.Fatalf("no binding must not error: %v", err)
	}
	if len(routes) != 1 || routes[0].BareAlias != "" {
		t.Fatalf("routes = %+v, want 1 with empty bare alias", routes)
	}
}

func TestListObjectKeyRoutes_PaginatesAcrossPages(t *testing.T) {
	tid := uuid.New()
	f := &fakeOKLister{
		pages:  [][]objectkey.ObjectKey{{ok(tid, "primary", "paladin", "a")}, {ok(tid, "primary", "paladin", "b")}},
		tokens: []string{"next-1", ""},
	}
	lister := objectKeyRouteLister{okH: f, tenants: fakeBindingReader{err: tenant.ErrNotFound}}

	routes, err := lister.ListObjectKeyRoutes(context.Background(), tid)
	if err != nil {
		t.Fatalf("ListObjectKeyRoutes: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2 across pages", len(routes))
	}
	// Second page must be requested with the token the first page returned.
	if len(f.gotArgs) != 2 || f.gotArgs[1].PageToken != "next-1" {
		t.Fatalf("page tokens = %+v, want second call to carry next-1", f.gotArgs)
	}
}

func TestListObjectKeyRoutes_BindingReadErrorPropagates(t *testing.T) {
	// A real infra error reading the binding (not ErrNotFound) surfaces, so
	// WhoAmI can degrade it to an empty table rather than emit a wrong one.
	lister := objectKeyRouteLister{
		okH:     &fakeOKLister{},
		tenants: fakeBindingReader{err: errors.New("db down")},
	}
	if _, err := lister.ListObjectKeyRoutes(context.Background(), uuid.New()); err == nil {
		t.Fatal("want error when the binding read fails hard")
	}
}
