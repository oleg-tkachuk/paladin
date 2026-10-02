package paladin_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// Certificates for the tests: valid for an hour, which outlasts any run.
const (
	certLifetime = time.Hour
	serverID     = "spiffe://test.example/ns/paladin/sa/paladin-core"
	// reloadNow re-reads the TLS files on every connection.
	reloadNow = time.Nanosecond
	// unknownTLSVersion is a version number no TLS release has.
	unknownTLSVersion = 0x0399
)

var serial = struct {
	sync.Mutex
	n int64
}{}

func nextSerial() *big.Int {
	serial.Lock()
	defer serial.Unlock()
	serial.n++
	return big.NewInt(serial.n)
}

type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newAuthority(t *testing.T) *authority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: nextSerial(), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(certLifetime),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &authority{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// leaf is an issued certificate, as PEM and as a tls.Certificate.
type leaf struct {
	certPEM, keyPEM []byte
	pair            tls.Certificate
	serial          *big.Int
}

func (a *authority) issue(t *testing.T, client bool, ips []net.IP, dns []string, uris ...string) leaf {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	usage := x509.ExtKeyUsageServerAuth
	if client {
		usage = x509.ExtKeyUsageClientAuth
	}
	tmpl := &x509.Certificate{
		SerialNumber: nextSerial(), Subject: pkix.Name{CommonName: "test leaf"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(certLifetime),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
		IPAddresses: ips, DNSNames: dns,
	}
	for _, u := range uris {
		parsed, _ := url.Parse(u)
		tmpl.URIs = append(tmpl.URIs, parsed)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	l := leaf{
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		serial:  tmpl.SerialNumber,
	}
	l.pair, err = tls.X509KeyPair(l.certPEM, l.keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

var loopback = []net.IP{net.IPv4(127, 0, 0, 1)}

// files writes the CA bundle and a client pair to a directory, and returns
// their paths.
type files struct{ ca, cert, key string }

func writeFiles(t *testing.T, dir string, ca []byte, client leaf) files {
	t.Helper()
	f := files{filepath.Join(dir, "ca.pem"), filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")}
	for path, data := range map[string][]byte{f.ca: ca, f.cert: client.certPEM, f.key: client.keyPEM} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// mtlsHealth serves the IAM health service over TLS that requires a client
// certificate from ca, and records the serial of each one it was shown. It
// closes every connection, so each call is a new handshake.
type mtlsHealth struct {
	paladiniamv1connect.UnimplementedHealthServiceHandler
	mu      sync.Mutex
	serials []string
}

func (h *mtlsHealth) GetVersion(_ context.Context, req *connect.Request[iamv1.GetVersionRequest]) (*connect.Response[iamv1.VersionInfo], error) {
	return connect.NewResponse(&iamv1.VersionInfo{}), nil
}

func serveMTLS(t *testing.T, ca *authority, server leaf) (*mtlsHealth, string) {
	t.Helper()
	return serveMTLSWith(t, ca, server, func(*httptest.Server) {})
}

// serveMTLSWith is serveMTLS with the server adjusted before it starts.
func serveMTLSWith(t *testing.T, ca *authority, server leaf, adjust func(*httptest.Server)) (*mtlsHealth, string) {
	t.Helper()
	h := &mtlsHealth{}
	mux := http.NewServeMux()
	path, handler := paladiniamv1connect.NewHealthServiceHandler(h)
	mux.Handle(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.serials = append(h.serials, r.TLS.PeerCertificates[0].SerialNumber.String())
		h.mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	srv := httptest.NewUnstartedServer(mux)
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{server.pair},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS12,
	}
	srv.Config.SetKeepAlivesEnabled(false)
	adjust(srv)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return h, srv.URL
}

func getVersion(t *testing.T, url string, cfg paladin.TLS) error {
	t.Helper()
	p, err := paladin.Connect(paladin.Endpoints{IAM: url}, paladin.WithTLS(cfg))
	if err != nil {
		return err
	}
	_, err = p.IAM.Health.GetVersion(context.Background(), connect.NewRequest(&iamv1.GetVersionRequest{}))
	return err
}

func TestTLSVerifiesTheServerAndPresentsTheClientCertificate(t *testing.T) {
	ca := newAuthority(t)
	other := newAuthority(t)
	client := ca.issue(t, true, nil, nil)
	f := writeFiles(t, t.TempDir(), ca.pem, client)
	otherCA := writeFiles(t, t.TempDir(), other.pem, client)

	cases := []struct {
		name   string
		server leaf
		cfg    paladin.TLS
		ok     bool
	}{
		{"its IP", ca.issue(t, false, loopback, nil), paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key}, true},
		{"a CA it does not chain to", ca.issue(t, false, loopback, nil), paladin.TLS{CAFile: otherCA.ca, CertFile: f.cert, KeyFile: f.key}, false},
		{"a name that is not the host", ca.issue(t, false, nil, []string{"elsewhere.example"}), paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key}, false},
		{"the SPIFFE ID, no host name", ca.issue(t, false, nil, nil, serverID), paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key, ServerID: serverID}, true},
		{"another SPIFFE ID", ca.issue(t, false, loopback, nil, "spiffe://test.example/ns/x/sa/y"), paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key, ServerID: serverID}, false},
		{"a SPIFFE ID from another CA", other.issue(t, false, nil, nil, serverID), paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key, ServerID: serverID}, false},
		{"no client certificate", ca.issue(t, false, loopback, nil), paladin.TLS{CAFile: f.ca}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, url := serveMTLS(t, ca, tc.server)
			err := getVersion(t, url, tc.cfg)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
			if tc.ok && (len(h.serials) != 1 || h.serials[0] != client.serial.String()) {
				t.Errorf("server saw client serials %v, want [%s]", h.serials, client.serial)
			}
		})
	}
}

func TestTLSRefusesTheServerIDMismatchWithItsError(t *testing.T) {
	ca := newAuthority(t)
	f := writeFiles(t, t.TempDir(), ca.pem, ca.issue(t, true, nil, nil))
	_, url := serveMTLS(t, ca, ca.issue(t, false, loopback, nil, "spiffe://test.example/other"))
	err := getVersion(t, url, paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key, ServerID: serverID})
	if !errors.Is(err, paladin.ErrServerID) {
		t.Fatalf("err = %v, want ErrServerID", err)
	}
}

