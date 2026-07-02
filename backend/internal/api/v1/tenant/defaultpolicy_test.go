package tenant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

func TestRenderDefaultPolicy(t *testing.T) {
	tid := uuid.MustParse("0a8c0000-0000-7000-8000-000000000f12")

	t.Run("substitutes the slug as the Tenant UID", func(t *testing.T) {
		got := renderDefaultPolicy(tid, "acme")
		if strings.Contains(got, "placeholder") {
			t.Error("rendered policy still contains the literal placeholder")
		}
		if !strings.Contains(got, `Tenant::"acme"`) {
			t.Error("rendered policy is missing Tenant::\"acme\"")
		}
		// The template carries several placeholders — all must be replaced.
		if strings.Count(got, `Tenant::"acme"`) < 2 {
			t.Errorf("expected the slug substituted at every site, got %d",
				strings.Count(got, `Tenant::"acme"`))
		}
	})

	t.Run("falls back to the UUID when slug is empty", func(t *testing.T) {
		got := renderDefaultPolicy(tid, "")
		if strings.Contains(got, "placeholder") {
			t.Error("rendered policy still contains the literal placeholder")
		}
		if !strings.Contains(got, `Tenant::"`+tid.String()+`"`) {
			t.Errorf("expected the UUID %s substituted as the Tenant UID", tid)
		}
	})

	t.Run("is non-empty and parses as Cedar permit blocks", func(t *testing.T) {
		got := renderDefaultPolicy(tid, "acme")
		if len(strings.TrimSpace(got)) == 0 {
			t.Fatal("rendered policy is empty")
		}
		// Sanity: the deny-by-default template grants reads + writes to members.
		if !strings.Contains(got, "GetObject") || !strings.Contains(got, "PutObject") {
			t.Error("rendered policy is missing the expected member actions")
		}
	})
}

// ─── ReadTenant through the real Cedar engine ────────────────────────────────
//
// Regression for the confirmed live 403: the default policy granted no
// Action::"ReadTenant" at all, so tenant.admin (and every other member) was
// denied reading its OWN tenant record — the handler allows own-tenant reads
// and Cedar's deny-by-default then rejected them. These pin the rendered
// default policy through the real engine: own-tenant reads allow (admin and
// plain member alike), foreign-tenant reads stay denied at the policy layer.

type staticPolicyStore struct{ text string }

func (s staticPolicyStore) Fetch(context.Context, uuid.UUID, string) (string, []byte, error) {
	return s.text, []byte("h"), nil
}
func (s staticPolicyStore) Watch(context.Context) (<-chan cedar.ChangeEvent, error) {
	return nil, nil
}

func TestDefaultPolicy_ReadTenant(t *testing.T) {
	tid := uuid.MustParse("0a8c0000-0000-7000-8000-000000000f12")
	foreign := uuid.MustParse("0a8c0000-0000-7000-8000-00000000beef")
	engine := cedar.NewEngine(staticPolicyStore{text: renderDefaultPolicy(tid, "acme")}, time.Minute)

	authz := func(roles []string, resourceTenant uuid.UUID) cedar.Decision {
		t.Helper()
		dec, err := engine.IsAuthorized(context.Background(),
			&cedar.Principal{Subject: "op-1", TenantID: tid, TenantSlug: "acme", Roles: roles},
			cedar.ActionReadTenant,
			&cedar.Resource{TenantID: resourceTenant},
			cedar.RequestContext{Now: time.Now()},
		)
		if err != nil {
			t.Fatalf("IsAuthorized: %v", err)
		}
		return dec
	}

	if got := authz([]string{"tenant.admin"}, tid); got != cedar.DecisionAllow {
		t.Errorf("tenant.admin reading OWN tenant = %v, want Allow", got)
	}
	if got := authz([]string{"tenant.user"}, tid); got != cedar.DecisionAllow {
		t.Errorf("plain member reading OWN tenant = %v, want Allow (UI needs it on login)", got)
	}
	if got := authz([]string{"tenant.admin"}, foreign); got != cedar.DecisionDeny {
		t.Errorf("tenant.admin reading FOREIGN tenant = %v, want Deny at the policy layer", got)
	}
	if got := authz([]string{"platform.admin"}, foreign); got != cedar.DecisionAllow {
		t.Errorf("platform.admin reading foreign tenant = %v, want Allow", got)
	}
}
