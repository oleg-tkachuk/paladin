package worker

import (
	"strings"
	"testing"
)

// A half-configured client certificate must fail, not silently connect without
// one.
//
// `if cfg.TLSClientCert != "" || cfg.TLSClientKey != ""` guards the keypair
// load. Mutation testing flipped it to `&&`, and the package stayed green: with
// only a cert (or only a key) configured, the load is skipped entirely, the
// transport is built with TLS but NO client certificate, and the connection
// proceeds. A broker that requires mTLS then rejects it — or worse, one
// configured to accept either presents the operator with a working pipeline
// that is not doing the authentication they configured.
//
// The point of the `||` is that a half-configured mTLS is a MISTAKE and has to
// be reported as one. `&&` turns it into a silent downgrade.
func TestHalfConfiguredMTLSIsRejected(t *testing.T) {
	for _, c := range []struct{ name, cert, key string }{
		{"cert without key", "-----BEGIN CERTIFICATE-----\nnot a real one\n-----END CERTIFICATE-----", ""},
		{"key without cert", "", "-----BEGIN PRIVATE KEY-----\nnot a real one\n-----END PRIVATE KEY-----"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := buildKafkaTransport(kafkaSinkConfig{
				TLSEnabled:    true,
				TLSClientCert: c.cert,
				TLSClientKey:  c.key,
			})
			if err == nil {
				t.Fatal("half-configured mTLS was accepted — the transport carries " +
					"no client certificate and nothing said so")
			}
			if !strings.Contains(err.Error(), "mTLS keypair") {
				t.Errorf("error %q does not name the keypair — the operator has to "+
					"know WHICH half is missing", err)
			}
		})
	}
}

// The other side of it: TLS with no client material at all is a legitimate
// configuration (server-verified TLS), and must not be turned into an error by
// tightening the guard above.
func TestServerOnlyTLSIsAccepted(t *testing.T) {
	tr, err := buildKafkaTransport(kafkaSinkConfig{TLSEnabled: true})
	if err != nil {
		t.Fatalf("server-verified TLS without a client cert was rejected: %v", err)
	}
	if tr == nil || tr.TLS == nil {
		t.Fatal("tls_enabled produced no TLS config")
	}
	if len(tr.TLS.Certificates) != 0 {
		t.Error("a client certificate appeared without one being configured")
	}
}

// And plaintext stays plaintext: no SASL, no TLS, no transport.
func TestPlaintextProducesNoTransport(t *testing.T) {
	tr, err := buildKafkaTransport(kafkaSinkConfig{})
	if err != nil {
		t.Fatalf("plaintext config errored: %v", err)
	}
	if tr != nil {
		t.Errorf("plaintext produced a transport (%+v) — the caller reads nil as "+
			"'use the default', and a non-nil one silently changes the dial", tr)
	}
}
