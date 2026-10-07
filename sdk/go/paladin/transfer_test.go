package paladin_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // the digest the fake object records
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// streamOnly hides every interface but io.Reader, as a pipe would.
type streamOnly struct{ r io.Reader }

func (s streamOnly) Read(p []byte) (int, error) { return s.r.Read(p) }

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func sha256Of(b []byte) string { sum := sha256.Sum256(b); return b64(sum[:]) }

func mustTransfer(t *testing.T, opts ...paladin.TransferOption) *paladin.Transfer {
	t.Helper()
	tr, err := paladin.NewTransfer(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func upload(t *testing.T, data *paladin.DataPlane, key string, body []byte) *datav1.Object {
	t.Helper()
	obj, err := paladin.Upload(context.Background(), data, paladin.UploadInput{
		Parent: testParent, Key: key, ContentType: "text/plain", Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

func readAll(t *testing.T, data *paladin.DataPlane, name string, opts paladin.DownloadOptions) ([]byte, error) {
	t.Helper()
	r, err := paladin.Download(context.Background(), data, name, opts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

// The signed origin is a public name nothing resolves; the transfer must reach
// the fake storage at its real address and still name the signed host.
const signedOrigin = "http://storage.public.example:9000"

func TestSplitHorizonSendsToTheInternalAddressWithTheSignedHost(t *testing.T) {
	st := &storage{blobs: map[string][]byte{}, headersSeen: map[string]string{}}
	dp, internal := newTransferAt(t, st, signedOrigin)
	data := connectData(t, dp, paladin.WithTransfer(mustTransfer(t, paladin.WithSplitHorizon(signedOrigin, internal))))

	body := []byte("split horizon")
	obj := upload(t, data, "k", body)
	got, err := readAll(t, data, obj.GetName(), paladin.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("downloaded %q, want %q", got, body)
	}
	signedHost := strings.TrimPrefix(signedOrigin, "http://")
	for _, h := range st.hosts {
		if h != signedHost {
			t.Errorf("storage saw Host %q, want the signed %q", h, signedHost)
		}
	}
	if len(st.hosts) != 2 {
		t.Errorf("storage saw %d requests, want the PUT and the GET", len(st.hosts))
	}
}

func TestTransferRewriteKeepsTheSignedHost(t *testing.T) {
	st := &storage{blobs: map[string][]byte{}, headersSeen: map[string]string{}}
	dp, internal := newTransferAt(t, st, signedOrigin)
	to, _ := url.Parse(internal)
	rewrite := func(u *url.URL) *url.URL { out := *u; out.Host = to.Host; return &out }
	data := connectData(t, dp, paladin.WithTransfer(mustTransfer(t, paladin.WithTransferRewrite(rewrite))))
	upload(t, data, "k", []byte("x"))
	if len(st.hosts) != 1 || st.hosts[0] != strings.TrimPrefix(signedOrigin, "http://") {
		t.Errorf("hosts = %v, want the signed host", st.hosts)
	}
}

func TestSplitHorizonLeavesOtherOriginsAlone(t *testing.T) {
	data, _, st := newTransfer(t, 0, paladin.WithTransfer(mustTransfer(t,
		paladin.WithSplitHorizon("http://elsewhere.example", "http://127.0.0.1:1"))))
	upload(t, data, "k", []byte("x"))
	if len(st.hosts) != 1 {
		t.Fatalf("storage saw %d requests, want 1: the URL was rewritten though its origin did not match", len(st.hosts))
	}
}

func TestNewTransferRefusesAnInvalidOrigin(t *testing.T) {
	for _, origin := range []string{"", "storage:9000", "ftp://storage", "http://storage/path", "http://storage?x=1"} {
		if _, err := paladin.NewTransfer(paladin.WithSplitHorizon(origin, "http://ok")); !errors.Is(err, paladin.ErrInvalidOrigin) {
			t.Errorf("origin %q: err = %v, want ErrInvalidOrigin", origin, err)
		}
	}
}

func TestTransferRefusesARedirect(t *testing.T) {
	data, _, st := newTransfer(t, 0)
	obj := upload(t, data, "k", []byte("x"))
	followed := false
	st.redirectTo = newStorageServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	_, err := readAll(t, data, obj.GetName(), paladin.DownloadOptions{})
	var te *paladin.TransferError
	if !errors.As(err, &te) || te.Status != http.StatusTemporaryRedirect {
		t.Fatalf("err = %v, want a TransferError with 307", err)
	}
	if followed {
		t.Error("the redirect was followed, carrying the signed headers to another host")
	}
}

func TestTransferErrorQuotesACappedBodyAndTheHost(t *testing.T) {
	const limit = 512
	srv := newStorageServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(bytes.Repeat([]byte("x"), limit*2))
	}))
	dp := &dataPlane{storageURL: srv, completed: map[string]string{}, checksums: map[string]string{}}
	data := connectData(t, dp)
	_, err := readAll(t, data, testParent+"/objects/k", paladin.DownloadOptions{})
	var te *paladin.TransferError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want a TransferError", err)
	}
	u, _ := url.Parse(srv)
	if te.Status != http.StatusForbidden || len(te.Body) != limit || te.Host != u.Host || te.Method != http.MethodGet {
		t.Errorf("TransferError = %s %s %d, %d body bytes; want GET %s 403, %d", te.Method, te.Host, te.Status, len(te.Body), u.Host, limit)
	}
}

// countingTransport counts the requests it carries.
type countingTransport struct{ n int }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n++
	return http.DefaultTransport.RoundTrip(r)
}

func TestTransferUsesTheGivenHTTPClient(t *testing.T) {
	ct := &countingTransport{}
	data, _, _ := newTransfer(t, 0, paladin.WithTransfer(mustTransfer(t,
		paladin.WithTransferHTTPClient(&http.Client{Transport: ct}))))
	obj := upload(t, data, "k", []byte("x"))
	if _, err := readAll(t, data, obj.GetName(), paladin.DownloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if ct.n != 2 {
		t.Errorf("the given client carried %d requests, want 2", ct.n)
	}
}

func TestUploadFromAStream(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		threshold int64
		partSize  int64
		parts     []string
	}{
		{"one PUT", "a stream of bytes", 0, 0, nil},
		{"multipart", "0123456789", 8, 4, []string{"0123", "4567", "89"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, dp, st := newTransfer(t, tc.partSize)
			obj, err := paladin.Upload(context.Background(), data, paladin.UploadInput{ContentType: testContentType,
				Parent: testParent, Key: "s", Size: int64(len(tc.body)), Stream: streamOnly{strings.NewReader(tc.body)},
			}, paladin.UploadOptions{MultipartThreshold: tc.threshold})
			if err != nil {
				t.Fatal(err)
			}
			if tc.parts == nil {
				if got := string(st.blobs["/s?"]); got != tc.body {
					t.Errorf("stored %q, want %q", got, tc.body)
				}
				if got := dp.checksums[obj.GetName()]; got != sha256Of([]byte(tc.body)) {
					t.Errorf("CompleteObject checksum = %q, want the content's SHA-256", got)
				}
				return
			}
			for i, chunk := range tc.parts {
				if got := string(st.blobs["/mp?part="+string(rune('1'+i))]); got != chunk {
					t.Errorf("part %d stored %q, want %q", i+1, got, chunk)
				}
			}
		})
	}
}

func TestUploadFailsOnAShortStream(t *testing.T) {
	data, dp, _ := newTransfer(t, 4)
	_, err := paladin.Upload(context.Background(), data, paladin.UploadInput{ContentType: testContentType,
		Parent: testParent, Key: "s", Size: 10, Stream: streamOnly{strings.NewReader("012345")},
	}, paladin.UploadOptions{MultipartThreshold: 8})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
	if dp.aborted != 1 {
		t.Errorf("aborted %d times, want 1", dp.aborted)
	}
}

func TestUploadNeedsExactlyOneBody(t *testing.T) {
	data, _, _ := newTransfer(t, 0)
	for name, in := range map[string]paladin.UploadInput{
		"neither": {ContentType: testContentType, Parent: testParent, Size: 1},
		"both":    {ContentType: testContentType, Parent: testParent, Size: 1, Body: bytes.NewReader([]byte("x")), Stream: strings.NewReader("x")},
	} {
		if _, err := paladin.Upload(context.Background(), data, in, paladin.UploadOptions{}); !errors.Is(err, paladin.ErrUploadBody) {
			t.Errorf("%s: err = %v, want ErrUploadBody", name, err)
		}
	}
}

func TestDownloadReadsARange(t *testing.T) {
	data, _, _ := newTransfer(t, 0)
	obj := upload(t, data, "k", []byte("0123456789"))
	cases := []struct {
		opts paladin.DownloadOptions
		want string
	}{
		{paladin.DownloadOptions{Offset: 2, Length: 3}, "234"},
		{paladin.DownloadOptions{Offset: 7}, "789"},
		{paladin.DownloadOptions{Length: 2}, "01"},
	}
	for _, tc := range cases {
		got, err := readAll(t, data, obj.GetName(), tc.opts)
		if err != nil || string(got) != tc.want {
			t.Errorf("%+v: got %q, %v; want %q", tc.opts, got, err, tc.want)
		}
	}
}

func TestDownloadRefusesAnIgnoredRange(t *testing.T) {
	data, _, st := newTransfer(t, 0)
	obj := upload(t, data, "k", []byte("0123456789"))
	st.ignoreRange = true
	if _, err := readAll(t, data, obj.GetName(), paladin.DownloadOptions{Offset: 2, Length: 3}); !errors.Is(err, paladin.ErrRangeIgnored) {
		t.Fatalf("err = %v, want ErrRangeIgnored", err)
	}
}

func TestDownloadRefusesANegativeRange(t *testing.T) {
	data, _, _ := newTransfer(t, 0)
	if _, err := paladin.Download(context.Background(), data, "x", paladin.DownloadOptions{Offset: -1}); !errors.Is(err, paladin.ErrInvalidRange) {
		t.Fatalf("err = %v, want ErrInvalidRange", err)
	}
}

func TestDownloadWithoutAURL(t *testing.T) {
	data, dp, _ := newTransfer(t, 0)
	dp.noURL = true
	if _, err := paladin.Download(context.Background(), data, testParent+"/objects/k", paladin.DownloadOptions{}); !errors.Is(err, paladin.ErrNoDownloadURL) {
		t.Fatalf("err = %v, want ErrNoDownloadURL", err)
	}
}

func TestDownloadVerifiesTheWholeObject(t *testing.T) {
	body := []byte("verified content")
	md5Sum := md5.Sum(body) //nolint:gosec // the fake's recorded digest
	crc := crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli))
	crcBytes := []byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)}
	wrong := sha256Of([]byte("something else"))
	cases := []struct {
		name     string
		size     int64
		algo     string
		value    string
		opts     paladin.DownloadOptions
		mismatch string // IntegrityError.What, or "" for a clean read
	}{
		{"sha256", int64(len(body)), paladin.ChecksumSHA256, sha256Of(body), paladin.DownloadOptions{}, ""},
		{"crc32c", int64(len(body)), paladin.ChecksumCRC32C, b64(crcBytes), paladin.DownloadOptions{}, ""},
		{"md5", int64(len(body)), paladin.ChecksumMD5, b64(md5Sum[:]), paladin.DownloadOptions{}, ""},
		{"no checksum recorded", int64(len(body)), "", "", paladin.DownloadOptions{}, ""},
		{"multipart composite is skipped", int64(len(body)), paladin.ChecksumSHA256, "abc-3", paladin.DownloadOptions{}, ""},
		{"a digest of the wrong length is skipped", int64(len(body)), paladin.ChecksumSHA256, "AAAA", paladin.DownloadOptions{}, ""},
		{"unknown algorithm is skipped", int64(len(body)), "XXH3", "AAAA", paladin.DownloadOptions{}, ""},
		{"wrong checksum", int64(len(body)), paladin.ChecksumSHA256, wrong, paladin.DownloadOptions{}, paladin.ChecksumSHA256},
		{"wrong size", int64(len(body)) + 1, paladin.ChecksumSHA256, sha256Of(body), paladin.DownloadOptions{}, "size"},
		{"a range is not verified", int64(len(body)), paladin.ChecksumSHA256, wrong, paladin.DownloadOptions{Offset: 1}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, dp, _ := newTransfer(t, 0)
			obj := upload(t, data, "k", body)
			dp.described = &datav1.Object{Name: obj.GetName(), SizeBytes: tc.size, ContentType: "text/plain"}
			if tc.algo != "" {
				dp.described.Checksum = &datav1.ChecksumDigest{Algorithm: tc.algo, Value: tc.value}
			}
			got, err := readAll(t, data, obj.GetName(), tc.opts)
			var ie *paladin.IntegrityError
			switch {
			case tc.mismatch == "" && err != nil:
				t.Fatalf("err = %v, want a clean read", err)
			case tc.mismatch != "" && (!errors.As(err, &ie) || ie.What != tc.mismatch):
				t.Fatalf("err = %v, want an IntegrityError on %s", err, tc.mismatch)
			case tc.mismatch == "" && tc.opts == (paladin.DownloadOptions{}) && !bytes.Equal(got, body):
				t.Errorf("read %q, want %q", got, body)
			}
		})
	}
}

