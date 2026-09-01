package api_test

// The guard behind `option idempotency_level = NO_SIDE_EFFECTS`.
//
// That option is a WIRE-level promise: buf classifies a change to it as
// breaking, and gRPC intermediaries read it to decide whether they may retry a
// call on their own. Declaring it on an RPC that writes is therefore not a
// documentation slip — it invites a proxy to duplicate the write.
//
// Which is a live risk here, not a theoretical one. The proto comment on
// TestBackend says "Read-only"; the handler persists the probe result via
// SetHealth. A classification built from names, or from the existing comments,
// gets that wrong — and an earlier draft of this work did, twice over.
//
// So the audit runs on every build instead of once. For every RPC the
// descriptors declare NO_SIDE_EFFECTS, this parses the handler and fails if it
// reaches a write — directly, or through a helper in the same package.
//
// What it does NOT prove: writes reached through an interface whose
// implementation lives elsewhere, or more than one hop away. It is a tripwire
// for the common shape, not a proof of purity. The annotations it guards were
// each read by hand first; this keeps them true afterwards.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
)

// writeVerb matches a method name that persists something. Deliberately broad:
// a false positive costs one line in the exceptions list below and a moment's
// thought, while a false negative publishes a wrong wire contract.
var writeVerb = regexp.MustCompile(
	`^(Create|Insert|Update|Set|Delete|Touch|Record|Upsert|Mark|Purge|Revoke|Rotate|Bind|Clear|Increment|Reset|Save|Put|Add|Remove|Dispatch)`)

// noSideEffectRPCs collects every method the descriptors declare NO_SIDE_EFFECTS.
func noSideEffectRPCs(t *testing.T) []string {
	t.Helper()
	var out []string
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "paladin.") {
			return true
		}
		svcs := fd.Services()
		for i := 0; i < svcs.Len(); i++ {
			ms := svcs.Get(i).Methods()
			for j := 0; j < ms.Len(); j++ {
				m := ms.Get(j)
				opts, _ := m.Options().(*descriptorpb.MethodOptions)
				if opts.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS {
					out = append(out, string(m.Name()))
				}
			}
		}
		return true
	})
	return out
}

// handlerFuncs indexes every `func (h *Handler) Name(...)` in internal/api by
// name, together with the file's other top-level funcs so one hop of helper
// calls can be followed.
type pkgIndex struct {
	methods map[string]*ast.FuncDecl // Handler methods by name
	helpers map[string]*ast.FuncDecl // package-level funcs and other methods
}

func indexHandlers(t *testing.T) map[string]*pkgIndex {
	t.Helper()
	byPkg := map[string]*pkgIndex{}
	files, err := filepath.Glob("v1/*/handler*.go")
	if err != nil {
		t.Fatal(err)
	}
	more, _ := filepath.Glob("*/v1/*/handler*.go")
	files = append(files, more...)
	if len(files) == 0 {
		t.Fatal("indexed no handler files — the glob is wrong, and an empty index " +
			"would make every assertion below vacuous")
	}
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		key := filepath.Dir(path)
		idx, ok := byPkg[key]
		if !ok {
			idx = &pkgIndex{methods: map[string]*ast.FuncDecl{}, helpers: map[string]*ast.FuncDecl{}}
			byPkg[key] = idx
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if fd.Recv != nil && isHandlerRecv(fd.Recv) {
				idx.methods[fd.Name.Name] = fd
			} else {
				idx.helpers[fd.Name.Name] = fd
			}
		}
	}
	return byPkg
}

// Any receiver whose type name ends in Handler, not only `*Handler`.
//
// ObjectService.GetVersion hangs off *VersionHandler, and the narrower version
// of this predicate skipped it in silence — the annotation would have been
// published with nothing checking it. That is the failure mode this whole file
// exists to prevent, reproduced inside the file itself.
func isHandlerRecv(fl *ast.FieldList) bool {
	if len(fl.List) == 0 {
		return false
	}
	star, ok := fl.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && strings.HasSuffix(id.Name, "Handler")
}

// writesIn reports the write-shaped calls reachable from fn, following one hop
// into same-package helpers and Handler methods.
func writesIn(fn *ast.FuncDecl, idx *pkgIndex, depth int) []string {
	var found []string
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			name := f.Sel.Name
			if writeVerb.MatchString(name) {
				found = append(found, name)
			}
			if depth > 0 {
				if m, ok := idx.methods[name]; ok {
					found = append(found, writesIn(m, idx, depth-1)...)
				}
			}
		case *ast.Ident:
			if depth > 0 {
				if h, ok := idx.helpers[f.Name]; ok {
					found = append(found, writesIn(h, idx, depth-1)...)
				}
			}
		}
		return true
	})
	return found
}

// Some write-shaped names are not writes. Each entry is a decision, not a
// silencer — if one stops being true the RPC's annotation is wrong.
var notReallyWrites = map[string]bool{
	"SetHeader":   true, // connect response metadata
	"SetTrailer":  true,
	"Set":         true, // http.Header.Set and friends
	"Add":         true,
	"MarshalJSON": true,
	"PutTx":       false, // deliberately NOT excused: a real write
}

