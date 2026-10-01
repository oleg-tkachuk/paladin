package auth

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

// taskfile is the backend Taskfile, which declares the dev-token defaults on
// its `auth` include.
var taskfile = filepath.Join("..", "..", "Taskfile.yaml")

// templateDefault captures X from `{{.VAR | default "X"}}`.
var templateDefault = regexp.MustCompile(`\|\s*default\s+"([^"]+)"`)

// A dev token minted with an audience no plane accepts is refused by every
// RPC as `audience mismatch`; it used to default to "paladin-api", which
// matches none of them.
func TestDevTokenDefaultAudienceIsAPlane(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(taskfile)
	if err != nil {
		t.Fatal(err)
	}
	var tf struct {
		Includes map[string]struct {
			Vars map[string]string `yaml:"vars"`
		} `yaml:"includes"`
	}
	if err := yaml.Unmarshal(raw, &tf); err != nil {
		t.Fatal(err)
	}
	value, ok := tf.Includes["auth"].Vars["JWT_AUD"]
	if !ok {
		t.Fatal("Taskfile.yaml: includes.auth.vars.JWT_AUD not found")
	}
	m := templateDefault.FindStringSubmatch(value)
	if m == nil {
		t.Fatalf("JWT_AUD %q has no default", value)
	}
	planes := []string{AudienceData, AudienceAdmin, AudienceIAM}
	if !slices.Contains(planes, m[1]) {
		t.Errorf("dev token defaults to audience %q; planes accept %v", m[1], planes)
	}
}
