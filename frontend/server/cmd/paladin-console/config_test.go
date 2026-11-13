package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

const authorizePath = "/oauth/authorize"

func env(vals map[string]string) func(string) string {
	return func(k string) string { return vals[k] }
}

func noFile(string) ([]byte, error) { return nil, os.ErrNotExist }

func TestLoadConfig_Defaults(t *testing.T) {
	c, err := loadConfig(env(nil), noFile, authorizePath)
	require.NoError(t, err)
	assert.Equal(t, defaultPort, c.port)
	assert.Equal(t, defaultIAMURL+authorizePath, c.authorizeURL)
	assert.Equal(t, zapcore.InfoLevel, c.logLevel)
	assert.Nil(t, c.backendCA)
	assert.Equal(t, ":3000", c.listenAddr())
}

func TestLoadConfig_FromTheEnvironment(t *testing.T) {
	c, err := loadConfig(env(map[string]string{
		envPort:           "8080",
		envIAMURL:         "https://iam.internal:8085/",
		envStorageOrigins: "https://s3.example.com",
		envLogLevel:       "debug",
		envBannerContent:  "Maintenance",
		envBannerSeverity: "warning",
		envBannerURL:      "/health",
		envBannerPerm:     "true",
		envBannerPosition: "bottom",
	}), noFile, authorizePath)
	require.NoError(t, err)
	assert.Equal(t, 8080, c.port)
	assert.Equal(t, "https://iam.internal:8085/oauth/authorize", c.authorizeURL)
	assert.Equal(t, "https://s3.example.com", c.storageOrigins)
	assert.Equal(t, zapcore.DebugLevel, c.logLevel)
	assert.Equal(t, "Maintenance", c.banner.Content)
	assert.Equal(t, "warning", c.banner.Severity)
	assert.Equal(t, "/health", c.banner.URL)
	assert.Equal(t, "true", c.banner.Permanent)
	assert.Equal(t, "bottom", c.banner.Position)
}

func TestLoadConfig_Refuses(t *testing.T) {
	for name, vals := range map[string]map[string]string{
		"a port that is not a number": {envPort: "http"},
		"a port out of range":         {envPort: "70000"},
		"an unknown log level":        {envLogLevel: "loud"},
		"a CA file that is missing":   {envBackendCAFile: "/nowhere/ca.crt"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfig(env(vals), noFile, authorizePath)
			assert.Error(t, err)
		})
	}

	t.Run("a CA file holding no certificate", func(t *testing.T) {
		read := func(string) ([]byte, error) { return []byte("not a pem"), nil }
		_, err := loadConfig(env(map[string]string{envBackendCAFile: "/ca.crt"}), read, authorizePath)
		require.ErrorContains(t, err, "no certificate")
	})
}

func TestLoadConfig_BackendCA(t *testing.T) {
	ca, caPEM := testCA(t)
	read := func(string) ([]byte, error) { return caPEM, nil }
	c, err := loadConfig(env(map[string]string{envBackendCAFile: "/ca.crt"}), read, authorizePath)
	require.NoError(t, err)
	require.NotNil(t, c.backendCA)

	// The relay trusts a plane whose certificate the CA signed.
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.TLS = ca.serverTLS(t)
	srv.StartTLS()
	defer srv.Close()
	resp, err := (&http.Client{Transport: c.relayTransport()}).Get(srv.URL)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Without it, the same plane is refused.
	_, err = (&http.Client{Transport: config{}.relayTransport()}).Get(srv.URL)
	var unverified *tls.CertificateVerificationError
	assert.ErrorAs(t, err, &unverified)
}

func TestProbe(t *testing.T) {
	const live = "/api/health/live"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != live {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	_, portText, err := net.SplitHostPort(srv.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)

	assert.Equal(t, 0, probe(port, live))
	assert.Equal(t, 1, probe(port, "/elsewhere"), "a non-200 is not live")
	srv.Close()
	assert.Equal(t, 1, probe(port, live), "nothing listening is not live")
}

// testCA is a throwaway CA, for the relay's TLS.
type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func testCA(t *testing.T) (authority, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return authority{cert: cert, key: key}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// The chart sets what the server reads: a variable renamed on one side only
// would leave the other at its default with nothing said.
func TestTheChartSetsTheEnvironment(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "chart", "templates", "deployment.yaml"))
	require.NoError(t, err)
	for _, name := range []string{
		envIAMURL, envBackendCAFile, envStorageOrigins,
		envBannerContent, envBannerSeverity, envBannerURL, envBannerPerm, envBannerPosition,
	} {
		assert.Contains(t, string(body), "name: "+name, name)
	}
}

// serverTLS is a certificate for 127.0.0.1 the authority signed.
func (a authority) serverTLS(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "plane"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	require.NoError(t, err)
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	}
}
