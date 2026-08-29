package app_test

// Every handler that can emit lifecycle events takes its producer through an
// OPTIONAL, nil-safe setter. That is the right shape — a role that emits
// nothing should not have to wire one — and it means a deleted wiring line is
// invisible. Nothing fails to compile, nothing panics, the feature just stops.
//
// Measured, not assumed. Removing `objH.SetEventProducer(apiDispatcher)` from
// build_listeners_api.go leaves `go build` clean, every unit test green, and
// every integration test in this repository green — while Paladin silently
// stops telling any subscriber that objects were uploaded, deleted or purged.
//
// The integration suite cannot catch it: those tests wire a producer onto a
// handler they construct themselves, so they prove the mechanism works once
// connected, never that the app connects it.
//
// This is the static half of that check, in the style of
// connectshim/mapping_test.go: find every handler the app builds whose type
// CAN emit, and require the wiring call beside it. It cannot tell a
// mis-wired producer from a correct one — only a missing one — which is
// exactly the failure that was measured. BACKLOG carries the runtime version
// (assemble the real muxes, make one request, require the outbox row); the
// first attempt at it is recorded there with what stopped it.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// emitterTypes are the concrete types that define SetEventProducer, as
// "pkg.Type" — discovered rather than listed, so a handler is covered the day
// it gains the method.
//
// Type, not package: object.VersionHandler and object.LockHandler share a
// package with object.Handler and emit nothing of their own. A package-level
// rule reported both as unwired, which is the kind of noise that gets a gate
// switched off.
func emitterTypes(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	roots := []string{"../api", "../worker"}
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") {
				return err
			}
			src, rerr := os.ReadFile(path) // #nosec G304 — walking our own tree
			if rerr != nil {
				return rerr
			}
			pkg := filepath.Base(filepath.Dir(path))
			for _, m := range recvRe.FindAllStringSubmatch(string(src), -1) {
				out[pkg+"."+m[1]] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(out) == 0 {
		t.Fatal("found no types defining SetEventProducer — the discovery is broken, " +
			"which would make this test pass by finding nothing")
	}
	return out
}

// recvRe matches the receiver type of a SetEventProducer method declaration.
var recvRe = regexp.MustCompile(`func \([a-z]+ \*([A-Za-z_]\w*)\) SetEventProducer\(`)

// skipWiring records handlers deliberately built without a producer. An entry
// is a claim that the silence is CORRECT, not that nobody got round to it.
var skipWiring = map[string]string{
	// Empty, and that is the point: every handler the app builds from an
	// emitting type is wired today. An entry here is a claim that a
	// particular silence is CORRECT — not that nobody got round to it.
}

// importAliases maps the identifier a file actually uses for each import to
// the package's directory name.
//
// Without this the check has silent blind spots: wire.ProvideCollectionHandler
// returns *objectkey.Handler, where objectkey is an alias for the collection
// package. Reading the selector literally produced "objectkey.Handler", which
// matched no discovered type, so deleting that handler's wiring went
// unnoticed by the very test written to notice it. Found by removing each of
// the ten wiring lines in turn and checking the failure — four of five caught
// on the first pass.
func importAliases(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		base := path[strings.LastIndex(path, "/")+1:]
		name := base
		if imp.Name != nil {
			name = imp.Name.Name
		}
		out[name] = base
	}
	return out
}

// providerReturnTypes maps wire.ProvideXxx → "pkg.Type" of the handler it
// returns, for providers that return an emitting type.
func providerReturnTypes(t *testing.T, emitters map[string]bool) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	out := map[string]string{}
	files, err := filepath.Glob("../wire/*.go")
	if err != nil {
		t.Fatalf("glob wire: %v", err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		aliases := importAliases(f)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Provide") {
				continue
			}
			if fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
				continue
			}
			star, ok := fn.Type.Results.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			sel, ok := star.X.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				continue
			}
			qualified := aliases[pkg.Name] + "." + sel.Sel.Name
			if !emitters[qualified] {
				continue
			}
			out[fn.Name.Name] = qualified
		}
	}
	if len(out) == 0 {
		t.Fatal("resolved no wire providers to an emitting type — the app builds its " +
			"handlers through them, so finding none means this test checks almost nothing")
	}
	return out
}

// TestEveryEmittingHandlerIsWired walks the app's builders and requires that a
// handler constructed from an emitter package has SetEventProducer called on
// it in the same function.
func TestEveryEmittingHandlerIsWired(t *testing.T) {
	emitters := emitterTypes(t)
	providers := providerReturnTypes(t, emitters)

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	fset := token.NewFileSet()
	var missing []string
	checked := 0

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			built := builtEmitters(fn, emitters, providers, importAliases(f))
			if len(built) == 0 {
				continue
			}
			wired := wiredIn(fn)
			for name, ctor := range built {
				checked++
				if wired[name] {
					continue
				}
				if _, skip := skipWiring[ctor]; skip {
					continue
				}
				missing = append(missing,
					path+":"+fn.Name.Name+": "+name+" = "+ctor+
						" — built from a package that emits events, but no SetEventProducer call")
			}
		}
	}

	if checked == 0 {
		t.Fatal("checked no handlers — the walk found nothing, so a green result means nothing")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("handlers built without an event producer:\n  %s\n\n"+
			"A missing wiring line compiles, runs, and silently stops the events. If the "+
			"silence is deliberate, say so in skipWiring.",
			strings.Join(missing, "\n  "))
	}
	t.Logf("checked %d handler(s) built from %d emitting type(s)", checked, len(emitters))
}

// builtEmitters returns variable name → constructor for every assignment in fn
// that produces a handler from an emitting package.
//
// The app almost never calls those constructors directly: it goes through
// wire.ProvideXxxHandler. So a provider is resolved to the package it returns
// (providerReturns, read from internal/wire), and both spellings count. The
// first version of this test only understood the direct form and silently
// checked two handlers out of ten — a pass that meant nothing.
func builtEmitters(fn *ast.FuncDecl, emitters map[string]bool, providers map[string]string, aliases map[string]string) map[string]string {
	out := map[string]string{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) == 0 || len(as.Rhs) != 1 {
			return true
		}
		call, ok := as.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		// A direct constructor call: New<Type> for any emitting type in that
		// package. NewHandler is the convention for the handlers, but the
		// worker's BucketReconciler is built as worker.NewBucketReconciler,
		// and matching only NewHandler left it unchecked — the one line out
		// of ten the first full mutation sweep did not catch.
		direct := strings.HasPrefix(sel.Sel.Name, "New") &&
			emitters[aliases[pkg.Name]+"."+strings.TrimPrefix(sel.Sel.Name, "New")]
		viaProvider := pkg.Name == "wire" && providers[sel.Sel.Name] != ""
		if !direct && !viaProvider {
			return true
		}
		lhs, ok := as.Lhs[0].(*ast.Ident)
		if !ok || lhs.Name == "_" {
			return true
		}
		out[lhs.Name] = pkg.Name + "." + sel.Sel.Name
		return true
	})
	return out
}

// wiredIn returns the set of receivers with a SetEventProducer call in fn.
func wiredIn(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "SetEventProducer" {
			return true
		}
		if recv, ok := sel.X.(*ast.Ident); ok {
			out[recv.Name] = true
		}
		return true
	})
	return out
}
