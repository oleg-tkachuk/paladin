package connectshim_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

// unimplementedPrefix and handlerSuffix spell the embedded field every Connect
// server type carries: paladinadminv1connect.UnimplementedXServiceHandler.
const (
	unimplementedPrefix = "Unimplemented"
	handlerSuffix       = "Handler"
)

// handlerTypeName is the type a shim's interface-typed field stands for: the
// handler packages each name theirs Handler.
const handlerTypeName = "Handler"

// goModFile names the file the module path is read from.
const goModFile = "go.mod"

// generatedPathSegment marks the import path of a package protoc generated:
// an RPC's request is one of its messages.
const generatedPathSegment = "/sdk/go/gen/"

// rpcParams is how many parameters a unary Connect handler method takes: the
// context and the request.
const rpcParams = 2

// rpcGateAllowlist names the RPCs a server serves with no gate of its own and
// none behind it, as the server's directory under internal/api, then
// Type.Method. Each entry says why it needs none.
var rpcGateAllowlist = map[string]string{
	"connectshim/iam.AuthServer.Login":                 "pre-authentication: mints the token",
	"connectshim/iam.AuthServer.RefreshToken":          "pre-authentication: exchanges a refresh token the caller holds",
	"connectshim/iam.AuthServer.Revoke":                "revokes by token; the caller proves possession",
	"connectshim/iam.AuthServer.ExchangeAudience":      "derives a per-audience token from the caller's refresh token; authh.assertAudienceAllowed decides",
	"connectshim/iam.AuthServer.WhoAmI":                "self-introspection: the caller's own principal",
	"connectshim/iam.AuthServer.ChangePassword":        "the caller's own password, behind the old one",
	"connectshim/iam.AuthServer.ListMyMemberships":     "self-scoped: the caller's own tenant memberships",
	"connectshim/iam.AuthServer.SwitchTenant":          "self-scoped: refused without the caller's own non-disabled membership in the target",
	"connectshim/iam.UserSettingsServer.GetMine":       "self-service: the caller's own settings",
	"connectshim/iam.UserSettingsServer.UpdateMine":    "self-service: the caller's own settings",
	"connectshim/iam.SystemServer.GetVersion":          "build metadata; no tenant data",
	"connectshim/iam.SystemServer.GetHealth":           "this pod's health checks; no tenant data",
	"connectshim/admin.SystemServer.GetPlatformHealth": "every role's health; no tenant data, open to any session as iam HealthService.GetHealth is",
	"admin/v1/celh.Handler.Validate":                   "stateless CEL syntax check of the caller's own text; reads no tenant data",
	"admin/v1/apitokenh.Handler.GetSelf":               "self-introspection: the API token the caller authenticated with",
	"unary/unarytest.Probe.GetVersion":                 "test probe; no listener registers it",
}

// fieldRef is the declared type of a server struct's field: pkg is the import
// alias it is qualified by, "" for a type of the shim's own package.
type fieldRef struct {
	pkg      string
	typeName string
}

// goFunc is one function or method: its body's source, and for a method the
// type it is declared on.
type goFunc struct {
	recv string
	body string
	decl *ast.FuncDecl
}

// goPackage is the parsed non-test source of one directory.
type goPackage struct {
	dir  string
	fset *token.FileSet
	// servers are the types that embed an Unimplemented*Handler or serve an RPC.
	servers map[string]struct{}
	// funcs is keyed by "Type.Method" for a method and by the bare name for
	// a function.
	funcs map[string]goFunc
	// fields maps a struct type to its named fields' types.
	fields map[string]map[string]fieldRef
	// imports maps an import alias to its path.
	imports map[string]string
	// wired maps a struct type to the concrete types its constructors put in
	// its fields, where a field is declared as an interface.
	wired map[string]map[string]fieldRef
}

// sourceTree is every package under internal/api, by directory, and how an
// import path names one.
type sourceTree struct {
	module   string
	repoRoot string
	pkgs     map[string]*goPackage
}

