package contract

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// TestDataPlaneRPCAssertsCapabilityOp proves that every RPC a capability can
// reach checks the operation it performs.
//
// A capability's per-operation restrictions — Caveats.Ops, resource prefixes
// and URIs, tainted-read, and the offline Biscuit attenuation folded into
// them — are enforced in exactly one place, auth.AssertCapabilityOp, and only
// when a handler calls it. The interceptor cannot do it: it does not know
// which operation an RPC performs or on what. So an RPC that never asserts
// lets a capability restricted to `get` call it anyway, and nothing fails.
//
// The check: for every data-plane RPC, follow the shim method to each handler
// method it delegates to, and require that every one of them calls
// auth.AssertCapabilityOp — directly, or through a helper in assertingHelpers.
// An RPC that legitimately does not is listed in exemptRPC with the reason.
//
// Only the data plane is gated, because only there can a capability carry the
// request. The iam plane mounts no capability interceptor, so no capability is
// ever on its context. The admin plane mounts the additive interceptor AFTER
// a JWT / role-bearing API-token gate that does not step aside for
// capabilities: a capability-only request is refused before any handler, and
// one presented alongside a principal cannot widen what that principal's
// roles and Cedar already allow. The one admin RPC that grants authority from
// a capability, CapabilityService.Delegate, checks OpShare itself.
// TestAdminAndIAMCapabilityReaders pins that surface so a new reader there
// is a decision, not an accident.
func TestDataPlaneRPCAssertsCapabilityOp(t *testing.T) {
	t.Parallel()

	shims := loadShimPackage(t, repoPath(t, "internal", "api", "connectshim", "data"))
	handlers := map[string]*goPackage{}
	handlerPkg := func(name string) *goPackage {
		if p, ok := handlers[name]; ok {
			return p
		}
		p := loadGoPackage(t, repoPath(t, "internal", "api", "data", "v1", name))
		handlers[name] = p
		return p
	}

	for _, h := range assertingHelpers {
		fn, ok := handlerPkg(h.pkg).methods[methodKey{h.typ, h.method}]
		if !ok {
			t.Errorf("assertingHelpers names %s.%s.%s, which does not exist", h.pkg, h.typ, h.method)
			continue
		}
		if !callsAssert(fn, nil) {
			t.Errorf("assertingHelpers names %s.%s.%s, which does not call auth.AssertCapabilityOp",
				h.pkg, h.typ, h.method)
		}
	}

	var missing []string
	checked := 0
	forEachDataRPC(t, func(svc protoreflect.ServiceDescriptor, rpc protoreflect.MethodDescriptor) {
		full := string(svc.Name()) + "." + string(rpc.Name())
		_, exempt := exemptRPC[full]

		shimType, ok := shims.serves[string(svc.Name())]
		if !ok {
			missing = append(missing, fmt.Sprintf("%s: no shim asserts it serves %s", full, svc.Name()))
			return
		}
		body, ok := shims.methods[methodKey{shimType, string(rpc.Name())}]
		if !ok {
			missing = append(missing, fmt.Sprintf("%s: %s has no method %s", full, shimType, rpc.Name()))
			return
		}
		targets, errs := shims.delegations(shimType, body)
		missing = append(missing, prefixAll(full, errs)...)
		if len(targets) == 0 && len(errs) == 0 {
			missing = append(missing, full+": delegates to no handler method, so nothing can assert")
			return
		}

		asserted := len(targets) > 0
		for _, tg := range targets {
			fn, ok := handlerPkg(tg.pkg).methods[methodKey{tg.typ, tg.method}]
			if !ok {
				missing = append(missing, fmt.Sprintf("%s: handler %s.%s.%s not found", full, tg.pkg, tg.typ, tg.method))
				asserted = false
				continue
			}
			if callsAssert(fn, helpersOf(tg.pkg, tg.typ)) {
				continue
			}
			asserted = false
			if !exempt {
				missing = append(missing, fmt.Sprintf(
					"%s: %s.%s.%s never calls auth.AssertCapabilityOp — a capability restricted to other operations can call it",
					full, tg.pkg, tg.typ, tg.method))
			}
		}
		if exempt && asserted {
			missing = append(missing, full+": listed in exemptRPC but asserts — remove the exemption")
		}
		checked++
	})

	if checked < minDataRPCs {
		t.Fatalf("checked %d data-plane RPCs, expected at least %d — the walk is broken and the gate proves nothing",
			checked, minDataRPCs)
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Error(m)
	}
	if len(missing) > 0 {
		t.Logf("\n%d problem(s). Call auth.AssertCapabilityOp in the handler, or — if the RPC "+
			"genuinely cannot be reached by a capability, or refuses one — add it to exemptRPC "+
			"with the reason.", len(missing))
	}
}