func TestTLSVerifyPeerCanRefuse(t *testing.T) {
	ca := newAuthority(t)
	f := writeFiles(t, t.TempDir(), ca.pem, ca.issue(t, true, nil, nil))
	_, url := serveMTLS(t, ca, ca.issue(t, false, loopback, nil))
	refused := errors.New("not this one")
	var saw *x509.Certificate
	err := getVersion(t, url, paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key,
		VerifyPeer: func(c *x509.Certificate) error { saw = c; return refused }})
	if !errors.Is(err, refused) || saw == nil {
		t.Fatalf("err = %v, leaf seen %v; want the hook's error, after it saw the leaf", err, saw != nil)
	}
}

func TestTLSPicksUpARotatedClientCertificate(t *testing.T) {
	ca := newAuthority(t)
	first, second := ca.issue(t, true, nil, nil), ca.issue(t, true, nil, nil)
	dir := t.TempDir()
	f := writeFiles(t, dir, ca.pem, first)
	h, url := serveMTLS(t, ca, ca.issue(t, false, loopback, nil))
	p, err := paladin.Connect(paladin.Endpoints{IAM: url},
		paladin.WithTLS(paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key, ReloadInterval: reloadNow}))
	if err != nil {
		t.Fatal(err)
	}
	call := func() {
		t.Helper()
		if _, err := p.IAM.Health.GetVersion(context.Background(), connect.NewRequest(&iamv1.GetVersionRequest{})); err != nil {
			t.Fatal(err)
		}
	}
	call()
	writeFiles(t, dir, ca.pem, second)
	// A rewrite within the file system's timestamp granularity would look
	// unchanged; move the times on explicitly.
	later := time.Now().Add(time.Minute)
	for _, path := range []string{f.cert, f.key} {
		if err := os.Chtimes(path, later, later); err != nil {
			t.Fatal(err)
		}
	}
	call()
	want := []string{first.serial.String(), second.serial.String()}
	if len(h.serials) != 2 || h.serials[0] != want[0] || h.serials[1] != want[1] {
		t.Errorf("server saw %v, want %v: the rotated certificate was not picked up", h.serials, want)
	}
}

