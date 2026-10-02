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

const (
	// DefaultTLSReloadInterval is how often the TLS files are checked for a
	// change: a rotated certificate is in use on the first connection after it.
	DefaultTLSReloadInterval = 30 * time.Second
	// DefaultTLSMinVersion is the lowest TLS version offered when
	// TLS.MinVersion is zero, and the lowest it may be set to.
	DefaultTLSMinVersion = tls.VersionTLS12
)

// Errors from TLS.
var (
	ErrTLSKeyPair = errors.New("paladin: a client certificate needs both CertFile and KeyFile")
	ErrNoCA       = errors.New("paladin: the CA file holds no PEM certificate")
	ErrServerID   = errors.New("paladin: the server certificate is not the expected SPIFFE ID")
	// ErrServerIDNeedsCA is ServerID without CAFile: a SPIFFE ID is checked
	// against its trust domain's bundle, which the system roots are not.
	ErrServerIDNeedsCA = errors.New("paladin: ServerID needs CAFile, the trust bundle")
	ErrTLSAndHTTP      = errors.New("paladin: WithTLS and WithHTTPClient both set the HTTP client; give one")
	// ErrTLSMinVersion is a MinVersion below DefaultTLSMinVersion, or one
	// crypto/tls does not know.
	ErrTLSMinVersion = errors.New("paladin: TLS.MinVersion is below TLS 1.2 or unknown")
)

// TLS configures the connections to Paladin (WithTLS) or to storage
// (WithTransferTLS). The files are read when the client is built — so a
// missing one fails there — and again whenever they change, checked at most
// every ReloadInterval: certificates that rotate on disk, as a workload
// identity's do, are picked up without restarting the process. Through
// WithTLS and WithTransferTLS, a connection made before a rotation is closed
// as soon as it is idle, so the new files are in use on every connection
// within one ReloadInterval of a quiet moment; a request in flight finishes
// on the connection it started on.
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
	// MinVersion is the lowest TLS version offered, a crypto/tls VersionTLS
	// constant; zero takes DefaultTLSMinVersion, and anything lower is
	// ErrTLSMinVersion.
	MinVersion uint16
}

// minVersion is MinVersion with its default applied, or ErrTLSMinVersion.
func (c TLS) minVersion() (uint16, error) {
	switch c.MinVersion {
	case 0:
		return DefaultTLSMinVersion, nil
	case tls.VersionTLS12, tls.VersionTLS13:
		return c.MinVersion, nil
	}
	return 0, fmt.Errorf("%w: %#04x", ErrTLSMinVersion, c.MinVersion)
}

// Transport returns an http.Transport that makes its connections with c,
// bounded like NewTransfer's, for a client of your own; its fields are yours
// to change. A rotation reaches its new connections only: the ones it already
// holds are not closed for it, as WithTLS and WithTransferTLS close theirs.
// Its connections are made directly: a proxy from the environment is not
// used, because it would make the TLS connection itself, without these files.
func (c TLS) Transport() (*http.Transport, error) {
	t, _, err := c.transport()
	return t, err
}

// transport builds the http.Transport and the files it dials with.
func (c TLS) transport() (*http.Transport, *tlsFiles, error) {
	if (c.CertFile == "") != (c.KeyFile == "") {
		return nil, nil, ErrTLSKeyPair
	}
	minVersion, err := c.minVersion()
	if err != nil {
		return nil, nil, err
	}
	files := &tlsFiles{spec: c, interval: c.ReloadInterval, minVersion: minVersion, open: map[uint64]int{}}
	if c.ServerID != "" {
		if c.CAFile == "" {
			return nil, nil, ErrServerIDNeedsCA
		}
		id, err := spiffeid.FromString(c.ServerID)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrServerID, err)
		}
		files.serverID = id
	}
	if files.interval <= 0 {
		files.interval = DefaultTLSReloadInterval
	}
	if err := files.load(); err != nil {
		return nil, nil, err
	}
	t := newTransferTransport()
	t.Proxy = nil
	t.DialTLSContext = files.dial
	return t, files, nil
}

// roundTripper is the transport WithTLS and WithTransferTLS use: before each
// request it checks the files and closes the idle connections a rotation has
// left on old ones.
func (c TLS) roundTripper() (*rotatingTransport, error) {
	t, files, err := c.transport()
	if err != nil {
		return nil, err
	}
	return &rotatingTransport{Transport: t, files: files}, nil
}

// rotatingTransport retires the connections made with files a rotation has
// replaced: each is closed once it is idle, so the next request dials with the
// new ones.
type rotatingTransport struct {
	*http.Transport
	files *tlsFiles
}

