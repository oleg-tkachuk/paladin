package tlsconfig_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/tlsconfig"
)

// certValidity only has to outlive the test.
const certValidity = time.Hour

// writeCert writes a self-signed certificate and its key as PEM and returns
// the two paths.
func writeCert(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(certValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	write(t, certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	write(t, keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPath, keyPath
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNewLoadsCertAndCA(t *testing.T) {
	certPath, keyPath := writeCert(t)
	cfg, err := tlsconfig.New(certPath, keyPath, certPath, "paladin.internal", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Certificates) != 1 || cfg.Certificates[0].Certificate == nil {
		t.Fatal("certificate not loaded")
	}
	if cfg.RootCAs == nil || cfg.ClientCAs == nil || !cfg.RootCAs.Equal(cfg.ClientCAs) {
		t.Fatal("the CA must back both RootCAs and ClientCAs")
	}
	if cfg.ServerName != "paladin.internal" {
		t.Fatalf("ServerName = %q", cfg.ServerName)
	}
}

func TestNewWithoutFiles(t *testing.T) {
	cfg, err := tlsconfig.New("", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RootCAs != nil || !cfg.InsecureSkipVerify {
		t.Fatalf("unexpected config: RootCAs=%v InsecureSkipVerify=%v", cfg.RootCAs, cfg.InsecureSkipVerify)
	}
}

func TestNewErrors(t *testing.T) {
	certPath, keyPath := writeCert(t)
	garbage := filepath.Join(t.TempDir(), "garbage.pem")
	write(t, garbage, []byte("not a certificate"))
	missing := filepath.Join(t.TempDir(), "missing.pem")

	cases := []struct {
		name          string
		cert, key, ca string
	}{
		{"key does not match", certPath, garbage, ""},
		{"missing CA file", certPath, keyPath, missing},
		{"CA file holds no certificate", certPath, keyPath, garbage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tlsconfig.New(tc.cert, tc.key, tc.ca, "", false); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestParseClientAuth(t *testing.T) {
	cases := []struct {
		mode    string
		want    tls.ClientAuthType
		wantErr bool
	}{
		{"", tls.NoClientCert, false},
		{"none", tls.NoClientCert, false},
		{"request", tls.RequestClientCert, false},
		{"require", tls.RequireAnyClientCert, false},
		{"permissive", tls.VerifyClientCertIfGiven, false},
		{"verify_if_given", tls.VerifyClientCertIfGiven, false},
		{"strict", tls.RequireAndVerifyClientCert, false},
		{" STRICT ", tls.RequireAndVerifyClientCert, false},
		{"require_and_verify", tls.RequireAndVerifyClientCert, false},
		{"mutual", tls.NoClientCert, true},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			got, err := tlsconfig.ParseClientAuth(tc.mode)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("ParseClientAuth(%q) = %v, %v", tc.mode, got, err)
			}
		})
	}
}
