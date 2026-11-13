package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/paladin/frontend/server/internal/web"
)

// The environment the chart sets (deploy/chart/templates/deployment.yaml).
const (
	envPort           = "PORT"
	envIAMURL         = "PALADIN_IAM_URL"
	envBackendCAFile  = "PALADIN_BACKEND_CA_FILE"
	envStorageOrigins = "PALADIN_STORAGE_ORIGINS"
	envLogLevel       = "PALADIN_LOG_LEVEL"
	envBannerContent  = "PALADIN_BANNER_CONTENT"
	envBannerSeverity = "PALADIN_BANNER_SEVERITY"
	envBannerURL      = "PALADIN_BANNER_URL"
	envBannerPerm     = "PALADIN_BANNER_PERMANENT"
	envBannerPosition = "PALADIN_BANNER_POSITION"
)

const (
	// defaultPort is the chart's service.port.
	defaultPort = 3000
	// defaultIAMURL is the backend chart's iam Service under its default
	// release name.
	defaultIAMURL = "http://paladin-core-iam:8085"
	maxPort       = 65535
)

type config struct {
	port           int
	authorizeURL   string
	backendCA      *x509.CertPool
	storageOrigins string
	logLevel       zapcore.Level
	banner         web.Banner
}

// loadConfig reads the environment through getenv; readFile reads the CA.
func loadConfig(getenv func(string) string, readFile func(string) ([]byte, error), authorizePath string) (config, error) {
	c := config{
		port:           defaultPort,
		storageOrigins: getenv(envStorageOrigins),
		logLevel:       zapcore.InfoLevel,
		banner: web.Banner{
			Content:   getenv(envBannerContent),
			Severity:  getenv(envBannerSeverity),
			URL:       getenv(envBannerURL),
			Permanent: getenv(envBannerPerm),
			Position:  getenv(envBannerPosition),
		},
	}
	if v := getenv(envPort); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p <= 0 || p > maxPort {
			return config{}, fmt.Errorf("%s=%q: not a port", envPort, v)
		}
		c.port = p
	}
	iam := getenv(envIAMURL)
	if iam == "" {
		iam = defaultIAMURL
	}
	c.authorizeURL = strings.TrimRight(iam, "/") + authorizePath
	if v := getenv(envLogLevel); v != "" {
		if err := c.logLevel.UnmarshalText([]byte(v)); err != nil {
			return config{}, fmt.Errorf("%s=%q: %w", envLogLevel, v, err)
		}
	}
	if path := getenv(envBackendCAFile); path != "" {
		pem, err := readFile(path)
		if err != nil {
			return config{}, fmt.Errorf("%s: %w", envBackendCAFile, err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return config{}, fmt.Errorf("%s=%q: no certificate in it", envBackendCAFile, path)
		}
		c.backendCA = pool
	}
	return c, nil
}

func (c config) listenAddr() string { return net.JoinHostPort("", strconv.Itoa(c.port)) }

// relayTransport trusts the backend's CA when one is set, the system's
// otherwise.
func (c config) relayTransport() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	if c.backendCA != nil {
		t.TLSClientConfig = &tls.Config{RootCAs: c.backendCA, MinVersion: tls.VersionTLS12}
	}
	return t
}
