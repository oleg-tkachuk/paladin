package systemh

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/config"
)

// MarshalRedacted is the only path by which the running configuration leaves
// the process, and it was at 0% coverage. Everything an operator can read on
// the /config page comes through here, which makes two properties worth
// holding: the role gate, and the redaction.
//
// The redaction is the one that would fail quietly. Config.Obfuscated()
// replaces secrets with sentinels; if a newly added secret field is missed
// there, nothing errors and nothing looks wrong — the page simply renders a
// live credential to whoever can read it.

func adminCtx() context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "ops@example.test",
		Roles:   []string{"platform.admin"},
	})
}

func secretBearingConfig() config.Config {
	var c config.Config
	c.Auth.SigningKey = "signing-key-never-print-me"
	c.APIToken.HMACKey = "hmac-pepper-never-print-me"
	c.Datastores.Postgres.Password = "postgres-password-never-print-me"
	return c
}

func TestMarshalRedactedRequiresPlatformAdmin(t *testing.T) {
	h := New(secretBearingConfig(), "/app/configs/paladin.yaml")

	for _, roles := range [][]string{nil, {"tenant.admin"}, {"bucket.admin"}} {
		ctx := auth.WithPrincipal(context.Background(), &auth.Principal{
			Subject: "someone", Roles: roles,
		})
		_, _, err := h.MarshalRedacted(ctx)
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("roles=%v: code = %v, want PermissionDenied — this method returns the whole running config",
				roles, connect.CodeOf(err))
		}
	}
}

func TestMarshalRedactedDoesNotEmitSecrets(t *testing.T) {
	cfg := secretBearingConfig()
	h := New(cfg, "/app/configs/paladin.yaml")

	blob, path, err := h.MarshalRedacted(adminCtx())
	if err != nil {
		t.Fatalf("MarshalRedacted: %v", err)
	}
	if path != "/app/configs/paladin.yaml" {
		t.Errorf("source path = %q, want the path the binary loaded", path)
	}

	// Every secret planted above must be absent from the payload verbatim. A
	// miss here is not a formatting problem: it is a live credential rendered
	// on an operator page.
	for _, secret := range []string{
		cfg.Auth.SigningKey,
		cfg.APIToken.HMACKey,
		cfg.Datastores.Postgres.Password,
	} {
		if strings.Contains(blob, secret) {
			t.Errorf("the redacted config contains %q verbatim", secret)
		}
	}
	// And the redaction has to be visible rather than silently dropping the
	// field — an operator needs to see that a value IS set, without seeing it.
	if !strings.Contains(blob, "***") {
		t.Error("no redaction sentinel in the payload; secrets appear to have been omitted rather than masked, which reads as 'not configured'")
	}
}

func TestMarshalRedactedStillDescribesTheConfig(t *testing.T) {
	// Redaction must not empty the page: the non-secret shape is the reason
	// the endpoint exists.
	cfg := secretBearingConfig()
	cfg.App.Name = "paladin-core"
	cfg.App.Env = "local"
	h := New(cfg, "/app/configs/paladin.yaml")

	blob, _, err := h.MarshalRedacted(adminCtx())
	if err != nil {
		t.Fatalf("MarshalRedacted: %v", err)
	}
	for _, want := range []string{"paladin-core", "local"} {
		if !strings.Contains(blob, want) {
			t.Errorf("payload is missing %q — redaction removed more than the secrets", want)
		}
	}
}
