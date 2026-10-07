package paladin_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

const (
	// closeWait bounds how long a drained connection may take to close;
	// closePoll is how often the server is asked.
	closeWait = 5 * time.Second
	closePoll = 10 * time.Millisecond
)

// protocols the rotation tests run over: the server's EnableHTTP2.
var protocols = []struct {
	name string
	h2   bool
}{{"HTTP/1.1", false}, {"HTTP/2", true}}

// counting is a caller's own RoundTripper around the SDK's: it sees every
// request, as a circuit breaker or a metrics wrapper would.
type counting struct {
	next http.RoundTripper
	n    atomic.Int64
}

func (c *counting) RoundTrip(req *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return c.next.RoundTrip(req)
}

// rotatingServer is an mTLS server whose own certificate can change, which
// trusts client certificates from either of two CAs, and which records, per
// request, the client serial and the connection it came on.
type rotatingServer struct {
	url  string
	leaf atomic.Pointer[tls.Certificate]

	mu      sync.Mutex
	serials []string
	conns   []string // the remote address, one per request
	closed  map[string]bool
	// block, when set, holds the next request until it is closed.
	block   chan struct{}
	entered chan struct{}
}

func newRotatingServer(t *testing.T, h2 bool, server leaf, cas ...*authority) *rotatingServer {
	t.Helper()
	s := &rotatingServer{closed: map[string]bool{}}
	s.leaf.Store(&server.pair)
	pool := x509.NewCertPool()
	for _, ca := range cas {
		pool.AddCert(ca.cert)
	}
	mux := http.NewServeMux()
	handler := connectHandler(func(s *connect.Server) { paladiniamv1connect.RegisterHealthServiceHandler(s, &mtlsHealth{}) })
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.serials = append(s.serials, r.TLS.PeerCertificates[0].SerialNumber.String())
		s.conns = append(s.conns, r.RemoteAddr)
		block, entered := s.block, s.entered
		s.block, s.entered = nil, nil
		s.mu.Unlock()
		if block != nil {
			close(entered)
			<-block
		}
		handler.ServeHTTP(w, r)
	}))
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = h2
	srv.Config.ConnState = func(c net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			s.mu.Lock()
			s.closed[c.RemoteAddr().String()] = true
			s.mu.Unlock()
		}
	}
	base := &tls.Config{
		Certificates: []tls.Certificate{server.pair},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS12,
	}
	// Per handshake, so a re-issued server certificate is served from then
	// on; GetCertificate would not be asked, a client dialling an IP sending
	// no SNI.
	srv.TLS = base.Clone()
	srv.TLS.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		c := base.Clone()
		c.Certificates = []tls.Certificate{*s.leaf.Load()}
		c.NextProtos = srv.TLS.NextProtos
		return c, nil
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// holdNext makes the next request wait until the returned release is called;
// entered is closed once that request is in the handler.
func (s *rotatingServer) holdNext() (entered <-chan struct{}, release func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	block, in := make(chan struct{}), make(chan struct{})
	s.block, s.entered = block, in
	return in, func() { close(block) }
}

// closedWithin reports whether the connection from addr closes within d.
func (s *rotatingServer) closedWithin(addr string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		done := s.closed[addr]
		s.mu.Unlock()
		if done {
			return true
		}
		time.Sleep(closePoll)
	}
	return false
}

func (s *rotatingServer) seen() (serials, conns []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.serials...), append([]string(nil), s.conns...)
}

// rotate rewrites the CA bundle, certificate and key on disk, as cert-manager
// does on a re-issue, and moves their times past the reload check's
// granularity.
func rotate(t *testing.T, f files, ca []byte, client leaf) {
	t.Helper()
	for path, data := range map[string][]byte{f.ca: ca, f.cert: client.certPEM, f.key: client.keyPEM} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	later := time.Now().Add(time.Minute)
	for _, path := range []string{f.ca, f.cert, f.key} {
		if err := os.Chtimes(path, later, later); err != nil {
			t.Fatal(err)
		}
	}
}

func getVersionWith(p *paladin.Paladin) error {
	_, err := p.IAM.Health.GetVersion(context.Background(), &iamv1.GetVersionRequest{})
	return err
}

// A caller's own RoundTripper around TLS.RoundTripper gets rotation: after the
// CA, the certificate and the key are re-issued, the next request dials a new
// connection, with the new certificate, and the wrapper sees every request.
func TestRoundTripperWrappedByTheCallerRetiresRotatedConnections(t *testing.T) {
	for _, proto := range protocols {
		t.Run(proto.name, func(t *testing.T) {
			oldCA, newCA := newAuthority(t), newAuthority(t)
			first, second := oldCA.issue(t, true, nil, nil), newCA.issue(t, true, nil, nil)
			srv := newRotatingServer(t, proto.h2, oldCA.issue(t, false, loopback, nil), oldCA, newCA)
			f := writeFiles(t, t.TempDir(), oldCA.pem, first)

			rt, err := paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key, ReloadInterval: reloadNow}.RoundTripper()
			if err != nil {
				t.Fatal(err)
			}
			rt.ResponseHeaderTimeout = 0 // an RPC client, as WithTLS builds it
			wrapper := &counting{next: rt}
			p, err := paladin.Connect(paladin.Endpoints{IAM: srv.url}, paladin.WithHTTPClient(&http.Client{Transport: wrapper}))
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := getVersionWith(p); err != nil {
					t.Fatal(err)
				}
			}
			// The server re-issued from the new CA too; the client trusts only it.
			reissued := newCA.issue(t, false, loopback, nil).pair
			srv.leaf.Store(&reissued)
			rotate(t, f, newCA.pem, second)
			if err := getVersionWith(p); err != nil {
				t.Fatalf("after the rotation: %v", err)
			}

			serials, conns := srv.seen()
			wantSerials := []string{first.serial.String(), first.serial.String(), second.serial.String()}
			if !slices.Equal(serials, wantSerials) {
				t.Errorf("server saw client serials %v, want %v", serials, wantSerials)
			}
			if conns[0] != conns[1] || conns[2] == conns[1] {
				t.Errorf("connections %v: want the first reused, then a new one after the rotation", conns)
			}
			if got := wrapper.n.Load(); got != int64(len(wantSerials)) {
				t.Errorf("the wrapper saw %d requests, want %d", got, len(wantSerials))
			}
			var _ interface{ CloseIdleConnections() } = rt
		})
	}
}

