package paladin

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // verifies a digest storage recorded; not a security boundary
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"net/http"
	"strconv"

	"connectrpc.com/connect"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

// The algorithms the server records an object's checksum under
// (ChecksumDigest.algorithm), each with the digest Download verifies it with.
const (
	ChecksumSHA256 = "SHA256"
	ChecksumCRC32C = "CRC32C"
	ChecksumMD5    = "MD5"
)

var checksums = map[string]func() hash.Hash{
	ChecksumSHA256: sha256.New,
	ChecksumCRC32C: func() hash.Hash { return crc32.New(crc32.MakeTable(crc32.Castagnoli)) },
	ChecksumMD5:    md5.New,
}

const (
	headerRange         = "Range"
	headerContentLength = "Content-Length"
	// unknownLength is ObjectReader.ContentLength when storage sent none.
	unknownLength = -1
)

// Errors from Download.
var (
	ErrNoDownloadURL = errors.New("paladin: the server returned no download URL")
	ErrInvalidRange  = errors.New("paladin: a range needs Offset >= 0 and Length >= 0")
	// ErrRangeIgnored is storage answering a range request with the whole
	// object: reading it as the range would return the wrong bytes.
	ErrRangeIgnored = errors.New("paladin: storage ignored the range and sent the whole object")
)

// IntegrityError is a download whose content does not match what the server
// recorded for the object: its size, or its checksum.
type IntegrityError struct {
	// What is "size" or the checksum algorithm, e.g. ChecksumSHA256.
	What string
	Want string
	Got  string
}

func (e *IntegrityError) Error() string {
	return fmt.Sprintf("paladin: downloaded content does not match the object's %s: want %s, got %s", e.What, e.Want, e.Got)
}

// integritySize is IntegrityError.What for a length mismatch.
const integritySize = "size"

// DownloadOptions select what Download reads; the zero value reads the whole
// object and verifies it.
type DownloadOptions struct {
	// Offset and Length select a byte range: Length bytes from Offset, or to
	// the end when Length is 0. A range is not verified — the recorded
	// checksum covers the whole object.
	Offset int64
	Length int64
}

func (o DownloadOptions) ranged() bool { return o.Offset != 0 || o.Length != 0 }

// header is the Range header value: bytes=first-last, inclusive.
func (o DownloadOptions) header() string {
	if o.Length == 0 {
		return fmt.Sprintf("bytes=%d-", o.Offset)
	}
	return fmt.Sprintf("bytes=%d-%d", o.Offset, o.Offset+o.Length-1)
}

// ObjectReader streams an object's content. The caller closes it. Reading a
// whole object verifies it at the end: the last Read returns an
// *IntegrityError instead of io.EOF when the size or the recorded checksum
// does not match.
type ObjectReader struct {
	// Object is the object as the server described it.
	Object *datav1.Object
	// ContentType is what storage sent, else the object's.
	ContentType string
	// ContentLength is how many bytes this reader returns, or -1 when storage
	// did not say.
	ContentLength int64

	body     io.ReadCloser
	read     int64
	wantSize int64 // -1: nothing to check
	digest   hash.Hash
	want     []byte
	algo     string
}

// Read reads the content, and verifies it at the end of a whole object.
func (r *ObjectReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	r.read += int64(n)
	if r.digest != nil {
		r.digest.Write(p[:n])
	}
	if errors.Is(err, io.EOF) {
		if verr := r.verify(); verr != nil {
			return n, verr
		}
	}
	return n, err
}

func (r *ObjectReader) verify() error {
	if r.wantSize >= 0 && r.read != r.wantSize {
		return &IntegrityError{What: integritySize, Want: strconv.FormatInt(r.wantSize, 10), Got: strconv.FormatInt(r.read, 10)}
	}
	if r.digest != nil {
		if got := r.digest.Sum(nil); !bytes.Equal(got, r.want) {
			return &IntegrityError{What: r.algo, Want: base64.StdEncoding.EncodeToString(r.want), Got: base64.StdEncoding.EncodeToString(got)}
		}
	}
	return nil
}

// Close closes the response body.
func (r *ObjectReader) Close() error { return r.body.Close() }

// Download opens an object's content through a presigned URL, streamed: the
// object is never held in memory whole. The request goes through the
// client's Transfer (WithTransfer).
func Download(ctx context.Context, data *DataPlane, name string, opts DownloadOptions) (*ObjectReader, error) {
	if opts.Offset < 0 || opts.Length < 0 {
		return nil, ErrInvalidRange
	}
	resp, err := data.Object.DownloadObject(ctx, connect.NewRequest(&datav1.DownloadObjectRequest{Name: name}))
	if err != nil {
		return nil, err
	}
	signed, object := resp.Msg.GetDownloadUrl(), resp.Msg.GetObject()
	if signed.GetUrl() == "" {
		return nil, ErrNoDownloadURL
	}
	header := http.Header{}
	if opts.ranged() {
		header.Set(headerRange, opts.header())
	}
	got, err := data.Transfer().do(ctx, http.MethodGet, signed, header, nil, 0)
	if err != nil {
		return nil, err
	}
	if opts.ranged() && got.StatusCode != http.StatusPartialContent {
		_ = got.Body.Close()
		return nil, ErrRangeIgnored
	}
	r := &ObjectReader{
		Object:        object,
		ContentType:   got.Header.Get(headerContentType),
		ContentLength: unknownLength,
		body:          got.Body,
		wantSize:      unknownLength,
	}
	if r.ContentType == "" {
		r.ContentType = object.GetContentType()
	}
	if got.Header.Get(headerContentLength) != "" {
		r.ContentLength = got.ContentLength
	}
	if !opts.ranged() {
		r.expect(object)
	}
	return r, nil
}

// expect sets what a whole read must match: the object's recorded size and,
// when the server recorded one this SDK can compute, its checksum. A
// multipart object's checksum is a composite of its parts' and is not
// checked: it does not decode to a digest of the algorithm's length.
func (r *ObjectReader) expect(object *datav1.Object) {
	if size := object.GetSizeBytes(); size > 0 {
		r.wantSize = size
	}
	sum := object.GetChecksum()
	newDigest, ok := checksums[sum.GetAlgorithm()]
	if !ok {
		return
	}
	want, err := base64.StdEncoding.DecodeString(sum.GetValue())
	digest := newDigest()
	if err != nil || len(want) != digest.Size() {
		return
	}
	r.digest, r.want, r.algo = digest, want, sum.GetAlgorithm()
}
