// Package connectshim_test asserts that every connectshim method funnels
// through a handler method that gates the request — either via Cedar
// authorization (h.policy.IsAuthorized / h.authorize / h.authorizeInspect)
// or via an explicit role/tenant guard (requireRole / RequireRole /
// requirePlatformAdmin / hasPlatformAdmin / isPlatformAdmin / RequireAnyRole).
//
// This catches the failure mode where a new RPC is added to a connectshim
// but the underlying handler method skips both layers — bypassing Cedar
// gating entirely. Drift on this is silent; the test is the only signal.
//
// The allowlist exists for pre-authentication flows (Login / RefreshToken)
// and intentionally-open helpers (WhoAmI / ChangePassword over self).
package connectshim_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// gateMarkers — substrings that count as "this method is gated". A handler
// method whose body contains any one of these passes coverage.
var gateMarkers = []string{
	"h.policy.IsAuthorized",
	"h.engine.IsAuthorized",
	"h.authorize",
	"h.authorizeInspect",
	"requireRole",
	"requireAnyRole",
	"requirePlatformAdmin",
	"hasPlatformAdmin",
	"isPlatformAdmin",
	"apiutil.RequireRole",
	"apiutil.RequireAnyRole",
	"apiutil.HasRole",
}

// preAuthnAllowlist — handler methods that intentionally run before any
// principal is established. Adding a new RPC here requires explicit
// justification in code review.
var preAuthnAllowlist = map[string]struct{}{
	"Login":          {}, // mints the token
	"RefreshToken":   {}, // exchanges refresh for access
	"Revoke":         {}, // revokes by token (caller proves possession)
	"WhoAmI":         {}, // self-introspection; tenant comes from context
	"ChangePassword": {}, // operates on the caller's own password
	"ValidatePolicy": {}, // gated, but intentionally syntactic-only
}

// shimMethodCallRE matches `s.H.<MethodName>(` — the only way a shim
// reaches a handler.
var shimMethodCallRE = regexp.MustCompile(`\bs\.H\.([A-Z][A-Za-z0-9_]+)\(`)

// handlerMethodRE matches a handler-method definition body. We capture the
// method name and rely on Go's `}` block-closure to bound the body.
var handlerMethodRE = regexp.MustCompile(`(?m)^func\s+\(\s*[a-z]\s+\*Handler\s*\)\s+([A-Z][A-Za-z0-9_]+)\b`)

func TestEveryShimMethodHitsAGatedHandler(t *testing.T) {
	repoRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	shimDir := filepath.Join(repoRoot, "internal", "api", "connectshim")

	// 1. Collect every method name reached from any shim file.
	called := map[string]struct{}{}
	walkErr := filepath.Walk(shimDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range shimMethodCallRE.FindAllStringSubmatch(string(body), -1) {
			called[m[1]] = struct{}{}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk shims: %v", walkErr)
	}
	if len(called) == 0 {
		t.Fatal("no s.H.<Method> calls discovered — regex broken?")
	}

	// 2. For each called name, find a handler definition somewhere in the
	//    api tree and check its body has at least one gate marker.
	apiDir := filepath.Join(repoRoot, "internal", "api")
	bodies := collectHandlerBodies(t, apiDir)

	var ungated []string
	names := make([]string, 0, len(called))
	for n := range called {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := preAuthnAllowlist[name]; ok {
			continue
		}
		body, found := bodies[name]
		if !found {
			// Method is shim-internal (e.g. helper that doesn't exist on a
			// Handler — happens for methods on subordinate structs). Skip;
			// this test only covers Handler-method gating.
			continue
		}
		gated := false
		for _, marker := range gateMarkers {
			if strings.Contains(body, marker) {
				gated = true
				break
			}
		}
		if !gated {
			ungated = append(ungated, name)
		}
	}

	if len(ungated) > 0 {
		t.Fatalf("connectshim methods reach handler methods that lack any gate "+
			"(neither Cedar authorize nor role check). Add gating or extend the "+
			"allowlist with explicit justification:\n  %s",
			strings.Join(ungated, "\n  "))
	}
}

// collectHandlerBodies walks every .go file under apiDir and returns
// methodName → body for each `func (h *Handler) Name(...) { ... }` it
// finds. Bodies are parsed by counting matched braces, which is
// approximate but plenty for our gate-keyword scan.
func collectHandlerBodies(t *testing.T, apiDir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	walkErr := filepath.Walk(apiDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Skip the shim package itself — we only want target handler bodies.
		if strings.Contains(path, "/connectshim/") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(raw)
		for _, m := range handlerMethodRE.FindAllStringSubmatchIndex(src, -1) {
			name := src[m[2]:m[3]]
			body := extractBody(src[m[1]:])
			// First definition wins — duplicates across packages are
			// reasonable (different Handler types share method names).
			// We don't try to disambiguate; the gate-keyword scan only
			// needs to find *one* gated definition to pass coverage.
			if existing, ok := out[name]; ok {
				// If the existing body lacks any gate marker but the new
				// one has one, prefer the new one. This avoids the test
				// failing on a no-op stub when a real gated impl exists.
				if !anyGate(existing) && anyGate(body) {
					out[name] = body
				}
				continue
			}
			out[name] = body
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk handlers: %v", walkErr)
	}
	return out
}

// extractBody walks the runes after a `func (...)` signature, finds the
// first `{`, then returns the slice up to its matching `}`. Tracks nesting
// only — does NOT respect strings/comments. Sufficient for our keyword
// scan since gate markers are unique tokens unlikely to live inside string
// literals.
func extractBody(s string) string {
	open := strings.IndexByte(s, '{')
	if open < 0 {
		return ""
	}
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[open : i+1]
			}
		}
	}
	return s[open:]
}

func anyGate(body string) bool {
	for _, m := range gateMarkers {
		if strings.Contains(body, m) {
			return true
		}
	}
	return false
}
