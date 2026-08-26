package app

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
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

	if err := dialLocalListener(t.Context(), addr, false); err == nil {
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

	if err := dialLocalListener(t.Context(), ln.Addr().String(), false); err != nil {
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
	if err := dialLocalListener(t.Context(), net.JoinHostPort("0.0.0.0", port), false); err != nil {
		t.Fatalf("wildcard bind address was not resolved to loopback: %v", err)
	}
}

func TestIAMListenerCheck_RejectsMalformedAddress(t *testing.T) {
	err := dialLocalListener(t.Context(), "not-an-address", false)
	if err == nil {
		t.Fatal("malformed address accepted")
	}
	if !strings.Contains(err.Error(), "missing port") {
		t.Errorf("unhelpful error for a malformed address: %v", err)
	}
}

// On a TLS listener the check completes the handshake instead of hanging up
// on it. A bare TCP dial proves the port accepts, but the abandoned handshake
// looks to the server exactly like a client that gave up, and it logged a
// warning for every probe — six a minute, in the log where an unexplained
// handshake error was being investigated.
func TestIAMListenerCheck_CompletesTheHandshakeOnATLSListener(t *testing.T) {
	cert, err := selfSignedCert()
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	var lc net.ListenConfig
	raw, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln := tls.NewListener(raw, &tls.Config{Certificates: []tls.Certificate{cert}})
	defer func() { _ = ln.Close() }()

	handshakeErr := make(chan error, 1)
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			handshakeErr <- aerr
			return
		}
		defer func() { _ = conn.Close() }()
		// The server only learns the handshake failed when it tries it.
		handshakeErr <- conn.(*tls.Conn).HandshakeContext(t.Context())
	}()

	if err := dialLocalListener(t.Context(), ln.Addr().String(), true); err != nil {
		t.Fatalf("check failed against a live TLS listener: %v", err)
	}
	if err := <-handshakeErr; err != nil {
		t.Errorf("the server saw a broken handshake: %v — this is the warning "+
			"the probe used to emit on every tick", err)
	}
}

// selfSignedCert mints a throwaway cert for the test listener.
func selfSignedCert() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
