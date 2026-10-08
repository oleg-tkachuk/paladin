package contract

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestAdminAndIAMCapabilityReaders pins why TestDataPlaneRPCAssertsCapabilityOp
// gates the data plane only, so that a change undoing the reason fails here
// rather than silently widening what a capability can reach.
//
// The reason has two halves. Wiring: no capability can carry an admin or iam
// request on its own — iam mounts no capability interceptor, and admin mounts
// the additive one behind a JWT gate that does not step aside for
// capabilities. And handlers: outside the data plane, a capability on the
// context grants authority in exactly the places listed in capabilityReaders,
// each of which checks the op it needs itself.
func TestAdminAndIAMCapabilityReaders(t *testing.T) {
	t.Parallel()

	t.Run("wiring", func(t *testing.T) {
		t.Parallel()
		admin := readSource(t, "internal", "app", "build_listeners_admin.go")
		for _, forbidden := range []string{establishingCapInterceptor, capabilitySkippingJWTGate} {
			if strings.Contains(admin, forbidden) {
				t.Errorf("the admin plane now uses %s: a capability can carry an admin request alone, "+
					"so admin RPCs need the AssertCapabilityOp gate too", forbidden)
			}
		}
		iam := iamInterceptorBlock(t, readSource(t, "internal", "app", "build_listeners_api.go"))
		if lower := strings.ToLower(iam); strings.Contains(lower, capabilityWordStem) || strings.Contains(lower, capDataVar) {
			t.Errorf("the iam interceptor chain mentions a capability; iam RPCs would need the " +
				"AssertCapabilityOp gate too")
		}
	})

	t.Run("handlers", func(t *testing.T) {
		t.Parallel()
		got := capabilityReadersOutsideDataPlane(t)
		var unexpected []string
		for _, r := range got {
			if _, ok := capabilityReaders[r]; !ok {
				unexpected = append(unexpected, r)
			}
		}
		sort.Strings(unexpected)
		for _, r := range unexpected {
			t.Errorf("%s reads the capability on the context outside the data plane; decide whether "+
				"it checks the op it grants, and add it to capabilityReaders with the reason", r)
		}
		seen := map[string]bool{}
		for _, r := range got {
			seen[r] = true
		}
		for r := range capabilityReaders {
			if !seen[r] {
				t.Errorf("capabilityReaders lists %s, which no longer reads the capability — remove it", r)
			}
		}
	})
}

const (
	// establishingCapInterceptor lets a capability alone authenticate a call.
	establishingCapInterceptor = "CapabilityEstablishingInterceptor"
	// capabilitySkippingJWTGate lets a capability-only request past the JWT gate.
	capabilitySkippingJWTGate = "InterceptorSkipTokensAndCapabilities"
	// iamServerDecl opens the iam plane's interceptor chain: the plane's one
	// server, built with its interceptors.
	iamServerDecl = "iamServer := connect.NewServer("
	// serverBlockEnd closes a top-level connect.NewServer(...) call.
	serverBlockEnd = "\n\t)\n"
	// capabilityWordStem and capDataVar, lower-cased, would appear in the iam
	// chain if a capability interceptor were mounted there — by constructor
	// name, or as the data plane's variable reused.
	capabilityWordStem = "capabilit"
	capDataVar         = "capdata"
	// capabilityFromContext is how a handler reads a capability off the context.
	capabilityFromContext = "CapabilityFromContext"
)

// capabilityReaders lists functions outside the data plane that read a
// capability off the context, as "dir.Type.Method" relative to internal/api,
// with why that is safe without AssertCapabilityOp.
var capabilityReaders = map[string]string{
	"admin/v1/capabilityh.Handler.Delegate": "the agent share path: requires OpShare and that " +
		"parent_id is the caller's own capability, checked in the handler",
}

func readSource(t *testing.T, parts ...string) string {
	t.Helper()
	src, err := os.ReadFile(repoPath(t, parts...)) // #nosec G304 — repo-local path
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(parts...), err)
	}
	return string(src)
}

func iamInterceptorBlock(t *testing.T, src string) string {
	t.Helper()
	start := strings.Index(src, iamServerDecl)
	if start < 0 {
		t.Fatalf("could not find %q — the iam wiring moved; update this gate", iamServerDecl)
	}
	end := strings.Index(src[start:], serverBlockEnd)
	if end < 0 {
		t.Fatalf("could not find the end of the iam interceptor chain")
	}
	return src[start : start+end]
}

// capabilityReadersOutsideDataPlane returns every method under internal/api,
// data plane excluded, whose body calls auth.CapabilityFromContext.
func capabilityReadersOutsideDataPlane(t *testing.T) []string {
	t.Helper()
	root := repoPath(t, "internal", "api")
	dataPlane := []string{
		filepath.Join(root, "data") + string(filepath.Separator),
		filepath.Join(root, "connectshim", "data") + string(filepath.Separator),
	}
	var out []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		for _, prefix := range dataPlane {
			if strings.HasPrefix(path, prefix) {
				return nil
			}
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !callsSelector(fn.Body, authPackage, capabilityFromContext) {
				continue
			}
			name := fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				name = receiverType(fn) + "." + name
			}
			out = append(out, fmt.Sprintf("%s.%s", filepath.ToSlash(rel), name))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/api: %v", err)
	}
	return out
}

func callsSelector(body *ast.BlockStmt, pkg, fn string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == pkg && sel.Sel.Name == fn {
				found = true
			}
		}
		return !found
	})
	return found
}
