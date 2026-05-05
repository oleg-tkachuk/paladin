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
)

type Handler struct {
	apiKeys authstore.ApiKeyRepository
	issuer  *issuer.Issuer
	now     func() time.Time
}

func NewHandler(keys authstore.ApiKeyRepository, iss *issuer.Issuer) *Handler {
	return &Handler{apiKeys: keys, issuer: iss, now: time.Now}
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
	return h.apiKeys.List(ctx, args)
}

func (h *Handler) RevokeApiKey(ctx context.Context, id uuid.UUID) error {
	if _, err := h.GetApiKey(ctx, id); err != nil {
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
	if parent.Revoked {
		return "", time.Time{}, connect.NewError(connect.CodePermissionDenied, errors.New("parent api_key revoked"))
	}
	// Requested scopes must be ⊆ parent.Scopes.
	if !isScopeSubset(in.Scopes, parent.Scopes) {
		return "", time.Time{}, connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("requested scopes exceed parent api_key authority"))
	}

	tok, exp, err := h.issuer.MintAccess(issuer.AccessClaims{
		Subject:  parent.ApiKeyID.String(),
		TenantID: parent.TenantID,
		Audience: in.Audience,
		Roles:    parent.Roles,
		Scopes:   in.Scopes,
		Kind:     auth.PrincipalKindApiKey,
		TTL:      in.TTL,
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
