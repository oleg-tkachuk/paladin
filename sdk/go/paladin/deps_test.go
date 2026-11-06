package paladin_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// observabilityModules are the module path prefixes the SDK must neither
// require nor link: a published module's requirements become its consumers',
// and observability is theirs to choose (ADR-0020, ADR-0043). A test that
// needs one lives in a module of its own, as internal/oteltest does.
var observabilityModules = []string{
	"go.opentelemetry.io/",
	"connectrpc.com/otelconnect",
}

// moduleDir is the SDK module's root, relative to this package.
const moduleDir = ".."

func observability(path string) bool {
	for _, prefix := range observabilityModules {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// goJSON runs the go command in dir and decodes its JSON output, one value
// after another, into each value next returns.
func goJSON(t *testing.T, dir string, next func(*json.Decoder) error, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go %s: %v", strings.Join(args, " "), err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		if err := next(dec); errors.Is(err, io.EOF) {
			return
		} else if err != nil {
			t.Fatalf("go %s: decoding: %v", strings.Join(args, " "), err)
		}
	}
}

// The module requires no observability module, directly or indirectly.
func TestModuleRequiresNoObservability(t *testing.T) {
	var mod struct{ Require []struct{ Path string } }
	goJSON(t, moduleDir, func(dec *json.Decoder) error { return dec.Decode(&mod) }, "mod", "edit", "-json")
	if len(mod.Require) == 0 {
		t.Fatal("go.mod lists no requirement; the check would pass vacuously")
	}
	for _, req := range mod.Require {
		if observability(req.Path) {
			t.Errorf("go.mod requires %s", req.Path)
		}
	}
}

// The package and its tests link no observability package.
func TestPackageLinksNoObservability(t *testing.T) {
	linked := 0
	goJSON(t, ".", func(dec *json.Decoder) error {
		var pkg struct{ ImportPath string }
		if err := dec.Decode(&pkg); err != nil {
			return err
		}
		linked++
		if observability(pkg.ImportPath) {
			t.Errorf("paladin links %s", pkg.ImportPath)
		}
		return nil
	}, "list", "-deps", "-test", "-json=ImportPath", ".")
	if linked == 0 {
		t.Fatal("go list named no package; the check would pass vacuously")
	}
}
