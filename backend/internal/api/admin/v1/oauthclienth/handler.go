// Package oauthclienth implements admin/v1.OAuthClientService: an operator's
// hold on the OAuth Authorization Server's clients (ADR-0036). The clients are
// listed by MCPInspectService; this removes one.
//
// A client is global — its grants span every tenant — so the RPC is held to
// platform.admin before Cedar runs, and Cedar's ManageOAuthClient lets a
// tenant policy forbid it for that admin.
package oauthclienth

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	oauthpkg "github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

// resourcePrefix names a client in the audit log.
const resourcePrefix = "oauthClients/"

// Handler serves OAuthClientService.
type Handler struct {
	clients oauthpkg.ClientDeleter
	// seeded are the client ids the configuration registers at boot; deleting
	// one would last only until the next boot.
	seeded map[string]struct{}
	policy cedar.Authorizer
	// revocation revokes the capabilities exchanged under a deleted client,
	// in every tenant. Nil while the capability subsystem is off.
	revocation *auth.BulkRevocation
}

// WithCapabilityRevocation lets deleting a client revoke the capabilities
// exchanged under it.
func (h *Handler) WithCapabilityRevocation(r *auth.BulkRevocation) *Handler {
	h.revocation = r
	return h
}

// noteClientDeleted is why a client's capabilities were revoked.
const noteClientDeleted = "an operator deleted the OAuth client"

var _ paladinadminv1connect.OAuthClientServiceHandler = (*Handler)(nil)

// NewHandler serves deletes against clients, refusing the ones as seeds.
func NewHandler(clients oauthpkg.ClientDeleter, as config.OAuthAS, policy cedar.Authorizer) *Handler {
	if clients == nil || policy == nil {
		panic("oauthclienth: a client store and a policy authorizer are required")
	}
	seeded := make(map[string]struct{}, len(as.SeedClients))
	for _, sc := range as.SeedClients {
		seeded[sc.ClientID] = struct{}{}
	}
	return &Handler{clients: clients, seeded: seeded, policy: policy}
}

// DeleteOAuthClient removes a client, its codes and its grants, then revokes
// every capability exchanged under it, in every tenant: a client spans them
// all. The client goes first, so none is exchanged meanwhile; the revocation
// runs even for a client already gone, so a retry after it failed finishes
// it, and only then is a client that was not there NotFound.
func (h *Handler) DeleteOAuthClient(ctx context.Context, req *adminv1.DeleteOAuthClientRequest) (*adminv1.DeleteOAuthClientResponse, error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	id := req.GetClientId()
	apiutil.StashResource(ctx, resourcePrefix+id)
	if _, ok := h.seeded[id]; ok {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Sprintf("client %q is seeded by auth.oauth.seed_clients and would be registered again at the next boot; remove it from the configuration first", id))
	}
	err := h.clients.DeleteClient(ctx, id)
	gone := errors.Is(err, oauthpkg.ErrClientNotFound)
	if err != nil && !gone {
		return nil, apiutil.MapError(fmt.Errorf("delete oauth client: %w", err))
	}
	revoked, err := h.revocation.RevokeMatching(ctx, limes.RevokeMatchingRequest{
		AllTenants: true, ClientID: id, Note: noteClientDeleted, Actor: callerSubject(ctx),
	})
	if err != nil {
		return nil, auth.AnswerCapabilityError(fmt.Errorf("revoke the capabilities of oauth client %q: %w", id, err), nil)
	}
	logger.FromContext(ctx).Info("oauth client deleted",
		zap.String("client_id", id), zap.Bool("found", !gone), zap.Int64("capabilities_revoked", revoked))
	if gone {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Sprintf("no OAuth client %q", id))
	}
	return &adminv1.DeleteOAuthClientResponse{}, nil
}

// callerSubject is the subject of the principal ctx carries, empty for none.
func callerSubject(ctx context.Context) string {
	if p, err := auth.PrincipalFromContext(ctx); err == nil && p != nil {
		return p.Subject
	}
	return ""
}

func (h *Handler) authorize(ctx context.Context) error {
	if _, err := auth.PrincipalFromContext(ctx); err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) {
		return connect.NewError(connect.CodePermissionDenied, "an OAuth client spans every tenant; only a platform admin may manage one")
	}
	_, err := apiutil.AuthorizeOwnTenant(ctx, h.policy, cedar.ActionManageOAuthClient)
	return err
}