// TestEveryShimRPCIsGated holds every exported RPC method of every Connect
// server type under internal/api — the connectshim servers and the handlers
// registered directly — to a gate: a marker in its own body, in a same-package helper it
// calls, or in the handler method it forwards to and that method's helpers.
// Unlike TestEveryShimMethodHitsAGatedHandler it also sees a server that does
// the work itself and forwards nowhere.
func TestEveryShimRPCIsGated(t *testing.T) {
	tree := loadSourceTree(t)
	var ungated []string
	seen := 0
	for _, pkg := range tree.shimPackages() {
		for key, fn := range pkg.funcs {
			if _, ok := pkg.servers[fn.recv]; !ok || !pkg.isRPC(fn.decl) {
				continue
			}
			seen++
			if _, ok := rpcGateAllowlist[tree.qualified(pkg, key)]; ok {
				continue
			}
			if !tree.gated(pkg, key, map[string]bool{}) {
				pos := pkg.fset.Position(fn.decl.Pos())
				rel, _ := filepath.Rel(tree.repoRoot, pos.Filename)
				ungated = append(ungated, tree.qualified(pkg, key)+" ("+rel+":"+strconv.Itoa(pos.Line)+")")
			}
		}
	}
	if seen == 0 {
		t.Fatal("no connectshim RPC methods found — parser broken?")
	}
	sort.Strings(ungated)
	if len(ungated) > 0 {
		t.Fatalf("connectshim RPCs with no gate in their body, their helpers or the handler "+
			"they forward to. Gate them, or add them to rpcGateAllowlist with a reason:\n  %s",
			strings.Join(ungated, "\n  "))
	}
}

// TestRPCGateAllowlistNamesRealRPCs keeps the allowlist from outliving the
// RPCs it excuses.
func TestRPCGateAllowlistNamesRealRPCs(t *testing.T) {
	tree := loadSourceTree(t)
	found := map[string]bool{}
	for _, pkg := range tree.shimPackages() {
		for key, fn := range pkg.funcs {
			if _, ok := pkg.servers[fn.recv]; ok && pkg.isRPC(fn.decl) {
				found[tree.qualified(pkg, key)] = true
			}
		}
	}
	for key := range rpcGateAllowlist {
		if !found[key] {
			t.Errorf("rpcGateAllowlist names %s, which no connectshim server serves", key)
		}
	}
}

func loadSourceTree(t *testing.T) *sourceTree {
	t.Helper()
	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	tree := &sourceTree{module: readModule(t, filepath.Join(repoRoot, goModFile)), repoRoot: repoRoot, pkgs: map[string]*goPackage{}}
	apiDir := tree.apiDir()
	err = filepath.WalkDir(apiDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		if pkg := parseGoPackage(t, path); len(pkg.funcs) > 0 {
			tree.pkgs[path] = pkg
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", apiDir, err)
	}
	return tree
}

func readModule(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	module := modfile.ModulePath(data)
	if module == "" {
		t.Fatalf("%s names no module", path)
	}
	return module
}

// qualified is key as the allowlist spells it: pkg's directory under
// internal/api, then key.
func (tr *sourceTree) qualified(pkg *goPackage, key string) string {
	rel, _ := filepath.Rel(tr.apiDir(), pkg.dir)
	return filepath.ToSlash(rel) + "." + key
}

func (tr *sourceTree) apiDir() string { return filepath.Join(tr.repoRoot, "internal", "api") }

// shimPackages are the packages that declare at least one server type.
func (tr *sourceTree) shimPackages() []*goPackage {
	var out []*goPackage
	for _, pkg := range tr.pkgs {
		if len(pkg.servers) > 0 {
			out = append(out, pkg)
		}
	}
	return out
}

// gated reports whether key in pkg carries a gate marker, or calls a function
// of its package, a method of its receiver, or a method on one of its
// receiver's fields that does.
func (tr *sourceTree) gated(pkg *goPackage, key string, visiting map[string]bool) bool {
	visitKey := pkg.dir + "#" + key
	if visiting[visitKey] {
		return false
	}
	visiting[visitKey] = true
	fn, ok := pkg.funcs[key]
	if !ok {
		return false
	}
	if anyGate(fn.body) {
		return true
	}
	recvName := ""
	if r := fn.decl.Recv; r != nil && len(r.List) == 1 && len(r.List[0].Names) == 1 {
		recvName = r.List[0].Names[0].Name
	}
	result := false
	ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
		if result {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok {
			result = tr.callGated(pkg, fn.recv, recvName, call, visiting)
		}
		return !result
	})
	return result
}

