package tenanth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
)

// RetiredSlugNamePrefix is the resource-name prefix of a retired slug:
// "retiredTenantSlugs/{slug}".
const RetiredSlugNamePrefix = "retiredTenantSlugs/"

// RetiredSlugPage is a page of retired slugs and whether CreateTenant refuses
// them: admin.tenants.reserve_slugs.
type RetiredSlugPage struct {
	Slugs     []RetiredSlug
	NextToken string
	Enforced  bool
}

// ListRetiredTenantSlugs lists the slugs purges recorded. Platform admins
// only, as ListTenants: the rows span every tenant there was.
func (h *Handler) ListRetiredTenantSlugs(ctx context.Context, pageSize int32, pageToken string) (RetiredSlugPage, error) {
	if err := requirePlatformAdmin(ctx); err != nil {
		return RetiredSlugPage{}, err
	}
	if err := h.authorize(ctx, cedar.ActionReadTenant, uuid.Nil()); err != nil {
		return RetiredSlugPage{}, err
	}
	slugs, next, err := h.repo.ListRetiredSlugs(ctx, pageToken, pageSize)
	if err != nil {
		h.log.Error("list retired tenant slugs failed", zap.Error(err))
		return RetiredSlugPage{}, apiutil.MapError(err)
	}
	return RetiredSlugPage{Slugs: slugs, NextToken: next, Enforced: !h.allowSlugReuse}, nil
}

// ReleaseTenantSlug forgets a retired slug, so a new tenant can take it.
// Platform admins only: whatever still names the slug then names the new
// tenant. Idempotent on a slug not retired.
func (h *Handler) ReleaseTenantSlug(ctx context.Context, name string) error {
	if err := requirePlatformAdmin(ctx); err != nil {
		return err
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, uuid.Nil()); err != nil {
		return err
	}
	slug, err := ParseRetiredSlugName(name)
	if err != nil {
		return rpcerr.New(connect.CodeInvalidArgument, err)
	}
	caller, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	apiutil.StashResource(ctx, RetiredSlugNamePrefix+slug)
	former, err := h.repo.ReleaseSlug(ctx, slug)
	switch {
	case errors.Is(err, ErrSlugNotRetired):
		h.log.Info("tenant slug release: not retired, nothing to do",
			zap.String("slug", slug), zap.String("actor", caller.Subject))
		return nil
	case err != nil:
		h.log.Error("tenant slug release failed",
			zap.String("slug", slug), zap.String("actor", caller.Subject), zap.Error(err))
		return apiutil.MapError(err)
	}
	h.log.Info("tenant slug released",
		zap.String("slug", slug),
		zap.String("former_tenant_id", former.String()),
		zap.String("actor", caller.Subject))
	return nil
}

// ParseRetiredSlugName is the slug in "retiredTenantSlugs/{slug}", which must
// be a valid tenant slug.
func ParseRetiredSlugName(name string) (string, error) {
	slug, ok := strings.CutPrefix(name, RetiredSlugNamePrefix)
	if !ok {
		return "", fmt.Errorf("name %q: want %s{slug}", name, RetiredSlugNamePrefix)
	}
	if err := apiutil.ValidateTenantSlug(slug); err != nil {
		return "", fmt.Errorf("name %q: %w", name, err)
	}
	return slug, nil
}
