package logfield

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// internalRoot is the tree the gate reads, relative to this package.
const internalRoot = ".."

// urlKeys are the log keys that hold a URL. A broker URL carries its password
// in the userinfo, so these go through URL, never zap.String.
var urlKeys = map[string]bool{"url": true, "connected_url": true}

// Every zap.String under internal/ whose key names a URL is a place a
// password could be logged. RabbitMQ and NATS URLs were, before URL existed.
func TestNoURLIsLoggedRaw(t *testing.T) {
	var raw []string
	seen := 0
	err := filepath.WalkDir(internalRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "zap" || sel.Sel.Name != "String" {
				return true
			}
			seen++
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if key, _ := strconv.Unquote(lit.Value); urlKeys[key] {
				raw = append(raw, fset.Position(call.Pos()).String())
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("found no zap.String calls; the gate is reading the wrong tree")
	}
	for _, at := range raw {
		t.Errorf("%s logs a URL with zap.String; use logfield.URL", at)
	}
}
