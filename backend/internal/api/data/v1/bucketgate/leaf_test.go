package bucketgate

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// leafImports is everything the package may import. The store adapter, the
// handlers and the background workers all import it, so whatever it imports
// each of them links: a handler package here would drag the API into every
// worker binary role.
var leafImports = map[string]bool{
	"errors":                    true,
	"connectrpc.com/connect/v2": true,
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr":      true,
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1": true,
}

func TestPackageStaysALeaf(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if !leafImports[path] {
				t.Errorf("%s imports %s; the gate is a leaf every worker links, keep its imports to %v",
					name, path, leafImports)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no source files checked")
	}
}
