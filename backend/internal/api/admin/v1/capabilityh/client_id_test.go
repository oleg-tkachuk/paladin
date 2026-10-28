package capabilityh

import (
	"context"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/limes"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// clientStore keeps the client of the capability put on record.
type clientStore struct {
	fakeStore
	clientID string
}

func (s *clientStore) Insert(ctx context.Context, req limes.InsertRequest) error {
	s.clientID = req.Capability.ClientID
	return s.fakeStore.Insert(ctx, req)
}

// A capability is granted to the OAuth client its issuer acts for — an
// agent's, through MCP — and to Paladin itself when the issuer acts for
// none, as a console session or an API token does.
func TestIssueGrantsTheCallersOAuthClient(t *testing.T) {
	const agentClient = "agent-client"
	for _, tc := range []struct {
		name   string
		client string
		want   string
	}{
		{"an issuer acting for an OAuth client", agentClient, agentClient},
		{"an issuer acting for none", "", DirectClientID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &clientStore{}
			h := mkHandler(t, store, nil, &allowAuthorizer{})
			ctx := auth.WithPrincipal(context.Background(), &auth.Principal{
				Subject: "operator", TenantID: uuid.New(), Roles: []string{"platform.admin"},
				OAuthClientID: tc.client,
			})
			if _, err := h.Issue(ctx, &adminv1.CapabilityServiceIssueRequest{
				Subject: &adminv1.CapabilityPrincipal{
					Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: uuid.New().String(), Subject: "alice",
				},
				Audience:   []string{"paladin-data"},
				TtlSeconds: 300,
				Caveats:    openCaveats(limes.OpGet),
			}); err != nil {
				t.Fatalf("Issue: %v", err)
			}
			if store.clientID != tc.want {
				t.Errorf("client = %q, want %q", store.clientID, tc.want)
			}
		})
	}
}
