//go:build integration

// Every RPC, enumerated from the protobuf descriptors rather than a list
// someone maintains by hand — a service added tomorrow is covered the day it
// is registered, and a service deleted stops being tested without anyone
// remembering to remove it.
//
// Three mutations are applied to each method, each pinning a property that
// should hold for the whole surface:
//
//  1. No credential                → Unauthenticated. Never OK, never Internal.
//  2. A credential for another     → Unauthenticated or PermissionDenied.
//     plane's audience
//  3. A valid credential, an empty → anything EXCEPT Internal/Unknown.
//     request body
//
// The third is the one that earns its keep. An empty request is malformed for
// almost every method, so the correct answer is InvalidArgument — a *typed*
// refusal. Internal or Unknown means the request reached the database and
// something there disagreed with the code: a column that no longer exists, a
// conflict target with no matching constraint, a uuid compared against text.
// Every such bug found in this codebase surfaced exactly that way, and each
// was found by accident. This turns the accident into a gate.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
)

// plane maps a protobuf package to the listener that serves it and the
// audience its tokens must carry.
type plane struct {
	pkgPrefix string
	baseURL   string
	audience  string
}

func planes() []plane {
	base := func(env, def string) string {
		if v := os.Getenv(env); v != "" {
			return v
		}
		return def
	}
	return []plane{
		{"paladin.admin.v1.", base("PALADIN_RPC_ADMIN_URL", "http://127.0.0.1:8090"), "paladin-admin"},
		{"paladin.data.v1.", base("PALADIN_RPC_DATA_URL", "http://127.0.0.1:8080"), "paladin-data"},
		{"paladin.iam.v1.", base("PALADIN_RPC_IAM_URL", "http://127.0.0.1:8085"), "paladin-iam"},
	}
}

// methodsFor walks the descriptor registry and returns every unary method
// whose service lives in this plane's package.
func methodsFor(p plane) []protoreflect.MethodDescriptor {
	var out []protoreflect.MethodDescriptor
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		svcs := fd.Services()
		for i := 0; i < svcs.Len(); i++ {
			svc := svcs.Get(i)
			if !strings.HasPrefix(string(svc.FullName()), p.pkgPrefix) {
				continue
			}
			ms := svc.Methods()
			for j := 0; j < ms.Len(); j++ {
				m := ms.Get(j)
				// Streaming methods need a framed body; the empty-request
				// mutation is a unary shape.
				if m.IsStreamingClient() || m.IsStreamingServer() {
					continue
				}
				out = append(out, m)
			}
		}
		return true
	})
	return out
}

func rpcPath(m protoreflect.MethodDescriptor) string {
	return "/" + string(m.Parent().(protoreflect.ServiceDescriptor).FullName()) + "/" + string(m.Name())
}

// call POSTs an empty Connect JSON body and returns the HTTP status and the
// Connect error code (empty when the call succeeded).
func call(t *testing.T, base, path, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, base+path, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Idempotency-protected writes refuse without it long before they reach
	// the database; supplying one keeps the mutation aimed at the handler.
	req.Header.Set("Idempotency-Key", fmt.Sprintf("rpc-surface-%d", time.Now().UnixNano()))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var env struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &env)
	if env.Code != "" && testing.Verbose() {
		t.Logf("%s → %s: %s", path, env.Code, env.Message)
	}
	return resp.StatusCode, env.Code
}

// unknownToStack distinguishes "this method refused me" from "this stack has
// never heard of this method". The tests run against a deployed process, so a
// locally added RPC is absent there until a redeploy — and reporting that as
// "does not reject anonymous callers" sends the reader looking for a security
// hole that is really a version skew.
//
// connect-go answers an unrouted path with a plain 404 and no JSON envelope,
// which is the pair this checks for.
func unknownToStack(status int, code string) bool {
	return status == http.StatusNotFound && code == ""
}

