// Package tlsconfig builds the tls.Config the listeners and outbound clients
// share, and maps the configured client-auth mode onto Go's enum.
package tlsconfig

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
)

// New creates a tls.Config based on the provided settings.
//
// Used for BOTH client outbound (sets RootCAs) and server inbound
// (sets ClientCAs). For server-side mTLS termination, also set
// `clientAuth` via `ParseClientAuth(cfg.ClientAuth)` and assign the
// result to the returned config's `ClientAuth` field — see app.go
// for the wire-up. The `ClientCAs` pool here mirrors RootCAs by
// design: callers + callees within the cluster share the same
// internal-mtls CA bundle.
func New(certPath, keyPath, caPath, serverName string, insecureSkipVerify bool) (*tls.Config, error) {
	var cert tls.Certificate
	var err error

	// Load client/server certificate and key if provided
	if certPath != "" && keyPath != "" {
		cert, err = tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load certificate/key: %w", err)
		}
	}

	// Load CA certificate if provided
	var caPool *x509.CertPool
	if caPath != "" {
		caCert, err := os.ReadFile(caPath) // #nosec G304
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}
		caPool = x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to append CA certificate to pool")
		}
	}

	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		RootCAs:            caPool, // For Client verifying Server
		ClientCAs:          caPool, // For Server verifying Client (mTLS)
		ServerName:         serverName,
		InsecureSkipVerify: insecureSkipVerify, // #nosec G402
	}, nil
}

// ParseClientAuth maps the human-readable strings in config.TLS.ClientAuth
// onto Go's tls.ClientAuthType enum. The string form is the public
// API (lives in yaml + helm values); this helper is the single
// translation point.
//
// Modes:
//   - ""            → none           (default; one-way TLS, server presents
//     its cert, client cert ignored)
//   - "none"        → NoClientCert
//   - "request"     → RequestClientCert        (request, don't verify)
//   - "require"     → RequireAnyClientCert     (require, don't verify)
//   - "permissive"  → VerifyClientCertIfGiven  (verify when present —
//     rollout cutover mode)
//   - "strict"      → RequireAndVerifyClientCert (full mTLS — the goal)
//
// Unknown values fall back to NoClientCert + return a non-nil error
// so a misconfigured deployment fails loud at boot rather than
// silently weakening to one-way TLS.
func ParseClientAuth(mode string) (tls.ClientAuthType, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "none":
		return tls.NoClientCert, nil
	case "request":
		return tls.RequestClientCert, nil
	case "require":
		return tls.RequireAnyClientCert, nil
	case "permissive", "verify_if_given":
		return tls.VerifyClientCertIfGiven, nil
	case "strict", "require_and_verify":
		return tls.RequireAndVerifyClientCert, nil
	default:
		return tls.NoClientCert, fmt.Errorf("unknown client_auth mode %q (want one of: none|request|require|permissive|strict)", mode)
	}
}
