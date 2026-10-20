package paladin_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// limesImportPath is the capability library the server runs, which this SDK
// may use but never expose: a client works with the SDK alone.
const limesImportPath = `"github.com/oleg-tkachuk/limes`

// No exported function, method, type or field of the package names a limes
// type, so a client never needs limes to call it.
func TestExportedAPINamesNoLimesType(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		// The local names limes is imported under in this file.
		aliases := map[string]bool{}
		for _, imp := range file.Imports {
			if strings.HasPrefix(imp.Path.Value, limesImportPath) {
				alias := strings.Trim(imp.Path.Value[strings.LastIndex(imp.Path.Value, "/")+1:], `"`)
				if imp.Name != nil {
					alias = imp.Name.Name
				}
				aliases[alias] = true
			}
		}
		if len(aliases) == 0 {
			continue
		}
		names := func(n ast.Node) bool {
			found := false
			ast.Inspect(n, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && aliases[id.Name] {
						found = true
					}
				}
				return !found
			})
			return found
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Name.IsExported() && (d.Recv == nil || exportedRecv(d.Recv)) && names(d.Type) {
					t.Errorf("%s: %s names a limes type", name, d.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() && exportedPartNames(s.Type, names) {
							t.Errorf("%s: type %s names a limes type", name, s.Name.Name)
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() && s.Type != nil && names(s.Type) {
								t.Errorf("%s: %s names a limes type", name, n.Name)
							}
						}
					}
				}
			}
		}
	}
}

// exportedRecv reports whether a method's receiver is an exported type.
func exportedRecv(recv *ast.FieldList) bool {
	for _, f := range recv.List {
		typ := f.Type
		if star, ok := typ.(*ast.StarExpr); ok {
			typ = star.X
		}
		if id, ok := typ.(*ast.Ident); ok && id.IsExported() {
			return true
		}
	}
	return false
}

// exportedPartNames reports whether the part of a type a client sees — an
// exported field of a struct, or the whole of any other type — names a limes
// type.
func exportedPartNames(typ ast.Expr, names func(ast.Node) bool) bool {
	st, ok := typ.(*ast.StructType)
	if !ok {
		return names(typ)
	}
	for _, f := range st.Fields.List {
		exported := len(f.Names) == 0 // an embedded field is promoted
		for _, n := range f.Names {
			exported = exported || n.IsExported()
		}
		if exported && names(f.Type) {
			return true
		}
	}
	return false
}