// minDataRPCs guards against a walk that silently visits nothing: the data
// plane served 37 RPCs when this gate was written.
const minDataRPCs = 37

// dataPlanePackage is the proto package whose services are mounted on the
// data plane, the one plane where a capability can establish a principal.
const dataPlanePackage = "paladin.data.v1"

// assertingHelpers are handler methods that call auth.AssertCapabilityOp on
// behalf of their caller. A call to one counts as an assertion, and the gate
// checks that each still does.
var assertingHelpers = []handlerMethod{
	{pkg: "objecth", typ: "VersionHandler", method: "authorizeParent"},
	{pkg: "objecth", typ: "LockHandler", method: "resolve"},
	{pkg: "batchh", typ: "Handler", method: "assertOnObjects"},
	{pkg: "batchh", typ: "Handler", method: "assertCopy"},
}

// exemptRPC lists data-plane RPCs that legitimately do not assert, keyed by
// "Service.Method". An entry is a claim that a capability holder calling the
// RPC gains nothing its restrictions should have stopped.
var exemptRPC = map[string]string{}

func helpersOf(pkg, typ string) map[string]bool {
	out := map[string]bool{}
	for _, h := range assertingHelpers {
		if h.pkg == pkg && h.typ == typ {
			out[h.method] = true
		}
	}
	return out
}

func prefixAll(prefix string, errs []string) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, prefix+": "+e)
	}
	return out
}

func forEachDataRPC(t *testing.T, fn func(protoreflect.ServiceDescriptor, protoreflect.MethodDescriptor)) {
	t.Helper()
	forEachRPC(t, func(svc protoreflect.ServiceDescriptor, rpc protoreflect.MethodDescriptor) {
		if string(svc.ParentFile().Package()) == dataPlanePackage {
			fn(svc, rpc)
		}
	})
}

// callsAssert reports whether fn calls auth.AssertCapabilityOp, or a method
// named in helpers on its own receiver.
func callsAssert(fn *ast.FuncDecl, helpers map[string]bool) bool {
	if fn.Body == nil {
		return false
	}
	recv := receiverName(fn)
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch {
		case x.Name == authPackage && sel.Sel.Name == assertFunc:
			found = true
		case recv != "" && x.Name == recv && helpers[sel.Sel.Name]:
			found = true
		}
		return !found
	})
	return found
}

const (
	authPackage = "auth"
	assertFunc  = "AssertCapabilityOp"
)

// ─── package model ──────────────────────────────────────────────────────────

type methodKey struct{ typ, method string }

type handlerMethod struct{ pkg, typ, method string }

// goPackage is one directory's non-test Go files, parsed but not type-checked:
// the gate needs which method calls which, not the types of the arguments.
type goPackage struct {
	methods map[methodKey]*ast.FuncDecl
	// structFields: type → field name → the field's type, as written.
	structFields map[string]map[string]ast.Expr
	// implements: local interface → the concrete handler type asserted to
	// implement it, from `var _ iface = (*pkg.T)(nil)`.
	implements map[string]handlerType
	// serves: Connect service name → shim type, from
	// `var _ xconnect.FooServiceHandler = (*FooServer)(nil)`.
	serves map[string]string
}

type handlerType struct{ pkg, typ string }

func loadGoPackage(t *testing.T, dir string) *goPackage {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	p := &goPackage{
		methods:      map[methodKey]*ast.FuncDecl{},
		structFields: map[string]map[string]ast.Expr{},
		implements:   map[string]handlerType{},
		serves:       map[string]string{},
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		p.index(f)
	}
	if len(p.methods) == 0 {
		t.Fatalf("%s: indexed no methods — the gate would pass vacuously", dir)
	}
	return p
}

func loadShimPackage(t *testing.T, dir string) *goPackage {
	t.Helper()
	p := loadGoPackage(t, dir)
	if len(p.serves) == 0 {
		t.Fatalf("%s: found no Connect handler assertions", dir)
	}
	return p
}

