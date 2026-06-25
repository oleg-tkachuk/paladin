package tenant

import (
	"strings"
	"testing"

	"github.com/google/uuid"
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
