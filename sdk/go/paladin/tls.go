package paladin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
)

// DefaultTLSReloadInterval is how often the TLS files are checked for a
// change: a rotated certificate is in use on the first connection after it.
const DefaultTLSReloadInterval = 30 * time.Second

// Errors from TLS.
var (
	ErrTLSKeyPair = errors.New("paladin: a client certificate needs both CertFile and KeyFile")
	ErrNoCA       = errors.New("paladin: the CA file holds no PEM certificate")
	ErrServerID   = errors.New("paladin: the server certificate is not the expected SPIFFE ID")
	// ErrServerIDNeedsCA is ServerID without CAFile: a SPIFFE ID is checked
	// against its trust domain's bundle, which the system roots are not.
	ErrServerIDNeedsCA = errors.New("paladin: ServerID needs CAFile, the trust bundle")
	ErrTLSAndHTTP      = errors.New("paladin: WithTLS and WithHTTPClient both set the HTTP client; give one")
)

// TLS configures the connections to Paladin (WithTLS) or to storage
// (WithTransferTLS). The files are read when the client is built — so a
// missing one fails there — and again whenever they change, checked at most
// every ReloadInterval: certificates that rotate on disk, as a workload
// identity's do, are picked up without restarting the process. A connection
// already open keeps the certificate it was made with.
type TLS struct {
	// CAFile is a PEM bundle of the CAs the server's certificate must chain
	// to; empty trusts the system roots.
	CAFile string
	// CertFile and KeyFile are the client certificate for mutual TLS; both or
	// neither.
	CertFile string
	KeyFile  string
	// ServerID, when set, is the SPIFFE ID the server must present,
	// spiffe://trust-domain/path, verified as an X.509-SVID against CAFile as
	// that trust domain's bundle, by the SPIFFE project's own library. The
	// host name is then not checked: an SVID names a workload, not a host.
	ServerID string
	// VerifyPeer runs after the built-in checks, on the server's leaf
	// certificate; an error refuses the connection.
	VerifyPeer func(leaf *x509.Certificate) error
	// ReloadInterval bounds how often the files are checked for a change;
	// zero takes DefaultTLSReloadInterval.
	ReloadInterval time.Duration
}

// Transport returns an http.Transport that makes its connections with c,
// bounded like NewTransfer's, for a client of your own. Its connections are
// made directly: a proxy from the environment is not used, because it would
// make the TLS connection itself, without these files.
func (c TLS) Transport() (*http.Transport, error) {
	if (c.CertFile == "") != (c.KeyFile == "") {
		return nil, ErrTLSKeyPair
	}
	files := &tlsFiles{spec: c, interval: c.ReloadInterval}
	if c.ServerID != "" {
		if c.CAFile == "" {
			return nil, ErrServerIDNeedsCA
		}
		id, err := spiffeid.FromString(c.ServerID)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrServerID, err)
		}
		files.serverID = id
	}
	if files.interval <= 0 {
		files.interval = DefaultTLSReloadInterval
	}
	if err := files.load(); err != nil {
		return nil, err
	}
	t := newTransferTransport()
	t.Proxy = nil
	t.DialTLSContext = files.dial
	return t, nil
}

// tlsFiles holds what the files held when last read, and re-reads them when
// their modification times change.
type tlsFiles struct {
	spec     TLS
	interval time.Duration

	serverID spiffeid.ID // zero: none

	mu      sync.Mutex
	checked time.Time
	mtimes  [3]time.Time
	roots   *x509.CertPool // nil: the system roots
	bundle  *x509bundle.Bundle
	cert    *tls.Certificate
}

func (f *tlsFiles) paths() [3]string {
	return [3]string{f.spec.CAFile, f.spec.CertFile, f.spec.KeyFile}
}

