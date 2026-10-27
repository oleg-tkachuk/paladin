package objecth

import (
	"context"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
)

// countingListRepo records whether a list or count reached the store.
type countingListRepo struct {
	listRepo
	calls int
}

func (r *countingListRepo) ListObjects(ctx context.Context, args ListObjectsArgs) ([]Object, string, error) {
	r.calls++
	return r.listRepo.ListObjects(ctx, args)
}

func (r *countingListRepo) CountObjects(context.Context, CountObjectsArgs) (int64, bool, error) {
	r.calls++
	return 0, true, nil
}

// A state the object_state enum does not hold used to compile, reach Postgres
// as an enum parameter and come back Internal (SQLSTATE 22P02). It is the
// caller's mistake, so it must be InvalidArgument, named, and never queried.
func TestListObjectsRejectsUnknownState(t *testing.T) {
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: uuid.New()})
	const filter = `state == "available"`

	calls := map[string]func(h *Handler) error{
		"ListObjects": func(h *Handler) error {
			_, _, err := h.ListObjects(ctx, ListObjectsInput{Collection: "docs", PageSize: 10, Filter: filter})
			return err
		},
		"CountObjects": func(h *Handler) error {
			_, err := h.CountObjects(ctx, CountObjectsInput{Collection: "docs", Filter: filter})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			repo := &countingListRepo{}
			h := &Handler{repo: repo, policy: perRowAuthorizer{}, filter: cel.NewEvaluator()}

			err := call(h)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v (%v), want InvalidArgument", connect.CodeOf(err), err)
			}
			if !strings.Contains(err.Error(), "AVAILABLE") {
				t.Errorf("error %q does not name the accepted values", err)
			}
			if repo.calls != 0 {
				t.Errorf("store queried %d times for a filter that cannot match", repo.calls)
			}
		})
	}
}