func TestDownloadReportsTheContentTypeAndLength(t *testing.T) {
	data, _, _ := newTransfer(t, 0)
	body := []byte("0123456789")
	obj := upload(t, data, "k", body)
	r, err := paladin.Download(context.Background(), data, obj.GetName(), paladin.DownloadOptions{Offset: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if r.ContentLength != int64(len(body))-4 || r.ContentType == "" {
		t.Errorf("ContentLength %d, ContentType %q; want 6 and a type", r.ContentLength, r.ContentType)
	}
}

// newStorageServer serves h and returns its URL.
func newStorageServer(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// newTransferAt serves st at an internal address, and returns it with a fake
// data plane that signs its URLs for signed.
func newTransferAt(t *testing.T, st *storage, signed string) (*dataPlane, string) {
	t.Helper()
	internal := newStorageServer(t, st)
	return &dataPlane{storageURL: signed, completed: map[string]string{}, checksums: map[string]string{}}, internal
}

// Consumers parsed expires_at_rfc3339 themselves; PresignExpiry is the one
// reading the SDK's own retry logic uses.
func TestPresignExpiry(t *testing.T) {
	want := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		raw    string
		want   time.Time
		wantOK bool
	}{
		{"an RFC 3339 instant", "2026-10-06T12:00:00Z", want, true},
		{"an offset", "2026-10-06T15:00:00+03:00", want, true},
		{"none sent", "", time.Time{}, false},
		{"not a time", "tomorrow", time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := paladin.PresignExpiry(&commonv1.PresignedUrl{ExpiresAtRfc3339: tc.raw})
			if ok != tc.wantOK || !got.Equal(tc.want) {
				t.Errorf("PresignExpiry(%q) = (%v, %v), want (%v, %v)", tc.raw, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