// callGated reports whether call reaches a gated function: one of pkg's own,
// one on the receiver, or one on a field of the receiver.
func (tr *sourceTree) callGated(pkg *goPackage, recvType, recvName string, call *ast.CallExpr, visiting map[string]bool) bool {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return tr.gated(pkg, f.Name, visiting)
	case *ast.SelectorExpr:
		switch x := f.X.(type) {
		case *ast.Ident:
			if recvName != "" && x.Name == recvName {
				return tr.gated(pkg, recvType+"."+f.Sel.Name, visiting)
			}
		case *ast.SelectorExpr:
			base, ok := x.X.(*ast.Ident)
			if !ok || recvName == "" || base.Name != recvName {
				return false
			}
			// The concrete type the constructor puts in the field, when it
			// says; else the field's declared type.
			ref, ok := pkg.wired[recvType][x.Sel.Name]
			if !ok {
				ref, ok = pkg.fields[recvType][x.Sel.Name]
			}
			if !ok {
				return false
			}
			return tr.fieldMethodGated(pkg, ref, f.Sel.Name, visiting)
		}
	}
	return false
}

// fieldMethodGated reports whether method on a field of type ref is gated. A
// concrete type is looked up where it is declared. A field of an interface the
// shim declares stands for the handler type named after it — versionHandler
// for objecth.VersionHandler — or else for a handler package's Handler; any
// one of those may serve it.
func (tr *sourceTree) fieldMethodGated(pkg *goPackage, ref fieldRef, method string, visiting map[string]bool) bool {
	if ref.pkg != "" {
		target := tr.pkgByImport(pkg.imports[ref.pkg])
		return target != nil && tr.gated(target, ref.typeName+"."+method, visiting)
	}
	if _, isStruct := pkg.fields[ref.typeName]; isStruct {
		return tr.gated(pkg, ref.typeName+"."+method, visiting)
	}
	candidates := []string{handlerTypeName}
	if ref.typeName != "" {
		candidates = append([]string{strings.ToUpper(ref.typeName[:1]) + ref.typeName[1:]}, candidates...)
	}
	for _, typeName := range candidates {
		for _, target := range tr.pkgs {
			if target != pkg && tr.gated(target, typeName+"."+method, visiting) {
				return true
			}
		}
	}
	return false
}

func (tr *sourceTree) pkgByImport(path string) *goPackage {
	rel, ok := strings.CutPrefix(path, tr.module+"/")
	if !ok {
		return nil
	}
	return tr.pkgs[filepath.Join(tr.repoRoot, filepath.FromSlash(rel))]
}

func parseGoPackage(t *testing.T, dir string) *goPackage {
	t.Helper()
	pkg := &goPackage{
		dir: dir, fset: token.NewFileSet(),
		servers: map[string]struct{}{}, funcs: map[string]goFunc{},
		fields: map[string]map[string]fieldRef{}, imports: map[string]string{},
		wired: map[string]map[string]fieldRef{},
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		file, err := parser.ParseFile(pkg.fset, path, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		pkg.collect(file, src)
	}
	for _, fn := range pkg.funcs {
		if fn.recv == "" {
			pkg.collectWiring(fn.decl)
		}
	}
	return pkg
}

// collectWiring records which parameter of constructor d goes into which
// field of the struct it builds: `&T{H: h}`, or `s.F = f` on a value built so,
// where the parameter is of a type another package declares.
func (p *goPackage) collectWiring(d *ast.FuncDecl) {
	params := map[string]fieldRef{}
	for _, f := range d.Type.Params.List {
		ref := typeRef(f.Type)
		if ref.pkg == "" {
			continue
		}
		for _, n := range f.Names {
			params[n.Name] = ref
		}
	}
	if len(params) == 0 {
		return
	}
	wire := func(typeName, field string, value ast.Expr) {
		v, ok := value.(*ast.Ident)
		if !ok {
			return
		}
		ref, ok := params[v.Name]
		if !ok {
			return
		}
		if p.wired[typeName] == nil {
			p.wired[typeName] = map[string]fieldRef{}
		}
		p.wired[typeName][field] = ref
	}
	built := map[string]string{} // local variable → the struct type it holds
	ast.Inspect(d.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			t, ok := x.Type.(*ast.Ident)
			if !ok {
				return true
			}
			for _, elt := range x.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if key, ok := kv.Key.(*ast.Ident); ok {
						wire(t.Name, key.Name, kv.Value)
					}
				}
			}
		case *ast.AssignStmt:
			if len(x.Lhs) != 1 || len(x.Rhs) != 1 {
				return true
			}
			if lhs, ok := x.Lhs[0].(*ast.Ident); ok {
				if t := compositeType(x.Rhs[0]); t != "" {
					built[lhs.Name] = t
				}
				return true
			}
			if sel, ok := x.Lhs[0].(*ast.SelectorExpr); ok {
				if base, ok := sel.X.(*ast.Ident); ok && built[base.Name] != "" {
					wire(built[base.Name], sel.Sel.Name, x.Rhs[0])
				}
			}
		}
		return true
	})
}

