// Package apikeyh implements the IAM ApiKeyService — long-lived service
// account credentials used by the MCP server, CI runners, and any backend
// without an interactive login.
package apikeyh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// TenantSlugLookup mirrors authh.TenantSlugLookup — both packages call
// MintAccess and both want to populate the tenant_slug claim. Defined
// here to avoid cross-package coupling for one type alias.
type TenantSlugLookup func(ctx context.Context, tenantID uuid.UUID) (string, error)

// tokenMinter is the narrow slice of *issuer.Issuer this handler needs —
// minting a short-lived access token from claims (MintScopedToken). Declared
// as an interface so the handler is unit-testable with a stub; the concrete
// *issuer.Issuer satisfies it implicitly, so call sites are unchanged.
type tokenMinter interface {
	MintAccess(c issuer.AccessClaims) (string, time.Time, error)
}

type Handler struct {
	apiKeys    authstore.ApiKeyRepository
	issuer     tokenMinter
	policy     cedar.Authorizer
	tenantSlug TenantSlugLookup
	now        func() time.Time
}

func NewHandler(keys authstore.ApiKeyRepository, iss tokenMinter, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("apikeyh: policy authorizer is required")
	}
	return &Handler{apiKeys: keys, issuer: iss, policy: policy, now: time.Now}
}

// WithTenantSlugLookup installs the resolver used for the access-token
// `tenant_slug` claim. nil-safe — the field stays unset and minted tokens
// omit the claim, matching pre-Phase-2 behaviour.
func (h *Handler) WithTenantSlugLookup(f TenantSlugLookup) *Handler {
	h.tenantSlug = f
	return h
}

// authorize gates an api-key RPC against Cedar; the existing
// isPlatformAdmin / tenant-isolation checks at call sites stay as
// defense-in-depth.
func (h *Handler) authorize(ctx context.Context, action string, target authstore.ApiKey) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: p.TenantID, TenantSlug: p.TenantSlug, Roles: p.Roles, Scopes: apiutil.ScopeStrings(p.Scopes)},
		action,
		&cedar.Resource{
			TenantID:       target.TenantID,
			TargetApiKeyID: target.ApiKeyID,
		},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("denied by policy"))
	}
	return nil
}

// ─── Create ─────────────────────────────────────────────────────────────────

type CreateApiKeyInput struct {
	TenantID    uuid.UUID
	Description string
	Roles       []string
	Scopes      []auth.Scope
	TTL         time.Duration // 0 = no expiry
}

type CreateApiKeyOutput struct {
	ApiKey authstore.ApiKey
	Secret string
}

func (h *Handler) CreateApiKey(ctx context.Context, in CreateApiKeyInput) (*CreateApiKeyOutput, error) {
	if err := h.authorize(ctx, cedar.ActionManageApiKey,
		authstore.ApiKey{TenantID: in.TenantID}); err != nil {
		return nil, err
	}
	if in.Description == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("description required"))
	}
	secret, prefix, err := auth.GenerateApiKeySecret()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	hash, err := auth.HashApiKeySecret(secret)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	var expires *time.Time
	if in.TTL > 0 {
		t := h.now().Add(in.TTL)
		expires = &t
	}
	key, err := h.apiKeys.Create(ctx, authstore.ApiKey{
		TenantID:      in.TenantID,
		DisplayPrefix: prefix,
		Description:   in.Description,
		SecretHash:    hash,
		Roles:         in.Roles,
		Scopes:        in.Scopes,
		ExpiresAt:     expires,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &CreateApiKeyOutput{ApiKey: key, Secret: secret}, nil
}

// ─── Read ───────────────────────────────────────────────────────────────────

func (h *Handler) GetApiKey(ctx context.Context, id uuid.UUID) (*authstore.ApiKey, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	k, err := h.apiKeys.GetByID(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if !isPlatformAdmin(ctx) && k.TenantID != caller {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("api_key not found"))
	}
	if err := h.authorize(ctx, cedar.ActionReadApiKey, k); err != nil {
		return nil, err
	}
	return &k, nil
}

func (h *Handler) ListApiKeys(ctx context.Context, args authstore.ListApiKeysArgs) ([]authstore.ApiKey, string, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	if !isPlatformAdmin(ctx) {
		args.TenantID = caller
	}
	if err := h.authorize(ctx, cedar.ActionReadApiKey,
		authstore.ApiKey{TenantID: args.TenantID}); err != nil {
		return nil, "", err
	}
	return h.apiKeys.List(ctx, args)
}

func (h *Handler) RevokeApiKey(ctx context.Context, id uuid.UUID) error {
	current, err := h.GetApiKey(ctx, id)
	if err != nil {
		return err
	}
	if err := h.authorize(ctx, cedar.ActionManageApiKey, *current); err != nil {
		return err
	}
	return h.apiKeys.Revoke(ctx, id)
}

// ─── Rotate ─────────────────────────────────────────────────────────────────

func (h *Handler) RotateApiKey(ctx context.Context, id uuid.UUID, grace time.Duration) (*authstore.ApiKey, string, error) {
	current, err := h.GetApiKey(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if err := h.authorize(ctx, cedar.ActionRotateApiKey, *current); err != nil {
		return nil, "", err
	}
	newSecret, _, err := auth.GenerateApiKeySecret()
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err)
	}
	newHash, err := auth.HashApiKeySecret(newSecret)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err)
	}
	if err := h.apiKeys.UpdateSecretHash(ctx, id, newHash, h.now().Add(grace)); err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err)
	}
	updated, err := h.apiKeys.GetByID(ctx, id)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err)
	}
	_ = current
	return &updated, newSecret, nil
}