func TestTLSKeepsTheLastGoodPairWhileARotationIsHalfWritten(t *testing.T) {
	ca := newAuthority(t)
	first, second := ca.issue(t, true, nil, nil), ca.issue(t, true, nil, nil)
	dir := t.TempDir()
	f := writeFiles(t, dir, ca.pem, first)
	h, url := serveMTLS(t, ca, ca.issue(t, false, loopback, nil))
	p, err := paladin.Connect(paladin.Endpoints{IAM: url},
		paladin.WithTLS(paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key, ReloadInterval: reloadNow}))
	if err != nil {
		t.Fatal(err)
	}
	// The new certificate, but not yet its key: the pair does not match.
	if err := os.WriteFile(f.cert, second.certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(f.cert, later, later)
	if _, err := p.IAM.Health.GetVersion(context.Background(), connect.NewRequest(&iamv1.GetVersionRequest{})); err != nil {
		t.Fatalf("a half-written rotation broke the client: %v", err)
	}
	if len(h.serials) != 1 || h.serials[0] != first.serial.String() {
		t.Errorf("server saw %v, want the previous certificate %s", h.serials, first.serial)
	}
}

func TestTLSConfigErrors(t *testing.T) {
	ca := newAuthority(t)
	f := writeFiles(t, t.TempDir(), ca.pem, ca.issue(t, true, nil, nil))
	notPEM := filepath.Join(t.TempDir(), "ca.pem")
	_ = os.WriteFile(notPEM, []byte("not a certificate"), 0o600)
	cases := []struct {
		name string
		cfg  paladin.TLS
		want error
	}{
		{"cert without key", paladin.TLS{CertFile: f.cert}, paladin.ErrTLSKeyPair},
		{"key without cert", paladin.TLS{KeyFile: f.key}, paladin.ErrTLSKeyPair},
		{"a CA file with no certificate", paladin.TLS{CAFile: notPEM}, paladin.ErrNoCA},
		{"a missing file", paladin.TLS{CAFile: filepath.Join(t.TempDir(), "absent.pem")}, os.ErrNotExist},
		{"a server ID without its bundle", paladin.TLS{ServerID: serverID}, paladin.ErrServerIDNeedsCA},
		{"a server ID that is not a SPIFFE ID", paladin.TLS{CAFile: f.ca, ServerID: "https://paladin.example"}, paladin.ErrServerID},
		{"a minimum below TLS 1.2", paladin.TLS{MinVersion: tls.VersionTLS11}, paladin.ErrTLSMinVersion},
		{"a minimum crypto/tls does not know", paladin.TLS{MinVersion: unknownTLSVersion}, paladin.ErrTLSMinVersion},
	}
	for _, tc := range cases {
		if _, err := tc.cfg.Transport(); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if _, err := paladin.New("https://paladin.example", paladin.WithTLS(paladin.TLS{CAFile: f.ca}),
		paladin.WithHTTPClient(http.DefaultClient)); !errors.Is(err, paladin.ErrTLSAndHTTP) {
		t.Errorf("WithTLS with WithHTTPClient: err = %v, want ErrTLSAndHTTP", err)
	}
}

func TestTransferTLSReachesStorageThatRequiresAClientCertificate(t *testing.T) {
	ca := newAuthority(t)
	client := ca.issue(t, true, nil, nil)
	f := writeFiles(t, t.TempDir(), ca.pem, client)
	st := &storage{blobs: map[string][]byte{}, headersSeen: map[string]string{}}
	srv := httptest.NewUnstartedServer(st)
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{ca.issue(t, false, loopback, nil).pair},
		ClientAuth:   tls.RequireAndVerifyClientCert, ClientCAs: pool, MinVersion: tls.VersionTLS12,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	dp := &dataPlane{storageURL: srv.URL, completed: map[string]string{}, checksums: map[string]string{}}

	tr := mustTransfer(t, paladin.WithTransferTLS(paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key}))
	body := []byte("over mutual TLS")
	obj := upload(t, connectData(t, dp, paladin.WithTransfer(tr)), "k", body)
	if !bytes.Equal(st.blobs["/k?"], body) || obj.GetName() == "" {
		t.Fatalf("storage holds %q, want %q", st.blobs["/k?"], body)
	}

	// Without the client certificate, storage refuses the handshake.
	plain := mustTransfer(t, paladin.WithTransferTLS(paladin.TLS{CAFile: f.ca}))
	if _, err := paladin.Upload(context.Background(), connectData(t, dp, paladin.WithTransfer(plain)), paladin.UploadInput{
		Parent: testParent, Key: "k2", Size: 1, Body: bytes.NewReader([]byte("x")),
	}, paladin.UploadOptions{}); err == nil {
		t.Fatal("storage accepted a transfer with no client certificate")
	}
}

