package paladin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
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
	// ErrTLSAndHTTP is WithTLS with WithHTTPClient. To wrap the TLS
	// transport, build the client from TLS.RoundTripper instead.
	ErrTLSAndHTTP = errors.New("paladin: WithTLS and WithHTTPClient both set the HTTP client; give one, or wrap TLS.RoundTripper()")
	// ErrTLSMinVersion is a MinVersion below DefaultTLSMinVersion, or one
	// crypto/tls does not know.
	ErrTLSMinVersion = errors.New("paladin: TLS.MinVersion is below TLS 1.2 or unknown")
)

// TLS configures the connections to Paladin (WithTLS) or to storage
// (WithTransferTLS). The files are read when the client is built — so a
// missing one fails there — and again whenever they change, checked at most
// every ReloadInterval: certificates that rotate on disk, as a workload
// identity's do, are picked up without restarting the process. Through
// WithTLS, WithTransferTLS and RoundTripper, a connection made before a
// rotation is closed as soon as it is idle, so the new files are in use on
// every connection within one ReloadInterval of a quiet moment; a request in
// flight finishes on the connection it started on.
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
// holds are not closed for it. RoundTripper is the same transport with them
// closed, and is what a client of your own should use.
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
	files := &tlsFiles{spec: c, interval: c.ReloadInterval, minVersion: minVersion}
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

// RoundTripper returns the transport WithTLS and WithTransferTLS use, for a
// client of your own — one whose transport you wrap in a circuit breaker,
// metrics or a peer-identity recorder, then give to WithHTTPClient or
// WithTransferHTTPClient. It is Transport with rotation: once the files
// change, every new request goes out on a connection made with the new ones,
// HTTP/2 included, while a request already in flight finishes on its own; the
// connections of the old files are closed as soon as nothing uses them. Its
// bounds are Transport's; for RPCs set ResponseHeaderTimeout to zero, as
// WithTLS does, and let the call's context bound them.
func (c TLS) RoundTripper() (*RotatingTransport, error) {
	t, files, err := c.transport()
	if err != nil {
		return nil, err
	}
	return &RotatingTransport{Transport: t, files: files}, nil
}

// RotatingTransport sends each request on an http.Transport made for the TLS
// files as they are now. The embedded Transport is the template every one is
// cloned from: its fields are yours to change before the first request. A
// rotation starts a new clone for the requests after it, and the previous one
// is closed once its last response is read — so no request is sent on a
// connection made with replaced files, and none in flight is cut off.
type RotatingTransport struct {
	*http.Transport
	files *tlsFiles

	mu       sync.Mutex
	current  *generation
	draining map[*generation]struct{}
}

// generation is the transport for one load of the files, and the requests it
// is carrying.
type generation struct {
	id       uint64
	t        *http.Transport
	inFlight int
	open     atomic.Int64 // its connections not yet closed
}

// Bounds of closing a replaced generation's connections: an HTTP/2
// connection turns idle a moment after its last response is read, so the
// close is repeated every retirePoll until none is left, or until
// retireTimeout — the transport's own IdleConnTimeout reaps any after that.
const (
	retirePoll    = 50 * time.Millisecond
	retireTimeout = DefaultTransferIdleConnTimeout
)

// newGeneration clones the template for files load id, counting its
// connections.
func (r *RotatingTransport) newGeneration(id uint64) *generation {
	g := &generation{id: id, t: r.Clone()}
	g.t.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return r.files.dialCounted(ctx, network, addr, &g.open)
	}
	return g
}

// close closes g's connections as each turns idle; g takes no request.
func (g *generation) close() {
	deadline := time.Now().Add(retireTimeout)
	for {
		g.t.CloseIdleConnections()
		if g.open.Load() <= 0 || time.Now().After(deadline) {
			return
		}
		time.Sleep(retirePoll)
	}
}

// RoundTrip sends req on the transport for the current files.
func (r *RotatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	g := r.acquire()
	resp, err := g.t.RoundTrip(req)
	if err != nil {
		r.release(g)
		return nil, err
	}
	resp.Body = &releasingBody{ReadCloser: resp.Body, release: func() { r.release(g) }}
	return resp, nil
}

// acquire returns the generation for the files as they are now, starting a new
// one when they changed, and counts a request on it.
func (r *RotatingTransport) acquire() *generation {
	id := r.files.generationNow()
	r.mu.Lock()
	var idle *generation // replaced with nothing in flight: closed now
	if r.current == nil || r.current.id != id {
		if old := r.current; old != nil {
			if old.inFlight == 0 {
				idle = old
			} else {
				if r.draining == nil {
					r.draining = map[*generation]struct{}{}
				}
				r.draining[old] = struct{}{}
			}
		}
		r.current = r.newGeneration(id)
	}
	r.current.inFlight++
	g := r.current
	r.mu.Unlock()
	if idle != nil {
		go idle.close()
	}
	return g
}

// release ends a request on g, and closes g once it was replaced and its
// last request has ended.
func (r *RotatingTransport) release(g *generation) {
	r.mu.Lock()
	g.inFlight--
	_, replaced := r.draining[g]
	drained := replaced && g.inFlight == 0
	if drained {
		delete(r.draining, g)
	}
	r.mu.Unlock()
	if drained {
		go g.close()
	}
}

// CloseIdleConnections closes the idle connections of every generation, as
// http.Client.CloseIdleConnections expects.
func (r *RotatingTransport) CloseIdleConnections() {
	r.mu.Lock()
	ts := make([]*http.Transport, 0, len(r.draining)+1)
	if r.current != nil {
		ts = append(ts, r.current.t)
	}
	for g := range r.draining {
		ts = append(ts, g.t)
	}
	r.mu.Unlock()
	for _, t := range ts {
		t.CloseIdleConnections()
	}
}

// releasingBody ends its request when the body is closed, or read to its end.
type releasingBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (b *releasingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.release)
	}
	return n, err
}

func (b *releasingBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
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
	// generation counts the loads.
	generation uint64
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
func (f *tlsFiles) current() (*x509.CertPool, *x509bundle.Bundle, *tls.Certificate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refresh()
	return f.roots, f.bundle, f.cert
}

// generationNow refreshes the files and returns which load is in use.
func (f *tlsFiles) generationNow() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refresh()
	return f.generation
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
	return f.dialCounted(ctx, network, addr, nil)
}

// dialCounted is dial, counting the connection in open while it is open.
func (f *tlsFiles) dialCounted(ctx context.Context, network, addr string, open *atomic.Int64) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	roots, bundle, _ := f.current()
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
	if open != nil {
		// Below the TLS layer, so net/http still gets a *tls.Conn, which an
		// HTTP/2 upgrade through TLSNextProto requires.
		open.Add(1)
		raw = &countedConn{Conn: raw, open: open}
	}
	conn := tls.Client(raw, cfg)
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return conn, nil
}

// countedConn decrements its count once, when it closes.
type countedConn struct {
	net.Conn
	open *atomic.Int64
	once sync.Once
}

func (c *countedConn) Close() error {
	c.once.Do(func() { c.open.Add(-1) })
	return c.Conn.Close()
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
	rt, err := c.RoundTripper()
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
		rt, err := c.RoundTripper()
		if err != nil {
			return err
		}
		cfg.client = &http.Client{Transport: rt}
		return nil
	}
}
