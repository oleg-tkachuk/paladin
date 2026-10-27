package oauthclienth

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	oauthpkg "github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

const (
	seeded     = "paladin-mcp-server"
	registered = "paladin-0199-dcr"
)

type fakeAuthorizer struct {
	decision  cedar.Decision
	err       error
	gotAction cedar.Action
	calls     int
}

func (a *fakeAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, action cedar.Action, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.calls++
	a.gotAction = action
	return a.decision, a.err
}

type fakeClients struct {
	deleted []string
	err     error
}

func (f *fakeClients) DeleteClient(_ context.Context, id string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func ctxAs(roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "tester", TenantID: uuid.New(), TenantSlug: "acme", Roles: roles,
	})
}

func newHandler(clients *fakeClients, policy cedar.Authorizer) *Handler {
	return NewHandler(clients, config.OAuthAS{SeedClients: []config.OAuthSeedClient{{ClientID: seeded}}}, policy)
}

func del(h *Handler, ctx context.Context, id string) error {
	_, err := h.DeleteOAuthClient(ctx, &adminv1.DeleteOAuthClientRequest{ClientId: id})
	return err
}

// A platform admin Cedar allows deletes a client that is not seeded.
func TestDeleteRemovesAClient(t *testing.T) {
	clients := &fakeClients{}
	policy := &fakeAuthorizer{decision: cedar.DecisionAllow}
	if err := del(newHandler(clients, policy), ctxAs(apiutil.RolePlatformAdmin), registered); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(clients.deleted) != 1 || clients.deleted[0] != registered {
		t.Errorf("deleted = %v, want [%s]", clients.deleted, registered)
	}
	if policy.gotAction != cedar.ActionManageOAuthClient {
		t.Errorf("authorized %v, want ManageOAuthClient", policy.gotAction)
	}
}

func TestDeleteRefusals(t *testing.T) {
	cases := []struct {
		name     string
		ctx      context.Context
		policy   *fakeAuthorizer
		clients  *fakeClients
		id       string
		want     connect.Code
		askCedar bool
	}{
		{name: "anonymous", ctx: context.Background(), policy: &fakeAuthorizer{decision: cedar.DecisionAllow},
			id: registered, want: connect.CodeUnauthenticated},
		// A client spans every tenant: a tenant admin is refused before a
		// tenant policy could be asked to permit it.
		{name: "tenant admin", ctx: ctxAs("tenant.admin"), policy: &fakeAuthorizer{decision: cedar.DecisionAllow},
			id: registered, want: connect.CodePermissionDenied},
		{name: "forbidden by policy", ctx: ctxAs(apiutil.RolePlatformAdmin), policy: &fakeAuthorizer{decision: cedar.DecisionDeny},
			id: registered, want: connect.CodePermissionDenied, askCedar: true},
		{name: "seeded", ctx: ctxAs(apiutil.RolePlatformAdmin), policy: &fakeAuthorizer{decision: cedar.DecisionAllow},
			id: seeded, want: connect.CodeFailedPrecondition, askCedar: true},
		{name: "unknown", ctx: ctxAs(apiutil.RolePlatformAdmin), policy: &fakeAuthorizer{decision: cedar.DecisionAllow},
			clients: &fakeClients{err: oauthpkg.ErrClientNotFound}, id: registered, want: connect.CodeNotFound, askCedar: true},
		{name: "store down", ctx: ctxAs(apiutil.RolePlatformAdmin), policy: &fakeAuthorizer{decision: cedar.DecisionAllow},
			clients: &fakeClients{err: errors.New("pool closed")}, id: registered, want: connect.CodeInternal, askCedar: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clients := c.clients
			if clients == nil {
				clients = &fakeClients{}
			}
			err := del(newHandler(clients, c.policy), c.ctx, c.id)
			if got := connect.CodeOf(err); got != c.want {
				t.Fatalf("code = %v (%v), want %v", got, err, c.want)
			}
			if clients.err == nil && len(clients.deleted) != 0 {
				t.Errorf("a refused delete removed %v", clients.deleted)
			}
			if asked := c.policy.calls > 0; asked != c.askCedar {
				t.Errorf("asked Cedar = %v, want %v", asked, c.askCedar)
			}
		})
	}
}

func TestNewHandlerRequiresItsDependencies(t *testing.T) {
	for name, build := range map[string]func(){
		"no store":  func() { NewHandler(nil, config.OAuthAS{}, &fakeAuthorizer{}) },
		"no policy": func() { NewHandler(&fakeClients{}, config.OAuthAS{}, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("built without it")
				}
			}()
			build()
		})
	}
}