// login mints a token for the given audience against the running stack.
// Returns "" when the stack is unreachable, which the tests treat as a skip.
func login(t *testing.T, iamURL, audience string) string {
	t.Helper()
	subject := envOr("PALADIN_RPC_ADMIN_SUBJECT", "e2e-admin@local")
	password := envOr("PALADIN_RPC_ADMIN_PASSWORD", "e2e-not-a-secret-2026")
	body, _ := json.Marshal(map[string]string{
		"subject": subject, "password": password, "requestedAudience": audience,
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		iamURL+"/paladin.iam.v1.AuthService/Login", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build login: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Tokens struct {
			AccessToken string `json:"accessToken"`
		} `json:"tokens"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	_ = json.Unmarshal(raw, &out)
	return out.Tokens.AccessToken
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// requireStack skips unless a stack is reachable. Like smoke_test, an unmet
// environment precondition is a skip; PALADIN_RPC_SURFACE=1 forces a failure
// instead, for a job that brought the stack up on purpose.
func requireStack(t *testing.T, ps []plane) {
	t.Helper()
	for _, p := range ps {
		resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(p.baseURL + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			continue
		}
		if os.Getenv("PALADIN_RPC_SURFACE") == "1" {
			t.Fatalf("%s plane unreachable at %s: %v", p.pkgPrefix, p.baseURL, err)
		}
		t.Skipf("stack not reachable at %s (%v); bring one up, or set "+
			"PALADIN_RPC_ADMIN_URL / _DATA_URL / _IAM_URL at a deployed one",
			p.baseURL, err)
	}
}

// TestRPCSurface_RejectsAnonymous: no credential, no exceptions. A method
// that answers an unauthenticated caller with anything other than
// Unauthenticated is either leaking or crashing.
func TestRPCSurface_RejectsAnonymous(t *testing.T) {
	ps := planes()
	requireStack(t, ps)
	for _, p := range ps {
		for _, m := range methodsFor(p) {
			path := rpcPath(m)
			if anonymousAllowed[path] {
				continue
			}
			t.Run(strings.TrimPrefix(path, "/"), func(t *testing.T) {
				status, code := call(t, p.baseURL, path, "")
				if unknownToStack(status, code) {
					t.Skipf("the running stack does not serve this method (HTTP %d) — "+
						"it predates this build; redeploy to check it", status)
				}
				if code != "unauthenticated" {
					t.Errorf("anonymous call returned %q, want unauthenticated", code)
				}
			})
		}
	}
}

// anonymousAllowed lists the methods that are reachable without a credential
// by design. Keeping it explicit means adding a public endpoint is a visible
// decision in this file rather than a silent hole.
var anonymousAllowed = map[string]bool{
	"/paladin.iam.v1.AuthService/Login":        true,
	"/paladin.iam.v1.AuthService/RefreshToken": true,
	"/paladin.iam.v1.HealthService/GetHealth":  true,
	"/paladin.iam.v1.HealthService/GetVersion": true,
	// Carries its credential in the body (refresh_token) rather than the
	// Authorization header, so an empty request is a validation failure
	// before it is an authentication one — which is the correct order for
	// a method whose whole job is to trade one token for another.
	"/paladin.iam.v1.AuthService/ExchangeAudience": true,
}

// TestRPCSurface_RejectsWrongAudience: a data-plane token must not open the
// admin plane. The audience check is what separates a compromised
// data-plane credential from a platform takeover.
func TestRPCSurface_RejectsWrongAudience(t *testing.T) {
	ps := planes()
	requireStack(t, ps)
	iam := ps[2].baseURL

	for _, p := range ps {
		wrong := "paladin-data"
		if p.audience == "paladin-data" {
			wrong = "paladin-admin"
		}
		token := login(t, iam, wrong)
		if token == "" {
			t.Skip("could not mint a token; is bootstrap configured?")
		}
		for _, m := range methodsFor(p) {
			path := rpcPath(m)
			if anonymousAllowed[path] {
				continue
			}
			t.Run(strings.TrimPrefix(path, "/"), func(t *testing.T) {
				status, code := call(t, p.baseURL, path, token)
				if unknownToStack(status, code) {
					t.Skipf("the running stack does not serve this method (HTTP %d) — "+
						"it predates this build; redeploy to check it", status)
				}
				switch code {
				case "unauthenticated", "permission_denied":
					// correct
				default:
					t.Errorf("a %s token reached %s and got %q; want a refusal",
						wrong, p.audience, code)
				}
			})
		}
	}
}

// TestRPCSurface_EmptyRequestIsNeverInternal: with a valid credential and an
// empty body, every method must answer with a typed refusal — not a database
// error escaping as Internal or Unknown.
//
// This is the mutation that finds real bugs. An empty request is malformed
// for nearly every method here, so InvalidArgument is the expected answer;
// NotFound and PermissionDenied are fine too. Internal means the request got
// past validation and the database rejected the SQL, which is a defect in
// the query, not in the request.
func TestRPCSurface_EmptyRequestIsNeverInternal(t *testing.T) {
	ps := planes()
	requireStack(t, ps)
	iam := ps[2].baseURL

	for _, p := range ps {
		token := login(t, iam, p.audience)
		if token == "" {
			t.Skip("could not mint a token; is bootstrap configured?")
		}
		for _, m := range methodsFor(p) {
			path := rpcPath(m)
			t.Run(strings.TrimPrefix(path, "/"), func(t *testing.T) {
				status, code := call(t, p.baseURL, path, token)
				if unknownToStack(status, code) {
					t.Skipf("the running stack does not serve this method (HTTP %d) — "+
						"it predates this build; redeploy to check it", status)
				}
				if code == "internal" || code == "unknown" {
					t.Errorf("empty request produced %q — the query reached the "+
						"database and it disagreed; expected a typed refusal", code)
				}
			})
		}
	}
}
