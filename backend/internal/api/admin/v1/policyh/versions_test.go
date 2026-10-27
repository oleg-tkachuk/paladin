package policyh

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// fakeVersions answers a fixed history and records what it was asked.
type fakeVersions struct {
	versions []Version
	err      error
	got      LayerRef
	before   int32
	size     int32
	acting   uuid.UUID
}

func (f *fakeVersions) ListPolicyVersions(ctx context.Context, ref LayerRef, before, size int32) ([]Version, error) {
	f.got, f.before, f.size = ref, before, size
	if id, err := auth.EffectiveTenant(ctx); err == nil {
		f.acting = id
	}
	if f.err != nil {
		return nil, f.err
	}
	out := []Version{}
	for _, v := range f.versions {
		if (before == 0 || v.Version < before) && int32(len(out)) < size {
			out = append(out, v)
		}
	}
	return out, nil
}

// history is versions newest first, n down to 1.
func history(n int32) []Version {
	out := make([]Version, 0, n)
	for v := n; v >= 1; v-- {
		out = append(out, Version{Version: v, CedarPolicy: "p", WrittenAt: time.Unix(int64(v), 0)})
	}
	return out
}

func TestParseLayerName(t *testing.T) {
	tenant, backend := uuid.New(), uuid.New()
	cases := []struct {
		name string
		want LayerRef
	}{
		{"tenants/" + tenant.String(), LayerRef{Layer: LayerTenant, TenantID: tenant}},
		{"tenants/" + tenant.String() + "/collections/e2e/logs", LayerRef{Layer: LayerCollection, TenantID: tenant, Collection: "e2e/logs"}},
		{"storageBackends/" + backend.String(), LayerRef{Layer: LayerStorageBackend, BackendID: backend}},
		{"storageBackends/" + backend.String() + "/buckets/b1", LayerRef{Layer: LayerBucket, BackendID: backend, Bucket: "b1"}},
	}
	for _, c := range cases {
		got, err := parseLayerName(c.name)
		if err != nil || got != c.want {
			t.Errorf("parseLayerName(%q) = %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "tenants/acme", "storageBackends/x", "storageBackends/" + backend.String() + "/other/y", "buckets/b1", "tenants/" + tenant.String() + "/objects/x"} {
		if _, err := parseLayerName(bad); err == nil {
			t.Errorf("parseLayerName(%q) accepted a name that addresses no layer", bad)
		}
	}
}

func TestParseVersionToken(t *testing.T) {
	for token, want := range map[string]int32{"": 0, "1": 1, "42": 42} {
		if got, err := parseVersionToken(token); err != nil || got != want {
			t.Errorf("parseVersionToken(%q) = %d, %v; want %d", token, got, err, want)
		}
	}
	for _, bad := range []string{"0", "-1", "x", "99999999999"} {
		if _, err := parseVersionToken(bad); err == nil {
			t.Errorf("parseVersionToken(%q) accepted a token that is not a version", bad)
		}
	}
}

func TestClampVersionPageSize(t *testing.T) {
	for in, want := range map[int32]int32{0: DefaultVersionPageSize, -1: DefaultVersionPageSize, 3: 3, MaxVersionPageSize + 1: MaxVersionPageSize} {
		if got := clampVersionPageSize(in); got != want {
			t.Errorf("clampVersionPageSize(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestListPolicyVersions(t *testing.T) {
	caller, target := uuid.New(), uuid.New()
	collection := "tenants/" + target.String() + "/collections/docs"
	const pageSize = 2

	t.Run("pages newest first, as the tenant asked about", func(t *testing.T) {
		store := &fakeVersions{versions: history(3)}
		engine := allowEngine()
		h := NewHandler(engine, &fakeStore{})
		h.SetVersionStore(store)
		page, err := h.ListPolicyVersions(authedCtx(caller), collection, pageSize, "")
		if err != nil {
			t.Fatalf("ListPolicyVersions: %v", err)
		}
		if len(page.Versions) != pageSize || page.Versions[0].Version != 3 || page.NextPageToken != "2" {
			t.Fatalf("page = %+v, want versions 3, 2 and a token below 2", page)
		}
		if store.acting != target {
			t.Errorf("read acting as %s, want the tenant asked about, %s", store.acting, target)
		}
		if store.size != pageSize+1 {
			t.Errorf("asked the store for %d, want one more than the page", store.size)
		}
		call := engine.calls[0]
		if call.action != cedar.ActionInspectPolicy || call.resource.TenantID != target || call.resource.Collection != "docs" {
			t.Errorf("authorized %+v on %+v, want InspectPolicy on the collection", call.action, call.resource)
		}

		last, err := h.ListPolicyVersions(authedCtx(caller), collection, pageSize, page.NextPageToken)
		if err != nil || len(last.Versions) != 1 || last.Versions[0].Version != 1 || last.NextPageToken != "" {
			t.Errorf("last page = %+v, %v; want version 1 and no token", last, err)
		}
	})

	t.Run("a platform layer is checked against the caller's tenant", func(t *testing.T) {
		engine := allowEngine()
		h := NewHandler(engine, &fakeStore{})
		h.SetVersionStore(&fakeVersions{})
		if _, err := h.ListPolicyVersions(authedCtx(caller), "storageBackends/"+uuid.NewString(), 0, ""); err != nil {
			t.Fatalf("ListPolicyVersions: %v", err)
		}
		if got := engine.calls[0].resource.TenantID; got != caller {
			t.Errorf("checked against %s, want the caller's tenant %s", got, caller)
		}
	})

	t.Run("errors", func(t *testing.T) {
		for _, c := range []struct {
			name   string
			engine cedar.Authorizer
			store  VersionStore
			ctx    context.Context
			res    string
			token  string
			want   connect.Code
		}{
			{"no history wired", allowEngine(), nil, authedCtx(caller), collection, "", connect.CodeUnimplemented},
			{"a name that is no layer", allowEngine(), &fakeVersions{}, authedCtx(caller), "objects/x", "", connect.CodeInvalidArgument},
			{"a bad token", allowEngine(), &fakeVersions{}, authedCtx(caller), collection, "x", connect.CodeInvalidArgument},
			{"no principal", allowEngine(), &fakeVersions{}, context.Background(), collection, "", connect.CodeUnauthenticated},
			{"denied", denyEngine(), &fakeVersions{}, authedCtx(caller), collection, "", connect.CodePermissionDenied},
			{"no such layer", allowEngine(), &fakeVersions{err: ErrLayerNotFound}, authedCtx(caller), collection, "", connect.CodeNotFound},
			{"store fault", allowEngine(), &fakeVersions{err: errors.New("boom")}, authedCtx(caller), collection, "", connect.CodeInternal},
		} {
			t.Run(c.name, func(t *testing.T) {
				h := NewHandler(c.engine, &fakeStore{})
				if c.store != nil {
					h.SetVersionStore(c.store)
				}
				_, err := h.ListPolicyVersions(c.ctx, c.res, 0, c.token)
				wantCode(t, err, c.want)
			})
		}
	})
}
