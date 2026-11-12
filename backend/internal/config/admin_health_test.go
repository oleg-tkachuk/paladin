package config

import (
	"strings"
	"testing"
	"time"
)

// Nothing configured reads no role, within the default bound.
func TestAdminHealthDefaults(t *testing.T) {
	cfg, err := loadTrashRetention(t, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Admin.Health.Roles) != 0 {
		t.Errorf("admin.health.roles = %v, want none", cfg.Admin.Health.Roles)
	}
	if got, want := cfg.Admin.Health.Timeout, 5*time.Second; got != want {
		t.Errorf("admin.health.timeout = %v, want %v", got, want)
	}
}

func TestAdminHealthRolesLoadInOrder(t *testing.T) {
	cfg, err := loadTrashRetention(t, `
admin:
  health:
    timeout: 2s
    roles:
      - { name: api, url: "http://paladin-core-api:8080" }
      - { name: iam, url: "http://paladin-core-iam:8085" }
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []AdminHealthRole{
		{Name: "api", URL: "http://paladin-core-api:8080"},
		{Name: "iam", URL: "http://paladin-core-iam:8085"},
	}
	got := cfg.Admin.Health.Roles
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("admin.health.roles = %v, want %v", got, want)
	}
	if cfg.Admin.Health.Timeout != 2*time.Second {
		t.Errorf("admin.health.timeout = %v, want 2s", cfg.Admin.Health.Timeout)
	}
}

func TestAdminHealthRefusesAMalformedRole(t *testing.T) {
	for name, extra := range map[string]string{
		"no scheme":    "admin:\n  health:\n    roles: [{ name: api, url: \"paladin-core-api:8080\" }]\n",
		"bad name":     "admin:\n  health:\n    roles: [{ name: API, url: \"http://x:1\" }]\n",
		"unknown key":  "admin:\n  health:\n    roles: [{ name: api, url: \"http://x:1\", port: 1 }]\n",
		"zero timeout": "admin:\n  health:\n    timeout: 0s\n",
	} {
		if _, err := loadTrashRetention(t, extra); err == nil {
			t.Errorf("%s: loaded, want refused", name)
		}
	}
	c := minimalValidConfig()
	c.Admin.Health.Timeout = 0
	mustReject(t, c, "admin.health.timeout")
}

func TestAdminHealthRefusesADuplicateRole(t *testing.T) {
	_, err := loadTrashRetention(t, `
admin:
  health:
    roles:
      - { name: api, url: "http://a:1" }
      - { name: api, url: "http://b:1" }
`)
	if err == nil || !strings.Contains(err.Error(), "admin.health.roles") {
		t.Fatalf("Load with api twice: %v, want it refused naming admin.health.roles", err)
	}
}
