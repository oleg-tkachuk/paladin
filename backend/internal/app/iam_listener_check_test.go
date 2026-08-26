package app

import (
	"net"
	"strings"
	"testing"
)

// The failure this exists for: the iam listener stops accepting while the
// process — and therefore the data-plane readiness probe kubelet actually
// reaches — carries on answering. Readiness has to fail.
func TestIAMListenerCheck_FailsWhenNothingAccepts(t *testing.T) {
	// Bind then close, so the port is one nothing listens on any more.
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	if err := dialLocalListener(t.Context(), addr); err == nil {
		t.Fatal("check passed against a port nothing is listening on")
	}
}

func TestIAMListenerCheck_PassesWhenAccepting(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	if err := dialLocalListener(t.Context(), ln.Addr().String()); err != nil {
		t.Fatalf("check failed against a live listener: %v", err)
	}
}

// The configured bind address is "0.0.0.0:8085", which is not a destination.
// The check has to dial the loopback on that port — asking whether this
// process accepts, not how it advertises itself.
func TestIAMListenerCheck_HandlesWildcardBindAddress(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	if err := dialLocalListener(t.Context(), net.JoinHostPort("0.0.0.0", port)); err != nil {
		t.Fatalf("wildcard bind address was not resolved to loopback: %v", err)
	}
}

func TestIAMListenerCheck_RejectsMalformedAddress(t *testing.T) {
	err := dialLocalListener(t.Context(), "not-an-address")
	if err == nil {
		t.Fatal("malformed address accepted")
	}
	if !strings.Contains(err.Error(), "missing port") {
		t.Errorf("unhelpful error for a malformed address: %v", err)
	}
}