// ─── MintScopedToken ────────────────────────────────────────────────────────

type MintScopedTokenInput struct {
	ApiKeyID uuid.UUID
	Scopes   []auth.Scope
	TTL      time.Duration
	Audience string
}

func (h *Handler) MintScopedToken(ctx context.Context, in MintScopedTokenInput) (string, time.Time, error) {
	if in.Audience == "" {
		return "", time.Time{}, connect.NewError(connect.CodeInvalidArgument, errors.New("audience required"))
	}
	if len(in.Scopes) == 0 {
		return "", time.Time{}, connect.NewError(connect.CodeInvalidArgument, errors.New("scopes required"))
	}
	if in.TTL <= 0 {
		return "", time.Time{}, connect.NewError(connect.CodeInvalidArgument, errors.New("ttl required"))
	}

	parent, err := h.apiKeys.GetByID(ctx, in.ApiKeyID)
	if err != nil {
		return "", time.Time{}, connect.NewError(connect.CodeNotFound, err)
	}
	if err := h.authorize(ctx, cedar.ActionMintScopedToken, parent); err != nil {
		return "", time.Time{}, err
	}
	if parent.Revoked {
		return "", time.Time{}, connect.NewError(connect.CodePermissionDenied, errors.New("parent api_key revoked"))
	}
	// Requested scopes must be ⊆ parent.Scopes.
	if !isScopeSubset(in.Scopes, parent.Scopes) {
		return "", time.Time{}, connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("requested scopes exceed parent api_key authority"))
	}

	var slug string
	if h.tenantSlug != nil && parent.TenantID != uuid.Nil {
		slug, _ = h.tenantSlug(ctx, parent.TenantID)
	}
	tok, exp, err := h.issuer.MintAccess(issuer.AccessClaims{
		Subject:    parent.ApiKeyID.String(),
		TenantID:   parent.TenantID,
		TenantSlug: slug,
		Audience:   in.Audience,
		Roles:      parent.Roles,
		Scopes:     in.Scopes,
		Kind:       auth.PrincipalKindApiKey,
		TTL:        in.TTL,
	})
	if err != nil {
		return "", time.Time{}, connect.NewError(connect.CodeInternal, err)
	}
	return tok, exp, nil
}

// ─── helpers ────────────────────────────────────────────────────────────────

func isPlatformAdmin(ctx context.Context) bool {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil || p == nil {
		return false
	}
	return p.HasRole("platform.admin")
}

func isScopeSubset(want, have []auth.Scope) bool {
	for _, h := range have {
		if h.Type == auth.ScopeWildcard {
			return true
		}
	}
	haveSet := map[string]struct{}{}
	for _, s := range have {
		haveSet[s.String()] = struct{}{}
	}
	for _, s := range want {
		if _, ok := haveSet[s.String()]; !ok {
			return false
		}
	}
	return true
}