func TestNoSideEffectRPCsDoNotWrite(t *testing.T) {
	declared := noSideEffectRPCs(t)
	if len(declared) == 0 {
		t.Skip("no RPC declares NO_SIDE_EFFECTS yet — nothing to guard")
	}
	idx := indexHandlers(t)

	checked := 0
	var unmatched []string
	for _, name := range declared {
		matched := false
		for _, pkg := range idx {
			fn, ok := pkg.methods[name]
			if !ok {
				continue
			}
			matched = true
			checked++
			var bad []string
			for _, w := range writesIn(fn, pkg, 1) {
				if notReallyWrites[w] {
					continue
				}
				bad = append(bad, w)
			}
			if len(bad) > 0 {
				t.Errorf("%s declares NO_SIDE_EFFECTS but its handler calls %v — "+
					"a proxy reading that option may retry it, duplicating the write",
					name, uniq(bad))
			}
		}
		if !matched {
			unmatched = append(unmatched, name)
		}
	}
	// An RPC whose handler this cannot find is UNGUARDED, and an unguarded
	// annotation is the thing being guarded against. Failing here rather than
	// passing quietly is the difference between a check and a decoration.
	if len(unmatched) > 0 {
		t.Errorf("declared NO_SIDE_EFFECTS but no handler found, so nothing "+
			"checked them: %v — either the index misses their receiver shape, or "+
			"they should not carry the annotation", uniq(unmatched))
	}
	// `checked` counts handler bodies, not RPCs: a method name that exists on
	// two services (GetOperation, List, Validate …) is verified on both, so
	// this can legitimately exceed len(declared).
	t.Logf("checked %d handler bodies for %d declared NO_SIDE_EFFECTS RPCs",
		checked, len(declared))
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ─── the IDEMPOTENT half ────────────────────────────────────────────────────

// NO_SIDE_EFFECTS is guarded above by reading the handler. IDEMPOTENT needs a
// different claim — "repeating this is safe" — and a different check, because
// nothing about a handler's shape proves it.
//
// What does prove it, for this API, is one of two structures:
//
//   - the request carries `resource_version`, so a repeat either writes the
//     same value or loses the OCC check and fails Aborted; or
//   - the RPC removes something, and removing what is already gone is a no-op.
//
// An RPC declared IDEMPOTENT with neither is either misannotated or relies on
// a third argument nobody wrote down. The exceptions map below is where that
// third argument goes, one line of reasoning each — not a silencer.
//
// This matters because IDEMPOTENT is a wire promise like the other: a proxy
// may retry on it. Declaring it on RotateCredentials — which an early draft of
// this work did — invites a retry that mints a second credential pair.
var idempotentByArgument = map[string]string{
	// name: why repeating is safe without OCC and without being a removal
	"EnsureTenantStorage":     "converges on a target state; the name is the contract",
	"BindCollectionToBucket":  "an upsert of one binding row keyed by (tenant, collection)",
	"SetTenantDefaultBinding": "upserts the single binding row for a tenant",
	"RegenerateUploadUrl":     "re-signs a URL for an EXISTING pending row; creates nothing",
	"PutObjectTags":           "a PUT replaces the whole tag set with the one supplied",
	"ResetUsage":              "zeroes a counter; zeroing twice lands on zero",
}

var removalVerb = regexp.MustCompile(`^(Delete|Clear|Abort|Cancel|Revoke|Purge|Remove)`)

func TestIdempotentRPCsAreActuallyRepeatable(t *testing.T) {
	var checked, declared int
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "paladin.") {
			return true
		}
		svcs := fd.Services()
		for i := 0; i < svcs.Len(); i++ {
			ms := svcs.Get(i).Methods()
			for j := 0; j < ms.Len(); j++ {
				m := ms.Get(j)
				opts, _ := m.Options().(*descriptorpb.MethodOptions)
				if opts.GetIdempotencyLevel() != descriptorpb.MethodOptions_IDEMPOTENT {
					continue
				}
				declared++
				name := string(m.Name())
				if removalVerb.MatchString(name) {
					checked++
					continue
				}
				if _, ok := idempotentByArgument[name]; ok {
					checked++
					continue
				}
				if hasField(m.Input(), "resource_version") {
					checked++
					continue
				}
				t.Errorf("%s.%s declares IDEMPOTENT but is neither a removal, nor "+
					"OCC-guarded (no resource_version on %s), nor listed in "+
					"idempotentByArgument with a reason — a proxy may retry it",
					svcs.Get(i).Name(), name, m.Input().Name())
			}
		}
		return true
	})
	if declared == 0 {
		t.Skip("no RPC declares IDEMPOTENT yet")
	}
	t.Logf("checked %d of %d declared IDEMPOTENT RPCs", checked, declared)
}

func hasField(md protoreflect.MessageDescriptor, name string) bool {
	fs := md.Fields()
	for i := 0; i < fs.Len(); i++ {
		if string(fs.Get(i).Name()) == name {
			return true
		}
	}
	return false
}
