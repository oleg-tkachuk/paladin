package apiutil

import (
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin-private/internal/auth"
)

func TestScopeStrings(t *testing.T) {
	t.Run("empty -> nil", func(t *testing.T) {
		if got := ScopeStrings(nil); got != nil {
			t.Errorf("ScopeStrings(nil) = %v, want nil", got)
		}
		if got := ScopeStrings([]auth.Scope{}); got != nil {
			t.Errorf("ScopeStrings([]) = %v, want nil", got)
		}
	})
	t.Run("maps to wire form in declaration order", func(t *testing.T) {
		got := ScopeStrings([]auth.Scope{
			{Type: auth.ScopeTenant, Value: "t1"},
			{Type: auth.ScopeBucket, Value: "b1"},
			{Type: auth.ScopeWildcard},
		})
		want := []string{"tenant:t1", "bucket:b1", "*"}
		if len(got) != len(want) {
			t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})
}

func TestCedarPrincipal(t *testing.T) {
	t.Run("nil principal -> empty cedar principal", func(t *testing.T) {
		cp := CedarPrincipal(nil)
		if cp == nil {
			t.Fatal("want non-nil cedar principal")
		}
		if cp.Subject != "" || cp.TenantID != uuid.Nil || len(cp.Roles) != 0 ||
			len(cp.Scopes) != 0 || cp.TenantSlug != "" {
			t.Errorf("want zero-value cedar principal, got %+v", cp)
		}
	})
	t.Run("maps every field, scopes flattened to wire form", func(t *testing.T) {
		tid := uuid.MustParse("0a8c0000-0000-7000-8000-000000000f12")
		p := &auth.Principal{
			Subject:    "svc-uploader",
			TenantID:   tid,
			TenantSlug: "acme-corp",
			Roles:      []string{"tenant.admin", "bucket.admin"},
			Scopes: []auth.Scope{
				{Type: auth.ScopeTenant, Value: "t1"},
				{Type: auth.ScopeObjectKey, Value: "ok1"},
			},
		}
		cp := CedarPrincipal(p)
		if cp.Subject != "svc-uploader" || cp.TenantID != tid ||
			cp.TenantSlug != "acme-corp" {
			t.Errorf("identity mismatch: %+v", cp)
		}
		if len(cp.Roles) != 2 || cp.Roles[0] != "tenant.admin" {
			t.Errorf("roles mismatch: %v", cp.Roles)
		}
		wantScopes := []string{"tenant:t1", "object_key:ok1"}
		if len(cp.Scopes) != len(wantScopes) {
			t.Fatalf("scopes len = %d, want %d (%v)", len(cp.Scopes), len(wantScopes), cp.Scopes)
		}
		for i := range wantScopes {
			if cp.Scopes[i] != wantScopes[i] {
				t.Errorf("scope[%d] = %q, want %q", i, cp.Scopes[i], wantScopes[i])
			}
		}
	})
}
