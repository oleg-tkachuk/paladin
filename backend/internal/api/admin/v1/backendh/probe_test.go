package backendh

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
)

type fakeProber struct {
	err    error
	called bool
}

func (p *fakeProber) Probe(_ context.Context, _ string) error {
	p.called = true
	return p.err
}

func TestTestBackend_NoProber(t *testing.T) {
	h := NewHandler(fakeBackendRepo{}, allowAuthorizer{}, "")
	out, err := h.TestBackend(ctxWithRoles("platform.admin"), "primary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Reachable {
		t.Error("reachable should be false when no prober is wired")
	}
	if out.ErrorMessage == "" {
		t.Error("want an explanatory message when probing is unavailable")
	}
}

func TestTestBackend_Reachable(t *testing.T) {
	h := NewHandler(fakeBackendRepo{}, allowAuthorizer{}, "")
	pr := &fakeProber{}
	h.SetProber(pr)
	out, err := h.TestBackend(ctxWithRoles("platform.admin"), "primary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pr.called {
		t.Error("prober was not invoked")
	}
	if !out.Reachable {
		t.Errorf("want reachable=true, got msg=%q", out.ErrorMessage)
	}
}

func TestTestBackend_Unreachable(t *testing.T) {
	h := NewHandler(fakeBackendRepo{}, allowAuthorizer{}, "")
	h.SetProber(&fakeProber{err: errors.New("dial tcp: connection refused")})
	out, err := h.TestBackend(ctxWithRoles("platform.admin"), "primary")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Reachable {
		t.Error("want reachable=false on a probe error")
	}
	if out.ErrorMessage == "" {
		t.Error("want the probe error surfaced")
	}
}

func TestTestBackend_RequiresPlatformAdmin(t *testing.T) {
	h := NewHandler(fakeBackendRepo{}, allowAuthorizer{}, "")
	h.SetProber(&fakeProber{})
	_, err := h.TestBackend(ctxWithRoles("tenant.admin"), "primary")
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("got %v, want PermissionDenied", connect.CodeOf(err))
	}
}
