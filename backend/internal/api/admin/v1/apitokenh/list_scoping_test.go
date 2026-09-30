package apitokenh

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	adminv1 "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
)

// List is the only tenant-addressed RPC on this service, and it was the one
// with no tenant guard at all.
//
// api_tokens carries FORCE row-level security (`tenant_id =
// paladin_session_tenant_id()`), so the database filtered a foreign tenant's
// rows out rather than refusing the read. The caller got HTTP 200 and an empty
// page, and "you may not see this" was indistinguishable from "there is
// nothing here" — on a console page that renders for any tenant id, so an
// operator hunting a live credential to revoke was told none existed.
//
// Two halves, and both are needed: refuse the caller who may not cross, and
// tell RLS about the caller who may.

// recordingStore captures the args ListByTenant was called with, and the
// acting tenant on the context at that moment — which is what the RLS pool
// binds to paladin.tenant_id.
type recordingStore struct {
	fakeStore
	sawTenant     uuid.UUID
	sawActing     uuid.UUID
	hadActing     bool
	listCalled    bool
	revokeCalls   int
	revokedActing uuid.UUID
}

func (r *recordingStore) Revoke(ctx context.Context, _ uuid.UUID) error {
	r.revokeCalls++
	r.revokedActing, _ = auth.ActingTenant(ctx)
	return nil
}

func (r *recordingStore) ListByTenant(ctx context.Context, a api_token.ListByTenantArgs) ([]api_token.Token, string, error) {
	r.listCalled = true
	r.sawTenant = a.TenantID
	r.sawActing, r.hadActing = auth.ActingTenant(ctx)
	return nil, "", nil
}

func listReq(tenantID uuid.UUID) *connect.Request[adminv1.APITokenServiceListRequest] {
	return connect.NewRequest(&adminv1.APITokenServiceListRequest{
		Parent: "tenants/" + tenantID.String(),
	})
}

func TestListRefusesAForeignTenant(t *testing.T) {
	store := &recordingStore{}
	h := newHandler(&stubIssuer{}, store, allowAuthorizer{})

	// tenant.admin asking for somebody else's tenant. The answer has to be a
	// refusal the caller can act on, not a page they will read as "empty".
	_, err := h.List(ctxAs("tenant.admin"), listReq(uuid.New()))
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
	if store.listCalled {
		t.Error("the store was queried anyway — the refusal must come before the read")
	}
}

func TestListAllowsACallerTheirOwnTenant(t *testing.T) {
	store := &recordingStore{}
	h := newHandler(&stubIssuer{}, store, allowAuthorizer{})

	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "someone", TenantID: uuid.New(), Roles: []string{"tenant.admin"},
	})
	p, _ := auth.PrincipalFromContext(ctx)

	if _, err := h.List(ctx, listReq(p.TenantID)); err != nil {
		t.Fatalf("a caller was refused their own tenant: %v", err)
	}
	if store.sawTenant != p.TenantID {
		t.Errorf("store scoped to %v, want the caller's %v", store.sawTenant, p.TenantID)
	}
}

// Revoke was the operation the visibility was FOR, and it was unreachable: a
// bare token id cannot be scoped, and the row that would reveal its tenant is
// the one RLS hides. Addressing by tenants/{t}/apiTokens/{id} breaks the
// circle without a privileged read path around the enforcement mechanism.
func TestRevokeCrossesTenantsForPlatformAdmin(t *testing.T) {
	store := &recordingStore{}
	h := newHandler(&stubIssuer{}, store, allowAuthorizer{})
	target, tokenID := uuid.New(), uuid.New()

	if _, err := h.Revoke(ctxAs("platform.admin"), connect.NewRequest(
		&adminv1.APITokenServiceRevokeRequest{
			Name: "tenants/" + target.String() + "/apiTokens/" + tokenID.String(),
		})); err != nil {
		t.Fatalf("platform.admin could not revoke another tenant's token: %v", err)
	}
	if store.revokedActing != target {
		t.Errorf("acting tenant at revoke = %v, want %v — without it RLS hides the row and the revoke silently does nothing",
			store.revokedActing, target)
	}
}

func TestRevokeRefusesAForeignTenant(t *testing.T) {
	store := &recordingStore{}
	h := newHandler(&stubIssuer{}, store, allowAuthorizer{})

	_, err := h.Revoke(ctxAs("tenant.admin"), connect.NewRequest(
		&adminv1.APITokenServiceRevokeRequest{
			Name: "tenants/" + uuid.NewString() + "/apiTokens/" + uuid.NewString(),
		}))
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
	if store.revokeCalls != 0 {
		t.Error("the store was reached anyway — the refusal must come first")
	}
}

func TestRevokeRejectsABareId(t *testing.T) {
	h := newHandler(&stubIssuer{}, &recordingStore{}, allowAuthorizer{})

	// The old shape. It is refused rather than guessed at: an id without its
	// parent has no scope, and inferring one is what this change removes.
	_, err := h.Revoke(ctxAs("platform.admin"), connect.NewRequest(
		&adminv1.APITokenServiceRevokeRequest{Name: uuid.NewString()}))
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code(err))
	}
}

func TestListLetsPlatformAdminCrossAndTellsRLS(t *testing.T) {
	store := &recordingStore{}
	h := newHandler(&stubIssuer{}, store, allowAuthorizer{})
	target := uuid.New()

	if _, err := h.List(ctxAs("platform.admin"), listReq(target)); err != nil {
		t.Fatalf("platform.admin was refused: %v", err)
	}
	if store.sawTenant != target {
		t.Errorf("store scoped to %v, want the requested %v", store.sawTenant, target)
	}
	// The half that a permission check alone does not buy. Without an acting
	// tenant the session stays bound to the admin's own, RLS filters the rows
	// away, and the admitted caller reads the same empty page as the refused
	// one — the bug wearing a different hat.
	if !store.hadActing || store.sawActing != target {
		t.Errorf("acting tenant = %v (set=%v), want %v — RLS would filter the rows the caller was just authorised to see",
			store.sawActing, store.hadActing, target)
	}
}