func (p *goPackage) index(f *ast.File) {
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil || len(d.Recv.List) == 0 {
				continue
			}
			if typ := receiverType(d); typ != "" {
				p.methods[methodKey{typ, d.Name.Name}] = d
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					st, ok := s.Type.(*ast.StructType)
					if !ok {
						continue
					}
					fields := map[string]ast.Expr{}
					for _, fld := range st.Fields.List {
						for _, n := range fld.Names {
							fields[n.Name] = fld.Type
						}
					}
					p.structFields[s.Name.Name] = fields
				case *ast.ValueSpec:
					p.indexAssertion(s)
				}
			}
		}
	}
}

// indexAssertion records `var _ Iface = (*T)(nil)` lines.
func (p *goPackage) indexAssertion(s *ast.ValueSpec) {
	if len(s.Names) != 1 || s.Names[0].Name != "_" || len(s.Values) != 1 || s.Type == nil {
		return
	}
	conv, ok := s.Values[0].(*ast.CallExpr)
	if !ok {
		return
	}
	paren, ok := conv.Fun.(*ast.ParenExpr)
	if !ok {
		return
	}
	star, ok := paren.X.(*ast.StarExpr)
	if !ok {
		return
	}
	switch iface := s.Type.(type) {
	case *ast.SelectorExpr:
		// xconnect.FooServiceHandler = (*FooServer)(nil)
		if id, ok := star.X.(*ast.Ident); ok && strings.HasSuffix(iface.Sel.Name, connectHandlerSuffix) {
			p.serves[strings.TrimSuffix(iface.Sel.Name, connectHandlerSuffix)] = id.Name
		}
	case *ast.Ident:
		// localIface = (*pkg.T)(nil)
		if sel, ok := star.X.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok {
				p.implements[iface.Name] = handlerType{pkg: pkg.Name, typ: sel.Sel.Name}
			}
		}
	}
}

// connectHandlerSuffix is what connect-go appends to a service name to name
// its server interface.
const connectHandlerSuffix = "Handler"

// delegations returns the handler methods a shim method calls through its
// receiver's fields: `s.H.GetObject(...)` → objecth.Handler.GetObject.
func (p *goPackage) delegations(shimType string, fn *ast.FuncDecl) ([]handlerMethod, []string) {
	recv := receiverName(fn)
	fields := p.structFields[shimType]
	var out []handlerMethod
	var errs []string
	seen := map[handlerMethod]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		inner, ok := sel.X.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := inner.X.(*ast.Ident); !ok || id.Name != recv {
			return true
		}
		fieldType, ok := fields[inner.Sel.Name]
		if !ok {
			return true
		}
		ht, err := p.resolveField(fieldType)
		if err != nil {
			errs = append(errs, fmt.Sprintf("field %s: %v", inner.Sel.Name, err))
			return true
		}
		hm := handlerMethod{pkg: ht.pkg, typ: ht.typ, method: sel.Sel.Name}
		if !seen[hm] {
			seen[hm] = true
			out = append(out, hm)
		}
		return true
	})
	return out, errs
}

// resolveField maps a shim field's declared type to the concrete handler
// type serving it: a seam interface through its `var _` assertion, or a
// pointer to the handler type itself.
func (p *goPackage) resolveField(e ast.Expr) (handlerType, error) {
	switch x := e.(type) {
	case *ast.Ident:
		if ht, ok := p.implements[x.Name]; ok {
			return ht, nil
		}
		return handlerType{}, fmt.Errorf("interface %s has no `var _ %s = (*pkg.T)(nil)` naming its handler", x.Name, x.Name)
	case *ast.StarExpr:
		if sel, ok := x.X.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok {
				return handlerType{pkg: pkg.Name, typ: sel.Sel.Name}, nil
			}
		}
	}
	return handlerType{}, fmt.Errorf("unrecognised field type %T", e)
}

func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 || len(fn.Recv.List[0].Names) == 0 {
		return ""
	}
	return fn.Recv.List[0].Names[0].Name
}

func receiverType(fn *ast.FuncDecl) string {
	e := fn.Recv.List[0].Type
	if s, ok := e.(*ast.StarExpr); ok {
		e = s.X
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