// A request in flight when the files rotate finishes on its connection, with
// the old certificate; the next one after it uses the new.
func TestRoundTripperLetsARequestInFlightFinishOnItsConnection(t *testing.T) {
	for _, proto := range protocols {
		t.Run(proto.name, func(t *testing.T) {
			ca := newAuthority(t)
			first, second := ca.issue(t, true, nil, nil), ca.issue(t, true, nil, nil)
			srv := newRotatingServer(t, proto.h2, ca.issue(t, false, loopback, nil), ca)
			f := writeFiles(t, t.TempDir(), ca.pem, first)
			rt, err := paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key, ReloadInterval: reloadNow}.RoundTripper()
			if err != nil {
				t.Fatal(err)
			}
			rt.ResponseHeaderTimeout = 0
			p, err := paladin.Connect(paladin.Endpoints{IAM: srv.url}, paladin.WithHTTPClient(&http.Client{Transport: &counting{next: rt}}))
			if err != nil {
				t.Fatal(err)
			}

			entered, release := srv.holdNext()
			inFlight := make(chan error, 1)
			go func() { inFlight <- getVersionWith(p) }()
			select {
			case <-entered:
			case err := <-inFlight:
				t.Fatalf("the first request did not reach the server: %v", err)
			}
			rotate(t, f, ca.pem, second)
			// While the first is held, another request goes out: it may not
			// reuse the busy connection's old certificate.
			if err := getVersionWith(p); err != nil {
				t.Fatalf("a request during the rotation: %v", err)
			}
			release()
			if err := <-inFlight; err != nil {
				t.Fatalf("the request in flight was cut off: %v", err)
			}
			if err := getVersionWith(p); err != nil {
				t.Fatal(err)
			}
			serials, conns := srv.seen()
			want := []string{first.serial.String(), second.serial.String(), second.serial.String()}
			if !slices.Equal(serials, want) {
				t.Errorf("server saw %v, want %v: in flight on the old certificate, everything after on the new", serials, want)
			}
			if !srv.closedWithin(conns[0], closeWait) {
				t.Errorf("the connection with the old certificate is still open after its last request")
			}
		})
	}
}

// The same holds for presigned requests: a Transfer built on a wrapped
// RoundTripper presents the rotated certificate to storage.
func TestTransferOnAWrappedRoundTripperPresentsTheRotatedCertificate(t *testing.T) {
	for _, proto := range protocols {
		t.Run(proto.name, func(t *testing.T) {
			ca := newAuthority(t)
			first, second := ca.issue(t, true, nil, nil), ca.issue(t, true, nil, nil)
			f := writeFiles(t, t.TempDir(), ca.pem, first)
			var (
				mu      sync.Mutex
				serials []string
			)
			st := &storage{blobs: map[string][]byte{}, headersSeen: map[string]string{}}
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				serials = append(serials, r.TLS.PeerCertificates[0].SerialNumber.String())
				mu.Unlock()
				st.ServeHTTP(w, r)
			}))
			pool := x509.NewCertPool()
			pool.AddCert(ca.cert)
			srv.EnableHTTP2 = proto.h2
			srv.TLS = &tls.Config{
				Certificates: []tls.Certificate{ca.issue(t, false, loopback, nil).pair},
				ClientAuth:   tls.RequireAndVerifyClientCert, ClientCAs: pool, MinVersion: tls.VersionTLS12,
			}
			srv.StartTLS()
			t.Cleanup(srv.Close)
			dp := &dataPlane{storageURL: srv.URL, completed: map[string]string{}, checksums: map[string]string{}}

			rt, err := paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key, ReloadInterval: reloadNow}.RoundTripper()
			if err != nil {
				t.Fatal(err)
			}
			wrapper := &counting{next: rt}
			tr := mustTransfer(t, paladin.WithTransferHTTPClient(&http.Client{Transport: wrapper}))
			p := connectData(t, dp, paladin.WithTransfer(tr))
			upload(t, p, "a", []byte("before"))
			rotate(t, f, ca.pem, second)
			upload(t, p, "b", []byte("after"))

			mu.Lock()
			defer mu.Unlock()
			want := []string{first.serial.String(), second.serial.String()}
			if !slices.Equal(serials, want) {
				t.Errorf("storage saw %v, want %v", serials, want)
			}
			if got := wrapper.n.Load(); got != int64(len(want)) {
				t.Errorf("the wrapper saw %d requests, want %d", got, len(want))
			}
			if !bytes.Equal(st.blobs["/b?"], []byte("after")) {
				t.Errorf("storage holds %q", st.blobs["/b?"])
			}
		})
	}
}
