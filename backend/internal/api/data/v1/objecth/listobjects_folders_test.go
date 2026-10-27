package objecth

import (
	"context"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
)

// foldersRepo records what a folder listing asked the store.
type foldersRepo struct {
	countingListRepo
	listArgs    ListObjectsArgs
	prefixArgs  ListPrefixesArgs
	prefixCalls int
	folders     []string
	truncated   bool
}

func (r *foldersRepo) ListObjects(ctx context.Context, args ListObjectsArgs) ([]Object, string, error) {
	r.listArgs = args
	return r.countingListRepo.ListObjects(ctx, args)
}

func (r *foldersRepo) ListCommonPrefixes(_ context.Context, args ListPrefixesArgs) ([]string, bool, error) {
	r.prefixCalls++
	r.prefixArgs = args
	return r.folders, r.truncated, nil
}

func foldersHandler(repo Repository) (*Handler, context.Context) {
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: uuid.New()})
	return &Handler{repo: repo, policy: perRowAuthorizer{}, filter: cel.NewEvaluator()}, ctx
}

func TestListObjectsPassesTheFolderLevel(t *testing.T) {
	repo := &foldersRepo{}
	h, ctx := foldersHandler(repo)
	if _, _, err := h.ListObjects(ctx, ListObjectsInput{Collection: "docs", PageSize: 10, Prefix: "a/", Delimiter: "/"}); err != nil {
		t.Fatal(err)
	}
	if repo.listArgs.Prefix != "a/" || repo.listArgs.Delimiter != "/" {
		t.Errorf("repo got prefix %q delimiter %q", repo.listArgs.Prefix, repo.listArgs.Delimiter)
	}
}

func TestListCommonPrefixes(t *testing.T) {
	t.Run("without a delimiter there are no folders to ask for", func(t *testing.T) {
		repo := &foldersRepo{}
		h, ctx := foldersHandler(repo)
		got, truncated, err := h.ListCommonPrefixes(ctx, ListObjectsInput{Collection: "docs", Prefix: "a/"})
		if err != nil || got != nil || truncated || repo.prefixCalls != 0 {
			t.Fatalf("got %v %v %v, store asked %d times", got, truncated, err, repo.prefixCalls)
		}
	})
	t.Run("asks the store with the level and the filter", func(t *testing.T) {
		repo := &foldersRepo{folders: []string{"a/b/"}, truncated: true}
		h, ctx := foldersHandler(repo)
		got, truncated, err := h.ListCommonPrefixes(ctx, ListObjectsInput{
			Collection: "docs", Prefix: "a/", Delimiter: "/", Filter: `state == "DELETED"`,
		})
		if err != nil || len(got) != 1 || !truncated {
			t.Fatalf("got %v %v %v", got, truncated, err)
		}
		want := ListPrefixesArgs{TenantID: repo.prefixArgs.TenantID, Collection: "docs", Prefix: "a/", Delimiter: "/", Filter: `state == "DELETED"`}
		if repo.prefixArgs != want || repo.prefixArgs.TenantID == uuid.Nil() {
			t.Errorf("store asked %+v", repo.prefixArgs)
		}
	})
	t.Run("a filter that does not compile is the caller's mistake", func(t *testing.T) {
		repo := &foldersRepo{}
		h, ctx := foldersHandler(repo)
		_, _, err := h.ListCommonPrefixes(ctx, ListObjectsInput{Collection: "docs", Delimiter: "/", Filter: "key ==="})
		if connect.CodeOf(err) != connect.CodeInvalidArgument || repo.prefixCalls != 0 {
			t.Fatalf("code %v, store asked %d times", connect.CodeOf(err), repo.prefixCalls)
		}
	})
}

// The API declares one character; the handler holds to it whoever calls.
func TestFolderDelimiterIsOneCharacter(t *testing.T) {
	for _, d := range []string{"//", "ab"} {
		repo := &foldersRepo{}
		h, ctx := foldersHandler(repo)
		_, _, err := h.ListObjects(ctx, ListObjectsInput{Collection: "docs", Delimiter: d})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("ListObjects delimiter %q: code %v", d, connect.CodeOf(err))
		}
		_, _, err = h.ListCommonPrefixes(ctx, ListObjectsInput{Collection: "docs", Delimiter: d})
		if connect.CodeOf(err) != connect.CodeInvalidArgument || repo.prefixCalls != 0 {
			t.Errorf("ListCommonPrefixes delimiter %q: code %v", d, connect.CodeOf(err))
		}
	}
	// One character, multibyte or not, is a delimiter.
	h, ctx := foldersHandler(&foldersRepo{})
	if _, _, err := h.ListObjects(ctx, ListObjectsInput{Collection: "docs", Delimiter: "→"}); err != nil {
		t.Errorf("one multibyte character refused: %v", err)
	}
}

// The folders name keys, so they are held to the listing's own gates: a
// caller refused the list is refused its folders, before the store is asked.
func TestListCommonPrefixesIsGated(t *testing.T) {
	repo := &foldersRepo{folders: []string{"secret/"}}
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: uuid.New()})
	h := &Handler{repo: repo, policy: denyAll{}, filter: cel.NewEvaluator()}
	got, _, err := h.ListCommonPrefixes(ctx, ListObjectsInput{Collection: "docs", Delimiter: "/"})
	if connect.CodeOf(err) != connect.CodePermissionDenied || got != nil || repo.prefixCalls != 0 {
		t.Fatalf("got %v, code %v, store asked %d times", got, connect.CodeOf(err), repo.prefixCalls)
	}
}
