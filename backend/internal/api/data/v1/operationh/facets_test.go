package operationh

import (
	"context"
	"errors"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// copyType is an operation type the count tests narrow by.
const copyType = "BatchCopy"

// The counts are of the listing the same input lists: the same tenant, the
// same initiator, the same filter and facets — and refused where the
// listing is.
func TestCountOperationFacets(t *testing.T) {
	tid, other := uuid.New(), uuid.New()
	want := []FacetCount{{Facet: FacetState, Value: string(StateFailed), Count: 2}}
	facets := Facets{States: []State{StateFailed}, Types: []string{copyType}}

	t.Run("forwards the listing's scope", func(t *testing.T) {
		fr := &fakeRepo{countFn: func(context.Context, ListArgs) ([]FacetCount, error) { return want, nil }}
		in := ListInput{Mine: true, Filter: `type == "` + copyType + `"`, Facets: facets}
		got, err := NewHandler(fr, allowAuthorizer()).CountOperationFacets(authedCtx(tid), in)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("counts %v, want the store's %v", got, want)
		}
		args := fr.lastCount
		if args.TenantID != tid || args.Initiator != "u1" || !args.HideDismissed || args.Filter != in.Filter ||
			!slices.Equal(args.Facets.States, facets.States) || !slices.Equal(args.Facets.Types, facets.Types) {
			t.Errorf("store asked for %+v", args)
		}
	})

	t.Run("a platform admin counts a named tenant", func(t *testing.T) {
		fr := &fakeRepo{}
		admin := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "admin", TenantID: tid, Roles: []string{apiutil.RolePlatformAdmin}})
		if _, err := NewHandler(fr, allowAuthorizer()).CountOperationFacets(admin, ListInput{TenantID: other}); err != nil {
			t.Fatal(err)
		}
		if fr.lastCount.TenantID != other {
			t.Errorf("counted tenant %v, want %v", fr.lastCount.TenantID, other)
		}
	})

	for name, tc := range map[string]struct {
		ctx   context.Context
		in    ListInput
		authz *recordingAuthorizer
		code  connect.Code
	}{
		"unauthenticated":          {context.Background(), ListInput{}, allowAuthorizer(), connect.CodeUnauthenticated},
		"policy denies":            {authedCtx(tid), ListInput{}, denyAuthorizer(), connect.CodePermissionDenied},
		"a member, another tenant": {authedCtx(tid), ListInput{TenantID: other}, allowAuthorizer(), connect.CodePermissionDenied},
		"mine, with no subject":    {auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: tid}), ListInput{Mine: true}, allowAuthorizer(), connect.CodeUnauthenticated},
	} {
		t.Run(name, func(t *testing.T) {
			fr := &fakeRepo{}
			_, err := NewHandler(fr, tc.authz).CountOperationFacets(tc.ctx, tc.in)
			wantCode(t, err, tc.code)
			if fr.lastCount != nil {
				t.Error("a refused count reached the store")
			}
		})
	}

	t.Run("a store error is returned", func(t *testing.T) {
		boom := errors.New("boom")
		fr := &fakeRepo{countFn: func(context.Context, ListArgs) ([]FacetCount, error) { return nil, boom }}
		if _, err := NewHandler(fr, allowAuthorizer()).CountOperationFacets(authedCtx(tid), ListInput{}); !errors.Is(err, boom) {
			t.Errorf("err = %v, want the store's", err)
		}
	})
}
