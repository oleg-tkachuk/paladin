package wire

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/collection"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
)

type fakeOKLister struct {
	pages   [][]objectkey.Collection
	tokens  []string
	call    int
	gotArgs []objectkey.ListCollectionsArgs
	err     error
}

func (f *fakeOKLister) ListCollections(_ context.Context, args objectkey.ListCollectionsArgs) ([]objectkey.Collection, string, error) {
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

func ok(tid uuid.UUID, backend, bucket, key string) objectkey.Collection {
	return objectkey.Collection{TenantID: tid, BackendID: backend, BucketId: bucket, Collection: key}
}

func TestListCollectionRoutes_ShapesAndBareAlias(t *testing.T) {
	tid := uuid.New()
	lister := collectionRouteLister{
		okH: &fakeOKLister{
			pages:  [][]objectkey.Collection{{ok(tid, "primary", "paladin", "invoices"), ok(tid, "primary", "other", "logs"), ok(tid, "s3", "paladin", "x")}},
			tokens: []string{""},
		},
		// Default route is primary/paladin — only that OK gets a bare alias.
		tenants: fakeBindingReader{db: tenant.DefaultBinding{BackendID: "primary", BucketId: "paladin"}},
	}

	routes, _, err := lister.ListCollectionRoutes(context.Background(), tid, "")
	if err != nil {
		t.Fatalf("ListCollectionRoutes: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("routes = %d, want 3", len(routes))
	}

	r0 := routes[0]
	wantCanon := "storageBackends/primary/buckets/paladin/tenants/" + tid.String() + "/collections/invoices"
	if r0.Canonical != wantCanon {
		t.Errorf("canonical = %q, want %q", r0.Canonical, wantCanon)
	}
	if r0.TenantPath != "tenants/"+tid.String()+"/collections/invoices" {
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

func TestListCollectionRoutes_NoBindingNoBareAliases(t *testing.T) {
	tid := uuid.New()
	lister := collectionRouteLister{
		okH: &fakeOKLister{
			pages:  [][]objectkey.Collection{{ok(tid, "primary", "paladin", "invoices")}},
			tokens: []string{""},
		},
		tenants: fakeBindingReader{err: tenant.ErrNotFound},
	}

	routes, _, err := lister.ListCollectionRoutes(context.Background(), tid, "")
	if err != nil {
		t.Fatalf("no binding must not error: %v", err)
	}
	if len(routes) != 1 || routes[0].BareAlias != "" {
		t.Fatalf("routes = %+v, want 1 with empty bare alias", routes)
	}
}

func TestListCollectionRoutes_PaginatesAcrossPages(t *testing.T) {
	tid := uuid.New()
	f := &fakeOKLister{
		pages:  [][]objectkey.Collection{{ok(tid, "primary", "paladin", "a")}, {ok(tid, "primary", "paladin", "b")}},
		tokens: []string{"next-1", ""},
	}
	lister := collectionRouteLister{okH: f, tenants: fakeBindingReader{err: tenant.ErrNotFound}}

	routes, nextToken, err := lister.ListCollectionRoutes(context.Background(), tid, "")
	if err != nil {
		t.Fatalf("ListCollectionRoutes: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2 across pages", len(routes))
	}
	if nextToken != "" {
		t.Errorf("next_page_token = %q, want empty — the full table fit under the cap", nextToken)
	}
	// Second page must be requested with the token the first page returned.
	if len(f.gotArgs) != 2 || f.gotArgs[1].PageToken != "next-1" {
		t.Fatalf("page tokens = %+v, want second call to carry next-1", f.gotArgs)
	}
}

// TestListCollectionRoutes_ResumesFromPageToken: a caller-supplied page token is
// forwarded verbatim to the first ListCollections round-trip, so a client pages
// through the whole table (ADR-0010 Phase 4 DoD option a).
func TestListCollectionRoutes_ResumesFromPageToken(t *testing.T) {
	tid := uuid.New()
	f := &fakeOKLister{
		pages:  [][]objectkey.Collection{{ok(tid, "primary", "paladin", "b")}},
		tokens: []string{""},
	}
	lister := collectionRouteLister{okH: f, tenants: fakeBindingReader{err: tenant.ErrNotFound}}

	if _, _, err := lister.ListCollectionRoutes(context.Background(), tid, "resume-here"); err != nil {
		t.Fatalf("ListCollectionRoutes: %v", err)
	}
	if len(f.gotArgs) != 1 || f.gotArgs[0].PageToken != "resume-here" {
		t.Fatalf("page tokens = %+v, want first call to carry resume-here", f.gotArgs)
	}
}

// pagedKeys splits nKeys Collections into whoAmIRoutePageSize-sized pages plus
// the matching page tokens, mirroring how the real ListCollections handler pages.
// lastToken is the token attached to the final page ("" ⇒ exhausted).
func pagedKeys(tid uuid.UUID, nKeys int, lastToken string) ([][]objectkey.Collection, []string) {
	var pages [][]objectkey.Collection
	var tokens []string
	for start := 0; start < nKeys; start += whoAmIRoutePageSize {
		end := start + whoAmIRoutePageSize
		if end > nKeys {
			end = nKeys
		}
		page := make([]objectkey.Collection, 0, end-start)
		for i := start; i < end; i++ {
			page = append(page, ok(tid, "primary", "paladin", fmt.Sprintf("k%d", i)))
		}
		pages = append(pages, page)
		if end >= nKeys {
			tokens = append(tokens, lastToken)
		} else {
			tokens = append(tokens, fmt.Sprintf("t%d", end))
		}
	}
	return pages, tokens
}

// TestListCollectionRoutes_TruncatesAtCap: more readable Collections than the
// per-call cap → routes is exactly the cap and a next_page_token is returned.
// The cap lands on a page boundary (whoAmIMaxRoutes is a multiple of the page
// size), so the boundary cursor resumes exactly after the last route.
func TestListCollectionRoutes_TruncatesAtCap(t *testing.T) {
	tid := uuid.New()
	// One full page beyond the cap still pending → the cap page carries a token.
	pages, tokens := pagedKeys(tid, whoAmIMaxRoutes+whoAmIRoutePageSize, "")
	lister := collectionRouteLister{
		okH:     &fakeOKLister{pages: pages, tokens: tokens},
		tenants: fakeBindingReader{err: tenant.ErrNotFound},
	}

	routes, nextToken, err := lister.ListCollectionRoutes(context.Background(), tid, "")
	if err != nil {
		t.Fatalf("ListCollectionRoutes: %v", err)
	}
	if len(routes) != whoAmIMaxRoutes {
		t.Errorf("routes = %d, want exactly the cap %d", len(routes), whoAmIMaxRoutes)
	}
	if nextToken == "" {
		t.Error("next_page_token should be set when more Collections exist than the cap")
	}
}

// A table that fills EXACTLY to the cap with nothing left returns no token.
func TestListCollectionRoutes_ExactCapNotTruncated(t *testing.T) {
	tid := uuid.New()
	pages, tokens := pagedKeys(tid, whoAmIMaxRoutes, "")
	lister := collectionRouteLister{
		okH:     &fakeOKLister{pages: pages, tokens: tokens},
		tenants: fakeBindingReader{err: tenant.ErrNotFound},
	}

	routes, nextToken, err := lister.ListCollectionRoutes(context.Background(), tid, "")
	if err != nil {
		t.Fatalf("ListCollectionRoutes: %v", err)
	}
	if len(routes) != whoAmIMaxRoutes || nextToken != "" {
		t.Errorf("routes=%d next=%q, want %d/empty (exact fit)", len(routes), nextToken, whoAmIMaxRoutes)
	}
}

// TestListCollectionRoutes_PagesThroughEntireTable: a client that keeps feeding
// next_page_token back eventually sees every Collection with no duplicates and no
// gaps (ADR-0010 Phase 4 DoD option a — full pagination, not a hard cap).
func TestListCollectionRoutes_PagesThroughEntireTable(t *testing.T) {
	tid := uuid.New()
	const total = whoAmIMaxRoutes*2 + whoAmIRoutePageSize // spans 3 WhoAmI calls
	pages, tokens := pagedKeys(tid, total, "")
	lister := collectionRouteLister{
		okH:     &fakeOKLister{pages: pages, tokens: tokens},
		tenants: fakeBindingReader{err: tenant.ErrNotFound},
	}

	seen := make(map[string]bool, total)
	cursor := ""
	calls := 0
	for {
		routes, next, err := lister.ListCollectionRoutes(context.Background(), tid, cursor)
		if err != nil {
			t.Fatalf("ListCollectionRoutes: %v", err)
		}
		calls++
		for _, r := range routes {
			if seen[r.Canonical] {
				t.Fatalf("duplicate route across pages: %s", r.Canonical)
			}
			seen[r.Canonical] = true
		}
		if next == "" {
			break
		}
		cursor = next
		if calls > total { // runaway guard
			t.Fatal("pagination did not converge")
		}
	}
	if len(seen) != total {
		t.Fatalf("saw %d distinct routes across %d calls, want all %d", len(seen), calls, total)
	}
}

func TestListCollectionRoutes_BindingReadErrorPropagates(t *testing.T) {
	// A real infra error reading the binding (not ErrNotFound) surfaces, so
	// WhoAmI can degrade it to an empty table rather than emit a wrong one.
	lister := collectionRouteLister{
		okH:     &fakeOKLister{},
		tenants: fakeBindingReader{err: errors.New("db down")},
	}
	if _, _, err := lister.ListCollectionRoutes(context.Background(), uuid.New(), ""); err == nil {
		t.Fatal("want error when the binding read fails hard")
	}
}
