package auth

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// DelegatingUser is what a plane needs of the user a delegated capability
// acts for, read on every call so a role removed or a user disabled takes
// effect at the next one.
type DelegatingUser struct {
	TenantID uuid.UUID
	Roles    []string
	Scopes   []Scope
	Disabled bool
}

// DelegatingUserLookup finds a user by id, or answers ErrDelegatingUserGone.
type DelegatingUserLookup func(ctx context.Context, id uuid.UUID) (DelegatingUser, error)

// ErrDelegatingUserGone is a delegated capability's user that is no longer
// there.
var ErrDelegatingUserGone = errors.New("auth: the user a delegated capability acts for is gone")

// errDelegationRefused is a delegated capability whose user may not act.
var errDelegationRefused = errors.New("auth: delegated capability refused")

// delegation is how an interceptor recognises a delegated capability and
// finds its user.
type delegation struct {
	issuer string
	users  DelegatingUserLookup
}

// WithDelegation makes a capability whose `iss` is issuer act for the user
// it names (ADR-0041), on any plane: the user is read through users, and
// the principal established is theirs. Without it a delegated capability is
// a capability like any other.
func WithDelegation(issuer string, users DelegatingUserLookup) CapabilityOption {
	return func(i *capabilityInterceptor) { i.delegation = &delegation{issuer: issuer, users: users} }
}

// withPrincipal establishes the principal a verified capability stands for:
// its user's when the MCP server issued it, and otherwise its own, where the
// plane lets a capability be an identity. An existing principal wins.
func (i *capabilityInterceptor) withPrincipal(ctx context.Context, c *limes.Capability) (context.Context, error) {
	if i.delegation == nil || c.Issuer != i.delegation.issuer {
		ctx, err := i.withCapabilityPrincipal(ctx, c)
		if err != nil {
			return ctx, capabilityError(connect.CodePermissionDenied, err)
		}
		return ctx, nil
	}
	if _, err := PrincipalFromContext(ctx); err == nil {
		return ctx, nil
	}
	p, err := i.delegatedPrincipal(ctx, c)
	if err != nil {
		return ctx, err
	}
	return WithPrincipal(ctx, p), nil
}

// delegatedPrincipal is the principal of the user c acts for: the one
// ADR-0036's exchanged token carried, with the user's current roles.
func (i *capabilityInterceptor) delegatedPrincipal(ctx context.Context, c *limes.Capability) (*Principal, error) {
	log := logger.FromContext(ctx).With(zap.String("capability_id", c.ID.String()), zap.String("client_id", c.ClientID))
	refuse := func(reason error) (*Principal, error) {
		log.Warn("delegated capability refused", zap.Error(reason))
		return nil, capabilityError(connect.CodePermissionDenied, fmt.Errorf("%w: %w", errDelegationRefused, reason))
	}
	if c.OnBehalfOf == nil {
		return refuse(errors.New("it names no user"))
	}
	id, err := uuid.Parse(c.OnBehalfOf.Subject)
	if err != nil {
		return refuse(fmt.Errorf("it names no user id: %w", err))
	}
	user, err := i.delegation.users(ctx, id)
	switch {
	case errors.Is(err, ErrDelegatingUserGone):
		return refuse(err)
	case err != nil:
		log.Error("delegated capability's user not read", zap.String("user_id", id.String()), zap.Error(err))
		return nil, capabilityError(connect.CodeUnavailable, fmt.Errorf("auth: user %s: %w", id, err))
	case user.Disabled || user.TenantID != c.OnBehalfOf.TenantID:
		return refuse(fmt.Errorf("user %s may no longer act in tenant %s", id, c.OnBehalfOf.TenantID))
	}
	audience := PlaneAudience(i.audience)
	return &Principal{
		TenantID:      user.TenantID,
		Subject:       id.String(),
		Roles:         user.Roles,
		Scopes:        user.Scopes,
		Audience:      audience,
		Audiences:     []string{audience},
		ExpiresAt:     c.ExpiresAt,
		Kind:          PrincipalKindUser,
		OAuthClientID: c.ClientID,
		OAuthScopes:   DelegatedScopes(c.Caveats.Ops),
		Actor:         c.Subject.Subject,
	}, nil
}