func mtimeOf(path string) (time.Time, error) {
	if path == "" {
		return time.Time{}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// load reads every file. The caller holds mu, or owns f.
func (f *tlsFiles) load() error {
	var mtimes [3]time.Time
	for i, p := range f.paths() {
		m, err := mtimeOf(p)
		if err != nil {
			return fmt.Errorf("paladin: tls: %w", err)
		}
		mtimes[i] = m
	}
	var (
		roots  *x509.CertPool
		bundle *x509bundle.Bundle
	)
	if f.spec.CAFile != "" {
		pem, err := os.ReadFile(f.spec.CAFile)
		if err != nil {
			return fmt.Errorf("paladin: tls: %w", err)
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return fmt.Errorf("%w: %s", ErrNoCA, f.spec.CAFile)
		}
		if !f.serverID.IsZero() {
			b, err := x509bundle.Parse(f.serverID.TrustDomain(), pem)
			if err != nil {
				return fmt.Errorf("paladin: tls: %w", err)
			}
			bundle = b
		}
	}
	var cert *tls.Certificate
	if f.spec.CertFile != "" {
		pair, err := tls.LoadX509KeyPair(f.spec.CertFile, f.spec.KeyFile)
		if err != nil {
			return fmt.Errorf("paladin: tls: %w", err)
		}
		cert = &pair
	}
	f.roots, f.bundle, f.cert, f.mtimes, f.checked = roots, bundle, cert, mtimes, time.Now()
	return nil
}

// current returns the CA pool and client certificate, re-reading the files
// when ReloadInterval has passed and one of them changed. A file caught
// mid-rotation — one of the pair rewritten, the other not yet — fails to
// load; the last good pair is kept and the next check tries again.
func (f *tlsFiles) current() (*x509.CertPool, *x509bundle.Bundle, *tls.Certificate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if time.Since(f.checked) >= f.interval {
		f.checked = time.Now()
		for i, p := range f.paths() {
			if m, err := mtimeOf(p); err == nil && !m.Equal(f.mtimes[i]) {
				_ = f.load() // on failure the previous files stay in use
				break
			}
		}
	}
	return f.roots, f.bundle, f.cert
}

func (f *tlsFiles) clientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	_, _, cert := f.current()
	return cert, nil
}

// alpn offers HTTP/2 and HTTP/1.1, as http.Transport does on its own dials.
var alpn = []string{"h2", "http/1.1"}

// dial makes one TLS connection with the files as they are now: the current
// CA pool and client certificate. Without ServerID the server's certificate
// is verified the standard way, against the host dialled; with it, as an
// X.509-SVID for that SPIFFE ID, by go-spiffe.
func (f *tlsFiles) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	roots, bundle, _ := f.current()
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: host, NextProtos: alpn}
	switch {
	case !f.serverID.IsZero():
		// The hook resets the config's authentication fields, so the client
		// certificate is set after it.
		tlsconfig.HookTLSClientConfig(cfg, bundle, f.authorize)
	case f.spec.VerifyPeer != nil:
		cfg.VerifyConnection = func(cs tls.ConnectionState) error { return f.spec.VerifyPeer(cs.PeerCertificates[0]) }
	}
	if f.spec.CertFile != "" {
		cfg.GetClientCertificate = f.clientCertificate
	}
	var d net.Dialer
	d.Timeout = DefaultTransferDialTimeout
	raw, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, cfg)
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return conn, nil
}

// authorize runs once go-spiffe has verified the server's SVID against the
// bundle: the SPIFFE ID must be ServerID, and VerifyPeer must accept the leaf.
func (f *tlsFiles) authorize(id spiffeid.ID, chains [][]*x509.Certificate) error {
	if err := tlsconfig.AuthorizeID(f.serverID)(id, chains); err != nil {
		return fmt.Errorf("%w: %w", ErrServerID, err)
	}
	if f.spec.VerifyPeer != nil && len(chains) > 0 && len(chains[0]) > 0 {
		return f.spec.VerifyPeer(chains[0][0])
	}
	return nil
}

// WithTLS makes the client's connections to Paladin with c: a CA bundle, a
// client certificate, a server identity, each re-read when it rotates. It
// builds the HTTP client, so it cannot be combined with WithHTTPClient; give
// that a transport from c.Transport() instead.
func WithTLS(c TLS) Option {
	return func(cfg *config) { cfg.tls = &c }
}

// WithTransferTLS makes the presigned requests to storage with c — for
// storage that requires its own CA or a client certificate.
func WithTransferTLS(c TLS) TransferOption {
	return func(cfg *transferConfig) error {
		t, err := c.Transport()
		if err != nil {
			return err
		}
		cfg.client = &http.Client{Transport: t}
		return nil
	}
}
