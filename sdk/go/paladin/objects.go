package paladin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

// Upload strategy, the console's: up to the threshold one presigned PUT; above
// it multipart, whose parts go up a few at a time.
const (
	DefaultMultipartThreshold = 8 << 20
	DefaultPartConcurrency    = 3
)

// Presigned-URL request details.
const (
	headerContentType = "Content-Type"
	headerETag        = "ETag"
	etagQuote         = `"`
)

// Errors from Upload.
var (
	ErrNoUploadURL = errors.New("paladin: the server returned no upload URL")
	ErrNoETag      = errors.New("paladin: storage returned no ETag for a part; it must expose the ETag header")
	ErrUploadSize  = errors.New("paladin: upload size must be positive")
	ErrUploadBody  = errors.New("paladin: an upload needs exactly one of Body and Stream")
)

// UploadInput describes one object to upload, from exactly one of Body and
// Stream; neither is read into memory whole.
type UploadInput struct {
	Parent      string // tenants/{tenant}/collections/{collection}
	Key         string // empty: the server names the object by its id
	ContentType string
	Size        int64
	// Body is read at offsets, so a multipart upload sends its parts in
	// parallel: an *os.File, *bytes.Reader or io.NewSectionReader all fit.
	Body io.ReaderAt
	// Stream is read once, front to back — a pipe, a response body. Multipart
	// holds the parts in flight in memory, PartConcurrency+1 of them at most.
	Stream   io.Reader
	Metadata map[string]string
	Tags     map[string]string
}

// UploadOptions tune Upload; the zero value takes the defaults. The presigned
// requests go through the client's Transfer (WithTransfer).
type UploadOptions struct {
	MultipartThreshold int64
	PartConcurrency    int
}

// Transfer is what sends this plane's presigned requests: the client's
// WithTransfer, else a default shared by every client without one.
func (d *DataPlane) Transfer() *Transfer {
	if d == nil || d.transfer == nil {
		return defaultTransfer()
	}
	return d.transfer
}

// Upload stores the input as a new object and returns it once complete: one
// presigned PUT up to the multipart threshold, multipart above it. A failed
// multipart upload is aborted. A single PUT records the content's SHA-256 on
// the object, which Download then verifies.
func Upload(ctx context.Context, data *DataPlane, in UploadInput, opts UploadOptions) (*datav1.Object, error) {
	if in.Size <= 0 {
		return nil, ErrUploadSize
	}
	if (in.Body == nil) == (in.Stream == nil) {
		return nil, ErrUploadBody
	}
	threshold := opts.MultipartThreshold
	if threshold <= 0 {
		threshold = DefaultMultipartThreshold
	}
	if in.Size > threshold {
		return uploadMultipart(ctx, data, in, opts)
	}
	return uploadSingle(ctx, data, in)
}

// content reads length bytes at offset: from Body directly, from Stream in
// order. Only multipart asks for anything but the whole.
func (in UploadInput) content(offset, length int64) io.Reader {
	if in.Body != nil {
		return io.NewSectionReader(in.Body, offset, length)
	}
	return io.LimitReader(in.Stream, length)
}

func uploadSingle(ctx context.Context, data *DataPlane, in UploadInput) (*datav1.Object, error) {
	allocated, err := data.Object.UploadObject(ctx, connect.NewRequest(&datav1.UploadObjectRequest{
		Parent:            in.Parent,
		Key:               in.Key,
		ContentType:       in.ContentType,
		SizeHintBytes:     in.Size,
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
		Metadata:          in.Metadata,
		Tags:              in.Tags,
		Transport:         datav1.PresignTransport_PRESIGN_TRANSPORT_PUT,
	}))
	if err != nil {
		return nil, err
	}
	url, object := allocated.Msg.GetUploadUrl(), allocated.Msg.GetObject()
	if url.GetUrl() == "" || object == nil {
		return nil, ErrNoUploadURL
	}
	sum := sha256.New()
	etag, err := put(ctx, data.Transfer(), url, in.ContentType, io.TeeReader(in.content(0, in.Size), sum), in.Size)
	if err != nil {
		return nil, err
	}
	done, err := data.Object.CompleteObject(ctx, connect.NewRequest(&datav1.CompleteObjectRequest{
		Name: object.GetName(), Etag: etag, ChecksumValue: base64.StdEncoding.EncodeToString(sum.Sum(nil)),
	}))
	if err != nil {
		return nil, err
	}
	return done.Msg, nil
}

