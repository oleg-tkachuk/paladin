package collectionh

import (
	"context"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
)

// namesOutsideTheGrammar each fit the request's length rule and fail the
// collections_name_format CHECK.
var namesOutsideTheGrammar = []string{"Docs", "a//b", "-a", "a-", "a/", "a b", "a_b", "a//"}

// A name the column CHECK refuses is refused before a row is written, as the
// caller's mistake: the insert failed the CHECK and the caller read Internal.
func TestCreateCollectionRefusesANameOutsideTheGrammar(t *testing.T) {
	for _, name := range namesOutsideTheGrammar {
		repo := &fakeRepo{}
		_, err := NewHandler(repo, allowAll()).CreateCollection(authedCtx(uuid.New()),
			CreateCollectionArgs{Collection: name, BackendID: "b", BucketName: "n"})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%q: code = %v (%v), want InvalidArgument", name, connect.CodeOf(err), err)
		}
		if repo.lastCreate.Collection != "" {
			t.Errorf("%q: a row was written for a refused name", name)
		}
	}
}

func TestEnsureCollectionRefusesANameOutsideTheGrammar(t *testing.T) {
	for _, name := range namesOutsideTheGrammar {
		_, err := NewHandler(&fakeRepo{}, allowAll()).EnsureCollection(authedCtx(uuid.New()),
			CreateCollectionArgs{Collection: name, BackendID: "b", BucketName: "n"})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%q: code = %v (%v), want InvalidArgument", name, connect.CodeOf(err), err)
		}
	}
}

// creatingRepo stores whatever it is asked to, as the database does for a
// name in the grammar.
func creatingRepo() *fakeRepo {
	return &fakeRepo{createTxFn: func(_ context.Context, a CreateCollectionArgs) (Collection, error) {
		return fullKey(a.TenantID, a.Collection), nil
	}}
}

func TestCreateCollectionAdmitsANameInTheGrammar(t *testing.T) {
	for _, name := range []string{"docs", "e2e/logs", "a", strings.Repeat("a", 63)} {
		if _, err := NewHandler(creatingRepo(), allowAll()).CreateCollection(authedCtx(uuid.New()),
			CreateCollectionArgs{Collection: name, BackendID: "b", BucketName: "n"}); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
}