func TestTLSPicksUpARotatedCABundle(t *testing.T) {
	oldCA, newCA := newAuthority(t), newAuthority(t)
	dir := t.TempDir()
	f := writeFiles(t, dir, oldCA.pem, newCA.issue(t, true, nil, nil))
	_, url := serveMTLS(t, newCA, newCA.issue(t, false, loopback, nil))
	p, err := paladin.Connect(paladin.Endpoints{IAM: url},
		paladin.WithTLS(paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key, ReloadInterval: reloadNow}))
	if err != nil {
		t.Fatal(err)
	}
	call := func() error {
		_, err := p.IAM.Health.GetVersion(context.Background(), connect.NewRequest(&iamv1.GetVersionRequest{}))
		return err
	}
	if call() == nil {
		t.Fatal("a server from a CA not yet in the bundle was trusted")
	}
	if err := os.WriteFile(f.ca, append(append([]byte(nil), oldCA.pem...), newCA.pem...), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(f.ca, later, later)
	if err := call(); err != nil {
		t.Fatalf("the rotated bundle was not picked up: %v", err)
	}
}

func TestTLSMinVersion(t *testing.T) {
	ca := newAuthority(t)
	f := writeFiles(t, t.TempDir(), ca.pem, ca.issue(t, true, nil, nil))
	tls12Only := func(srv *httptest.Server) { srv.TLS.MaxVersion = tls.VersionTLS12 }
	cases := []struct {
		name string
		min  uint16
		ok   bool
	}{
		{"the default reaches a TLS 1.2 server", 0, true},
		{"1.2 reaches a TLS 1.2 server", tls.VersionTLS12, true},
		{"1.3 refuses a TLS 1.2 server", tls.VersionTLS13, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, url := serveMTLSWith(t, ca, ca.issue(t, false, loopback, nil), tls12Only)
			err := getVersion(t, url, paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key, MinVersion: tc.min})
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// A connection kept alive across a rotation is closed once idle, so the next
// call presents the new certificate rather than reusing the old handshake.
func TestTLSRetiresAKeptAliveConnectionAfterARotation(t *testing.T) {
	ca := newAuthority(t)
	first, second := ca.issue(t, true, nil, nil), ca.issue(t, true, nil, nil)
	dir := t.TempDir()
	f := writeFiles(t, dir, ca.pem, first)
	keepAlive := func(srv *httptest.Server) { srv.Config.SetKeepAlivesEnabled(true) }
	h, url := serveMTLSWith(t, ca, ca.issue(t, false, loopback, nil), keepAlive)
	p, err := paladin.Connect(paladin.Endpoints{IAM: url},
		paladin.WithTLS(paladin.TLS{CAFile: f.ca, CertFile: f.cert, KeyFile: f.key, ReloadInterval: reloadNow}))
	if err != nil {
		t.Fatal(err)
	}
	call := func() {
		t.Helper()
		if _, err := p.IAM.Health.GetVersion(context.Background(), connect.NewRequest(&iamv1.GetVersionRequest{})); err != nil {
			t.Fatal(err)
		}
	}
	call()
	call() // the same connection, kept alive
	writeFiles(t, dir, ca.pem, second)
	later := time.Now().Add(time.Minute)
	for _, path := range []string{f.cert, f.key} {
		if err := os.Chtimes(path, later, later); err != nil {
			t.Fatal(err)
		}
	}
	call()
	want := []string{first.serial.String(), first.serial.String(), second.serial.String()}
	if !slices.Equal(h.serials, want) {
		t.Errorf("server saw %v, want %v: the connection from before the rotation was reused", h.serials, want)
	}
}