func (r *rotatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.files.stale() {
		r.CloseIdleConnections()
	}
	return r.Transport.RoundTrip(req)
}

// tlsFiles holds what the files held when last read, and re-reads them when
// their modification times change.
type tlsFiles struct {
	spec       TLS
	interval   time.Duration
	minVersion uint16

	serverID spiffeid.ID // zero: none

	mu      sync.Mutex
	checked time.Time
	mtimes  [3]time.Time
	roots   *x509.CertPool // nil: the system roots
	bundle  *x509bundle.Bundle
	cert    *tls.Certificate
	// generation counts the loads; open counts the connections still open
	// per generation they were made with.
	generation uint64
	open       map[uint64]int
	// retiredGen and retiredAt are when idle connections were last closed
	// for a rotation.
	retiredGen uint64
	retiredAt  time.Time
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
	f.generation++
	return nil
}

// current returns the CA pool and client certificate, re-reading the files
// when ReloadInterval has passed and one of them changed. A file caught
// mid-rotation — one of the pair rewritten, the other not yet — fails to
// load; the last good pair is kept and the next check tries again.
func (f *tlsFiles) current() (*x509.CertPool, *x509bundle.Bundle, *tls.Certificate, uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refresh()
	return f.roots, f.bundle, f.cert, f.generation
}

// refresh re-reads the files when the interval has passed and one changed.
// The caller holds mu.
func (f *tlsFiles) refresh() {
	if time.Since(f.checked) < f.interval {
		return
	}
	f.checked = time.Now()
	for i, p := range f.paths() {
		if m, err := mtimeOf(p); err == nil && !m.Equal(f.mtimes[i]) {
			_ = f.load() // on failure the previous files stay in use
			return
		}
	}
}

// stale refreshes the files and reports whether idle connections should be
// closed: a connection made with older files is still open, and they have not
// been closed for this generation, nor within the interval. Closing idle
// connections closes current ones too, so a connection that stays busy — a
// long stream — costs at most one round of reconnects per interval.
func (f *tlsFiles) stale() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refresh()
	old := false
	for gen := range f.open {
		if gen != f.generation {
			old = true
			break
		}
	}
	if !old || (f.retiredGen == f.generation && time.Since(f.retiredAt) < f.interval) {
		return false
	}
	f.retiredGen, f.retiredAt = f.generation, time.Now()
	return true
}

// opened and closed track a connection made with generation gen.
func (f *tlsFiles) opened(gen uint64) {
	f.mu.Lock()
	f.open[gen]++
	f.mu.Unlock()
}

func (f *tlsFiles) closed(gen uint64) {
	f.mu.Lock()
	if f.open[gen]--; f.open[gen] <= 0 {
		delete(f.open, gen)
	}
	f.mu.Unlock()
}

// trackedConn is the TCP connection under a TLS one, telling its files when
// it closes. It sits below the *tls.Conn rather than around it: net/http hands
// HTTP/2 only a connection that is a *tls.Conn itself, and closing the TLS
// connection closes this one.
type trackedConn struct {
	net.Conn
	files *tlsFiles
	gen   uint64
	once  sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() { c.files.closed(c.gen) })
	return c.Conn.Close()
}

func (f *tlsFiles) clientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	_, _, cert, _ := f.current()
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
	roots, bundle, _, gen := f.current()
	cfg := &tls.Config{MinVersion: f.minVersion, RootCAs: roots, ServerName: host, NextProtos: alpn}
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
	f.opened(gen)
	tracked := &trackedConn{Conn: raw, files: f, gen: gen}
	conn := tls.Client(tracked, cfg)
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = tracked.Close()
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
// that a transport from c.Transport() instead. Unlike a transfer, an RPC has
// no response-header timeout: the call's context bounds it, as it does
// without TLS.
func WithTLS(c TLS) Option {
	return func(cfg *config) { cfg.tls = &c }
}

// rpcClient is the HTTP client WithTLS builds.
func (c TLS) rpcClient() (*http.Client, error) {
	rt, err := c.roundTripper()
	if err != nil {
		return nil, err
	}
	// A long call — a server stream, a large batch — sends its headers late.
	rt.ResponseHeaderTimeout = 0
	return &http.Client{Transport: rt}, nil
}

// WithTransferTLS makes the presigned requests to storage with c — for
// storage that requires its own CA or a client certificate.
func WithTransferTLS(c TLS) TransferOption {
	return func(cfg *transferConfig) error {
		rt, err := c.roundTripper()
		if err != nil {
			return err
		}
		cfg.client = &http.Client{Transport: rt}
		return nil
	}
}