// compositeType is the struct type e builds, `T{…}` or `&T{…}`, or "".
func compositeType(e ast.Expr) string {
	if u, ok := e.(*ast.UnaryExpr); ok {
		e = u.X
	}
	if c, ok := e.(*ast.CompositeLit); ok {
		if t, ok := c.Type.(*ast.Ident); ok {
			return t.Name
		}
	}
	return ""
}

// collect records file's imports, server types, struct fields and functions.
func (p *goPackage) collect(file *ast.File, src []byte) {
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		alias := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		p.imports[alias] = path
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					p.collectType(ts)
				}
			}
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			body := string(src[p.fset.Position(d.Body.Pos()).Offset:p.fset.Position(d.Body.End()).Offset])
			key, recv := d.Name.Name, ""
			if d.Recv != nil && len(d.Recv.List) == 1 {
				recv = receiverType(d.Recv.List[0].Type)
				key = recv + "." + d.Name.Name
			}
			p.funcs[key] = goFunc{recv: recv, body: body, decl: d}
			if p.isRPC(d) {
				p.servers[recv] = struct{}{}
			}
		}
	}
}

func (p *goPackage) collectType(ts *ast.TypeSpec) {
	st, ok := ts.Type.(*ast.StructType)
	if !ok {
		return
	}
	fields := map[string]fieldRef{}
	for _, f := range st.Fields.List {
		ref := typeRef(f.Type)
		if len(f.Names) == 0 {
			if strings.HasPrefix(ref.typeName, unimplementedPrefix) && strings.HasSuffix(ref.typeName, handlerSuffix) {
				p.servers[ts.Name.Name] = struct{}{}
			}
			continue
		}
		for _, n := range f.Names {
			fields[n.Name] = ref
		}
	}
	p.fields[ts.Name.Name] = fields
}

// typeRef is the named type e spells, through any pointer.
func typeRef(e ast.Expr) fieldRef {
	switch x := e.(type) {
	case *ast.StarExpr:
		return typeRef(x.X)
	case *ast.Ident:
		return fieldRef{typeName: x.Name}
	case *ast.SelectorExpr:
		if pkg, ok := x.X.(*ast.Ident); ok {
			return fieldRef{pkg: pkg.Name, typeName: x.Sel.Name}
		}
	}
	return fieldRef{}
}

// isRPC reports whether d has an RPC's shape: an exported method taking a
// context and a generated request message.
func (p *goPackage) isRPC(d *ast.FuncDecl) bool {
	if !d.Name.IsExported() || d.Recv == nil || d.Type.Params == nil {
		return false
	}
	params := d.Type.Params.List
	count := 0
	for _, f := range params {
		count += max(len(f.Names), 1)
	}
	if count != rpcParams || len(params) != rpcParams {
		return false
	}
	if sel, ok := params[0].Type.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Context" {
		return false
	}
	star, ok := params[1].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	ref := typeRef(star.X)
	return ref.pkg != "" && strings.Contains(p.imports[ref.pkg], generatedPathSegment)
}

func receiverType(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return receiverType(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return receiverType(x.X)
	}
	return ""
}
