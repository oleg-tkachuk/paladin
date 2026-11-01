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
// The reason has two halves. Wiring: no capability of its own can carry an
// admin or iam request — both planes mount the additive interceptor, which
// establishes a principal only for a delegated capability (ADR-0041), and a
// delegated capability stands for its user, whom Cedar and the write-scope
// interceptor decide on as for the user's own token. So the JWT gates may
// step aside for a capability only where the interceptor behind them accepts
// delegation. And handlers: outside the data plane, a capability on the
// context grants authority in exactly the places listed in capabilityReaders,
// each of which checks the op it needs itself.
func TestAdminAndIAMCapabilityReaders(t *testing.T) {
	t.Parallel()

	t.Run("wiring", func(t *testing.T) {
		t.Parallel()
		admin := readSource(t, "internal", "app", "build_listeners_admin.go")
		if strings.Contains(admin, establishingCapInterceptor) {
			t.Errorf("the admin plane now uses %s: a capability of its own can carry an admin request, "+
				"so admin RPCs need the AssertCapabilityOp gate too", establishingCapInterceptor)
		}
		if strings.Contains(admin, capabilitySkippingJWTGate) && !strings.Contains(admin, delegationOption) {
			t.Errorf("the admin JWT gate steps aside for a capability, and no interceptor behind it "+
				"accepts delegation (%s): a capability-only call would reach nothing that names its caller", delegationOption)
		}
		api := readSource(t, "internal", "app", "build_listeners_api.go")
		iam := iamCapabilityInterceptor(t, api)
		if strings.Contains(iam, establishingCapInterceptor) || !strings.Contains(iam, delegationOption) {
			t.Errorf("the iam capability interceptor must be the additive one, accepting delegation alone; got:\n%s", iam)
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
	// delegationOption is the wiring that makes a capability interceptor
	// accept delegated capabilities as their users (ADR-0041).
	delegationOption = "delegationOption("
	// iamCapDecl and iamCapEnd bound the iam plane's capability interceptor
	// as the wiring builds it.
	iamCapDecl = "capIAM = auth."
	iamCapEnd  = "\n\t\t)\n"
	// capabilityFromContext and verifiedCapabilityFromContext are how a
	// handler reads a capability off the context: as granted, and as the
	// verifier accepted it.
	capabilityFromContext         = "CapabilityFromContext"
	verifiedCapabilityFromContext = "VerifiedCapabilityFromContext"
)

// capabilityReaders lists functions outside the data plane that read a
// capability off the context, as "dir.Type.Method" relative to internal/api,
// with why that is safe without AssertCapabilityOp.
var capabilityReaders = map[string]string{
	"admin/v1/capabilityh.Handler.Delegate": "the agent share path: requires OpShare and that " +
		"parent_id is the caller's own capability, checked in the handler",
	"admin/v1/capabilityh.Handler.leaseTarget": "the agent lease path: requires OpShare, refuses a " +
		"Biscuit, and admits only the caller's own capability or one delegated from it, read from " +
		"the store's lineage, in the caller's own tenant",
}

func readSource(t *testing.T, parts ...string) string {
	t.Helper()
	src, err := os.ReadFile(repoPath(t, parts...)) // #nosec G304 — repo-local path
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(parts...), err)
	}
	return string(src)
}

// iamCapabilityInterceptor is the iam plane's capability interceptor as the
// wiring builds it.
func iamCapabilityInterceptor(t *testing.T, src string) string {
	t.Helper()
	start := strings.Index(src, iamCapDecl)
	if start < 0 {
		t.Fatalf("could not find %q — the iam wiring moved; update this gate", iamCapDecl)
	}
	end := strings.Index(src[start:], iamCapEnd)
	if end < 0 {
		t.Fatalf("could not find the end of the iam capability interceptor")
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
			if !ok || fn.Body == nil || !readsCapability(fn.Body) {
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

// readsCapability reports whether body reads a capability off the context,
// in either form.
func readsCapability(body *ast.BlockStmt) bool {
	return callsSelector(body, authPackage, capabilityFromContext) ||
		callsSelector(body, authPackage, verifiedCapabilityFromContext)
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
