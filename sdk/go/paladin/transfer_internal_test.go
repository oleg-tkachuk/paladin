package paladin

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// A presigned URL's required headers include Content-Length and Host, which
// the signature covers but the HTTP stack writes. The request carries the
// body's length and the URL's host whatever the map says, and every other
// required header as given.
func TestTransferLeavesContentLengthAndHostToTheTransport(t *testing.T) {
	const (
		checksumHeader = "X-Amz-Checksum-Sha256"
		checksum       = "signed"
		wrongLength    = 999
		wrongHost      = "elsewhere.example"
	)
	body := []byte("five!")
	type seen struct {
		length   int64
		host     string
		checksum string
		body     []byte
	}
	got := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- seen{r.ContentLength, r.Host, r.Header.Get(checksumHeader), b}
	}))
	t.Cleanup(srv.Close)
	tr, err := NewTransfer()
	if err != nil {
		t.Fatal(err)
	}
	signed := &commonv1.PresignedUrl{Url: srv.URL + "/o", Method: http.MethodPut, RequiredHeaders: map[string]string{
		"Content-Length": strconv.Itoa(wrongLength),
		"host":           wrongHost,
		checksumHeader:   checksum,
	}}
	resp, err := tr.do(context.Background(), http.MethodPut, signed, nil, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	s := <-got
	u, _ := url.Parse(srv.URL)
	if s.length != int64(len(body)) || !bytes.Equal(s.body, body) {
		t.Errorf("storage got %d bytes, Content-Length %d; want the body's %d", len(s.body), s.length, len(body))
	}
	if s.host != u.Host {
		t.Errorf("Host = %q, want the signed URL's %q", s.host, u.Host)
	}
	if s.checksum != checksum {
		t.Errorf("%s = %q, want the required %q", checksumHeader, s.checksum, checksum)
	}
}
