package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/exchange"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// UserDirectory finds the user a paladin-mcp token names.
type UserDirectory interface {
	GetByID(ctx context.Context, id uuid.UUID) (authstore.User, error)
}

// DelegationPolicy is the exchange policy of ADR-0041: a caller's
// paladin-mcp token is exchanged for a capability the agent holds on its
// user's behalf, for the planes asked for that the user may hold, with the
// operations the user consented to, for at most ttl.
//
// The plane authorizes the user it names, with the user's roles, so the
// policy refuses what the user could not do by their own token: a plane
// their roles do not reach, and a user disabled, gone or in another tenant
// since the token was issued.
func DelegationPolicy(users UserDirectory, ttl time.Duration) exchange.Policy {
	return func(ctx context.Context, g exchange.Grant) (limes.IssueRequest, error) {
		user, err := delegatingUser(ctx, users, g.Subject)
		if err != nil {
			return limes.IssueRequest{}, err
		}
		planes, err := delegatedPlanes(user, g)
		if err != nil {
			return limes.IssueRequest{}, err
		}
		ops, err := auth.DelegatedOps(g.Subject.Scopes)
		if err != nil {
			return limes.IssueRequest{}, fmt.Errorf("%w: %w", exchange.ErrInvalidScope, err)
		}
		client := g.Subject.ClientID
		return limes.IssueRequest{
			Subject: limes.Principal{
				Type: limes.PrincipalTypeAgent, TenantID: user.TenantID, Subject: client,
				Agent: &limes.AgentPrincipal{MCPClient: client},
			},
			OnBehalfOf: &limes.Principal{
				Type: limes.PrincipalTypeUser, TenantID: user.TenantID, Subject: user.UserID.String(),
			},
			IssuedBy: auth.DelegationIssuer(),
			Audience: planes,
			Caveats:  limes.Caveats{Ops: ops},
			TTL:      ttl,
			ClientID: client,
		}, nil
	}
}

// delegatingUser is the user subject names, as long as they may still act:
// present, enabled and in the tenant the token was issued in.
func delegatingUser(ctx context.Context, users UserDirectory, subject exchange.Subject) (authstore.User, error) {
	id, err := uuid.Parse(subject.Subject)
	if err != nil {
		return authstore.User{}, fmt.Errorf("%w: the token names no user: %w", exchange.ErrInvalidGrant, err)
	}
	tenant, _ := subject.Claims[subjectClaimTenant].(string)
	user, err := users.GetByID(ctx, id)
	switch {
	case errors.Is(err, authstore.ErrNotFound):
		return authstore.User{}, fmt.Errorf("%w: user %s is gone", exchange.ErrInvalidGrant, id)
	case err != nil:
		return authstore.User{}, fmt.Errorf("mcp: user %s: %w", id, err)
	case user.Disabled || user.TenantID.String() != tenant:
		return authstore.User{}, fmt.Errorf("%w: user %s may no longer act in tenant %s", exchange.ErrInvalidGrant, id, tenant)
	}
	return user, nil
}

// delegatedPlanes is what g asks for that user may hold: every target must
// be a plane, and a plane the user's roles do not reach is left out, as
// ExchangeAudience would refuse it. Nothing left is a refusal.
func delegatedPlanes(user authstore.User, g exchange.Grant) ([]string, error) {
	targets := append(append([]string(nil), g.Resources...), g.Audiences...)
	planes := make([]string, 0, len(targets))
	for _, plane := range targets {
		switch err := apiutil.AudienceEligible(user.Roles, auth.PlaneAudience(plane)); {
		case errors.Is(err, apiutil.ErrAudienceRole):
			continue
		case err != nil:
			return nil, fmt.Errorf("%w: %q is no plane: %w", exchange.ErrInvalidTarget, plane, err)
		}
		planes = append(planes, plane)
	}
	if len(planes) == 0 {
		return nil, fmt.Errorf("%w: the user may hold none of %v", exchange.ErrInvalidTarget, targets)
	}
	return planes, nil
}
