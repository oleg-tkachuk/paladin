package objecth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"
)

func TestObjectOrder(t *testing.T) {
	for _, f := range ObjectOrderFields {
		got, err := objectOrder(f, true)
		if err != nil || got != (keyset.Order{Field: f, Desc: true}) {
			t.Errorf("objectOrder(%q) = %+v, %v", f, got, err)
		}
	}
	// No order named is id order, either way.
	if got, err := objectOrder("", false); err != nil || got != (keyset.Order{Field: orderByID}) {
		t.Errorf(`objectOrder("") = %+v, %v; want id order`, got, err)
	}
}

// A field ListObjects cannot sort by is refused, naming those it can: it was
// once accepted and ignored. The console's old names are among the refused.
func TestObjectOrder_RefusesAnUnknownField(t *testing.T) {
	for _, f := range []string{"status", "object_tag", "id", "path", "Key"} {
		_, err := objectOrder(f, false)
		var ce *connect.Error
		if !errors.As(err, &ce) || ce.Code() != connect.CodeInvalidArgument {
			t.Errorf("objectOrder(%q): err = %v, want InvalidArgument", f, err)
			continue
		}
		if !strings.Contains(ce.Message(), OrderByKey) {
			t.Errorf("objectOrder(%q): %q names no field it may sort by", f, ce.Message())
		}
	}
}

// orderListRepo answers ListObjects with err and records the order it got.
type orderListRepo struct {
	listRepo
	err   error
	order keyset.Order
	calls int
}

func (r *orderListRepo) ListObjects(_ context.Context, args ListObjectsArgs) ([]Object, string, error) {
	r.calls++
	r.order = args.Order
	return nil, "", r.err
}

func TestListObjects_Order(t *testing.T) {
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: uuid.New()})
	newHandler := func(repo *orderListRepo) *Handler {
		return &Handler{repo: repo, policy: perRowAuthorizer{}, filter: cel.NewEvaluator()}
	}

	t.Run("the store gets the resolved order", func(t *testing.T) {
		repo := &orderListRepo{}
		if _, _, err := newHandler(repo).ListObjects(ctx, ListObjectsInput{Collection: "docs", OrderBy: OrderBySize, SortDesc: true}); err != nil {
			t.Fatal(err)
		}
		if repo.order != (keyset.Order{Field: OrderBySize, Desc: true}) {
			t.Errorf("store got %+v", repo.order)
		}
	})
	t.Run("an unknown field never reaches the store", func(t *testing.T) {
		repo := &orderListRepo{}
		_, _, err := newHandler(repo).ListObjects(ctx, ListObjectsInput{Collection: "docs", OrderBy: "status"})
		if connect.CodeOf(err) != connect.CodeInvalidArgument || repo.calls != 0 {
			t.Errorf("code = %v, store calls = %d; want InvalidArgument and none", connect.CodeOf(err), repo.calls)
		}
	})
	// A token the store cannot resume is the caller's to fix, not Internal.
	for _, cause := range []error{keyset.ErrMalformed, keyset.ErrOrderChanged} {
		t.Run(cause.Error(), func(t *testing.T) {
			repo := &orderListRepo{err: fmt.Errorf("list objects: %w", cause)}
			_, _, err := newHandler(repo).ListObjects(ctx, ListObjectsInput{Collection: "docs", PageToken: "x"})
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Errorf("code = %v, want InvalidArgument", connect.CodeOf(err))
			}
		})
	}
}
