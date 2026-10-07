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
	"strings"
	"time"

	"connectrpc.com/connect"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
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
	// ErrObjectChanged is a download whose object was replaced while it was
	// being retried: the fresh URL is for different bytes than the first.
	ErrObjectChanged = errors.New("paladin: the object changed while its download was retried")
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

	body io.ReadCloser
	read int64
	// ended reports the transfer once, at the end of the content or at
	// Close, whichever comes first.
	ended    func(err error)
	done     bool
	wantSize int64 // -1: nothing to check
	digest   hash.Hash
	want     []byte
	algo     string
	// composite, when set instead of digest, recomputes a multipart object's
	// composite checksum; wantParts is the part count it must reach.
	composite *compositeHash
	wantParts int
}

// Read reads the content, and verifies it at the end of a whole object.
func (r *ObjectReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	r.read += int64(n)
	if r.digest != nil {
		r.digest.Write(p[:n])
	}
	if r.composite != nil {
		r.composite.write(p[:n])
	}
	if errors.Is(err, io.EOF) {
		verr := r.verify()
		r.end(verr)
		if verr != nil {
			return n, verr
		}
	} else if err != nil {
		r.end(err)
	}
	return n, err
}

func (r *ObjectReader) end(err error) {
	if !r.done && r.ended != nil {
		r.done = true
		r.ended(err)
	}
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
	if r.composite != nil {
		if got, parts := r.composite.sum(); !bytes.Equal(got, r.want) || parts != r.wantParts {
			return &IntegrityError{What: r.algo, Want: compositeValue(r.want, r.wantParts), Got: compositeValue(got, parts)}
		}
	}
	return nil
}

// compositeSep separates a composite checksum's digest from its part count.
const compositeSep = "-"

// compositeHash recomputes a multipart object's composite checksum from its
// bytes: the digest restarts every partSize bytes, and the composite is the
// digest of the parts' digests, in order.
type compositeHash struct {
	newDigest func() hash.Hash
	partSize  int64
	part      hash.Hash
	inPart    int64
	parts     hash.Hash
	count     int
}

func newCompositeHash(newDigest func() hash.Hash, partSize int64) *compositeHash {
	return &compositeHash{newDigest: newDigest, partSize: partSize, part: newDigest(), parts: newDigest()}
}

func (c *compositeHash) write(p []byte) {
	for len(p) > 0 {
		n := min(int64(len(p)), c.partSize-c.inPart)
		c.part.Write(p[:n])
		c.inPart += n
		p = p[n:]
		if c.inPart == c.partSize {
			c.endPart()
		}
	}
}

func (c *compositeHash) endPart() {
	c.parts.Write(c.part.Sum(nil))
	c.part, c.inPart = c.newDigest(), 0
	c.count++
}

// sum ends a last part shorter than the rest and returns the composite and
// the part count.
func (c *compositeHash) sum() ([]byte, int) {
	if c.inPart > 0 {
		c.endPart()
	}
	return c.parts.Sum(nil), c.count
}

// compositeValue is a composite checksum as the server writes it.
func compositeValue(digest []byte, parts int) string {
	return base64.StdEncoding.EncodeToString(digest) + compositeSep + strconv.Itoa(parts)
}

// parseComposite splits a composite checksum into its digest and part count;
// false when value is not one.
func parseComposite(value string, digestSize int) ([]byte, int, bool) {
	encoded, count, ok := strings.Cut(value, compositeSep)
	if !ok {
		return nil, 0, false
	}
	parts, err := strconv.Atoi(count)
	if err != nil || parts < 1 {
		return nil, 0, false
	}
	digest, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(digest) != digestSize {
		return nil, 0, false
	}
	return digest, parts, true
}

// Close closes the response body.
func (r *ObjectReader) Close() error {
	r.end(nil)
	return r.body.Close()
}

// Download opens an object's content through a presigned URL, streamed: the
// object is never held in memory whole. The request goes through the
// client's Transfer (WithTransfer).
func Download(ctx context.Context, data *DataPlane, name string, opts DownloadOptions) (*ObjectReader, error) {
	if opts.Offset < 0 || opts.Length < 0 {
		return nil, ErrInvalidRange
	}
	// Bound to the object's ETag: the URL serves only the bytes this
	// response describes, so a range read — which the checksum cannot
	// verify — never splices in a different object written at the key.
	resp, err := data.Object.DownloadObject(ownKeys(ctx), connect.NewRequest(&datav1.DownloadObjectRequest{Name: name, RequireEtagMatch: true}))
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
	// A retry asks for a fresh URL; it is bound to the object's ETag as it is
	// then, so an object replaced in between is reported, not read.
	presign := func(ctx context.Context) (*commonv1.PresignedUrl, error) {
		again, err := data.Object.DownloadObject(ownKeys(ctx), connect.NewRequest(&datav1.DownloadObjectRequest{Name: name, RequireEtagMatch: true}))
		if err != nil {
			return nil, err
		}
		if again.Msg.GetObject().GetEtag() != object.GetEtag() {
			return nil, ErrObjectChanged
		}
		return again.Msg.GetDownloadUrl(), nil
	}
	transfer, start := data.Transfer(), time.Now()
	var got *http.Response
	err = withRetries(ctx, transfer.attempts, signed, presign, func(ctx context.Context, s *commonv1.PresignedUrl) error {
		var err error
		got, err = transfer.do(ctx, http.MethodGet, s, header, nil, 0)
		return err
	})
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
	r.ended = func(err error) {
		transfer.ended(ctx, got.Request.Method, got.Request.URL.Host, r.read, start, err)
	}
	return r, nil
}

// expect sets what a whole read must match: the object's recorded size and,
// when the server recorded one this SDK can compute, its checksum — a digest
// of the whole, or a multipart object's composite, recomputed part by part
// over the part size the server recorded beside it. A composite with no part
// size, as a server that predates it reports, is not checked.
func (r *ObjectReader) expect(object *datav1.Object) {
	if size := object.GetSizeBytes(); size > 0 {
		r.wantSize = size
	}
	sum := object.GetChecksum()
	newDigest, ok := checksums[sum.GetAlgorithm()]
	if !ok {
		return
	}
	size := newDigest().Size()
	if partSize := sum.GetPartSizeBytes(); partSize > 0 {
		if want, parts, ok := parseComposite(sum.GetValue(), size); ok {
			r.composite, r.want, r.wantParts, r.algo = newCompositeHash(newDigest, partSize), want, parts, sum.GetAlgorithm()
		}
		return
	}
	want, err := base64.StdEncoding.DecodeString(sum.GetValue())
	if err != nil || len(want) != size {
		return
	}
	r.digest, r.want, r.algo = newDigest(), want, sum.GetAlgorithm()
}

// LookupObject returns the object a paladin:// URI names, found by its key.
func LookupObject(ctx context.Context, data *DataPlane, uri ObjectURI) (*datav1.Object, error) {
	resp, err := data.Object.LookupObject(ctx, connect.NewRequest(&datav1.LookupObjectRequest{
		Parent: uri.Parent(), Key: uri.Key,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// DownloadURI is Download for the object a paladin:// URI names.
func DownloadURI(ctx context.Context, data *DataPlane, uri string, opts DownloadOptions) (*ObjectReader, error) {
	parsed, err := ParseObjectURI(uri)
	if err != nil {
		return nil, err
	}
	object, err := LookupObject(ctx, data, parsed)
	if err != nil {
		return nil, err
	}
	return Download(ctx, data, object.GetName(), opts)
}