func uploadMultipart(ctx context.Context, data *DataPlane, in UploadInput, opts UploadOptions) (_ *datav1.Object, err error) {
	init, err := data.MultipartUpload.InitiateMultipartUpload(ctx, connect.NewRequest(&datav1.InitiateMultipartUploadRequest{
		Parent:            in.Parent,
		Key:               in.Key,
		ContentType:       in.ContentType,
		SizeBytes:         in.Size,
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
		Metadata:          in.Metadata,
		Tags:              in.Tags,
	}))
	if err != nil {
		return nil, err
	}
	name, uploadID := init.Msg.GetObject().GetName(), init.Msg.GetUploadId()
	defer func() {
		if err != nil {
			// Best effort, and past a cancelled ctx: a session left open is
			// swept by the server, and must not mask the error that ended it.
			_, _ = data.MultipartUpload.AbortMultipartUpload(context.WithoutCancel(ctx),
				connect.NewRequest(&datav1.AbortMultipartUploadRequest{ObjectName: name, UploadId: uploadID}))
		}
	}()

	partSize := init.Msg.GetRecommendedPartSize()
	if partSize <= 0 {
		partSize = DefaultMultipartThreshold
	}
	parts := int((in.Size + partSize - 1) / partSize)
	etags, err := sendParts(ctx, data, in, opts, name, uploadID, partSize, parts)
	if err != nil {
		return nil, err
	}
	completed := make([]*datav1.CompletedPart, parts)
	for i, etag := range etags {
		completed[i] = &datav1.CompletedPart{PartNumber: int32(i + 1), Etag: etag} //nolint:gosec // parts ≤ 10000 by the contract
	}
	done, err := data.MultipartUpload.CompleteMultipartUpload(ctx, connect.NewRequest(&datav1.CompleteMultipartUploadRequest{
		ObjectName: name, UploadId: uploadID, Parts: completed,
	}))
	if err != nil {
		return nil, err
	}
	return done.Msg, nil
}

// part is one part to send: its index, and its bytes.
type part struct {
	index  int
	body   io.Reader
	length int64
}

// sendParts presigns and PUTs every part, PartConcurrency at a time, and
// returns their ETags in part order. The first failure stops the rest.
func sendParts(ctx context.Context, data *DataPlane, in UploadInput, opts UploadOptions,
	name, uploadID string, partSize int64, parts int,
) ([]string, error) {
	workers := opts.PartConcurrency
	if workers <= 0 {
		workers = DefaultPartConcurrency
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	etags := make([]string, parts)
	next := make(chan part)
	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	fail := func(err error) {
		once.Do(func() { firstErr = err; cancel() })
	}
	for range min(workers, parts) {
		wg.Go(func() {
			for p := range next {
				etag, err := sendPart(ctx, data, name, uploadID, p)
				if err != nil {
					fail(err)
					continue
				}
				etags[p.index] = etag
			}
		})
	}
feed:
	for i := range parts {
		offset := int64(i) * partSize
		p := part{index: i, length: min(partSize, in.Size-offset)}
		if in.Body != nil {
			p.body = in.content(offset, p.length)
		} else {
			// A stream is read in order, so each part is read here, before it
			// is handed to a worker; the unbuffered channel bounds how many
			// are held.
			buf := make([]byte, p.length)
			if _, err := io.ReadFull(in.Stream, buf); err != nil {
				fail(err)
				break feed
			}
			p.body = bytes.NewReader(buf)
		}
		select {
		case next <- p:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return etags, nil
}

func sendPart(ctx context.Context, data *DataPlane, name, uploadID string, p part) (string, error) {
	signed, err := data.MultipartUpload.PresignPart(ctx, connect.NewRequest(&datav1.PresignPartRequest{
		ObjectName: name, UploadId: uploadID, PartNumber: int32(p.index + 1), //nolint:gosec // parts ≤ 10000 by the contract
	}))
	if err != nil {
		return "", err
	}
	if signed.Msg.GetUploadUrl().GetUrl() == "" {
		return "", ErrNoUploadURL
	}
	etag, err := put(ctx, data.Transfer(), signed.Msg.GetUploadUrl(), "", p.body, p.length)
	if err != nil {
		return "", err
	}
	if etag == "" {
		return "", ErrNoETag
	}
	return etag, nil
}

// Put sends size bytes of body to a presigned URL — one a caller presigned
// itself, such as a part of a multipart upload it drives — and returns the
// ETag storage answered with.
func (t *Transfer) Put(ctx context.Context, signed *commonv1.PresignedUrl, body io.Reader, size int64) (string, error) {
	return put(ctx, t, signed, "", body, size)
}

// put sends body to a presigned URL and returns the ETag storage answered with.
func put(ctx context.Context, t *Transfer, signed *commonv1.PresignedUrl, contentType string, body io.Reader, size int64) (string, error) {
	header := http.Header{}
	if contentType != "" {
		header.Set(headerContentType, contentType)
	}
	start := time.Now()
	resp, err := t.do(ctx, http.MethodPut, signed, header, body, size)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body) // so the connection is reused
	t.ended(ctx, resp.Request.Method, resp.Request.URL.Host, size, start, nil)
	return strings.Trim(resp.Header.Get(headerETag), etagQuote), nil
}
