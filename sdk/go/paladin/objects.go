package paladin

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // S3's part ETag, compared on resume
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

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
	ErrUploadSize  = errors.New("paladin: upload size must not be negative")
	ErrUploadBody  = errors.New("paladin: an upload needs exactly one of Body and Stream")
)

// UploadInput describes one object to upload, from exactly one of Body and
// Stream.
//
// Every upload URL is signed for its body's exact size and SHA-256, so each
// body is hashed before it is presigned. Body is read twice for that — once
// to hash, once to send — and never held in memory. Stream can be read only
// once, so its bytes are held while they are hashed and sent: the whole
// object below the multipart threshold, and the parts in flight above it.
type UploadInput struct {
	Parent      string // tenants/{tenant}/collections/{collection}
	Key         string // empty: the server names the object by its id
	ContentType string
	// Size is the object's exact size; 0 is an empty object.
	Size int64
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
// requests go through the client's Transfer (WithTransfer), which retries
// each one through a freshly presigned URL.
type UploadOptions struct {
	MultipartThreshold int64
	PartConcurrency    int
	// OnSession is called once a multipart upload is open, with what Resume
	// needs to continue it after a crash; store it where a restart finds it.
	// With OnSession set, a failed upload is left open for that restart
	// instead of aborted.
	OnSession func(UploadSession)
	// Resume continues the multipart upload a previous Upload of the same
	// input opened, sending only the parts storage does not already hold. A
	// failed resume is left open too.
	Resume *UploadSession
}

// UploadSession is an open multipart upload: enough to resume it.
type UploadSession struct {
	ObjectName string
	UploadID   string
	PartSize   int64
	TotalParts int32
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
	if in.Size < 0 {
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

// source opens a body afresh: every call is an independent reader over the
// same bytes. Each attempt at a request reads its own, because net/http may
// still be reading a failed attempt's body when the next one starts.
type source func() io.Reader

// content is length bytes at offset as a source: a section of Body, read in
// place, or the next length bytes of Stream, held in memory.
func (in UploadInput) content(offset, length int64) (source, error) {
	if in.Body != nil {
		return func() io.Reader { return io.NewSectionReader(in.Body, offset, length) }, nil
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(in.Stream, buf); err != nil {
		return nil, err
	}
	return func() io.Reader { return bytes.NewReader(buf) }, nil
}

// hashed is the source's SHA-256 as an upload's checksum_value.
func hashed(body source) (string, error) {
	sum := sha256.New()
	if _, err := io.Copy(sum, body()); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sum.Sum(nil)), nil
}

func uploadSingle(ctx context.Context, data *DataPlane, in UploadInput) (*datav1.Object, error) {
	body, err := in.content(0, in.Size)
	if err != nil {
		return nil, err
	}
	sum, err := hashed(body)
	if err != nil {
		return nil, err
	}
	allocated, err := data.Object.UploadObject(ctx, &datav1.UploadObjectRequest{
		Parent:            in.Parent,
		Key:               in.Key,
		ContentType:       in.ContentType,
		SizeHintBytes:     in.Size,
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
		ChecksumValue:     sum,
		Metadata:          in.Metadata,
		Tags:              in.Tags,
		Transport:         datav1.PresignTransport_PRESIGN_TRANSPORT_PUT,
	})
	if err != nil {
		return nil, err
	}
	url, object := allocated.GetUploadUrl(), allocated.GetObject()
	if url.GetUrl() == "" || object == nil {
		return nil, ErrNoUploadURL
	}
	transfer := data.Transfer()
	regenerate := func(ctx context.Context) (*commonv1.PresignedUrl, error) {
		resp, err := data.Presign.RegenerateUploadUrl(ctx, &datav1.RegenerateUploadUrlRequest{Name: object.GetName()})
		if err != nil {
			return nil, err
		}
		return resp.GetUploadUrl(), nil
	}
	var etag string
	err = withRetries(ctx, transfer.attempts, url, regenerate, func(ctx context.Context, signed *commonv1.PresignedUrl) error {
		var err error
		etag, err = put(ctx, transfer, signed, in.ContentType, body(), in.Size)
		return err
	})
	// The URL refuses to overwrite, so 412 means an earlier attempt stored
	// the bytes and only its answer was lost: complete without its ETag,
	// which CompleteObject reads from storage itself.
	if err != nil && !AlreadyStored(err) {
		return nil, err
	}
	done, err := data.Object.CompleteObject(ctx, &datav1.CompleteObjectRequest{
		Name: object.GetName(), Etag: etag, ChecksumValue: sum,
	})
	if err != nil {
		return nil, err
	}
	return done, nil
}

func uploadMultipart(ctx context.Context, data *DataPlane, in UploadInput, opts UploadOptions) (_ *datav1.Object, err error) {
	session, stored, err := openSession(ctx, data, in, opts)
	if err != nil {
		return nil, err
	}
	resumable := opts.OnSession != nil || opts.Resume != nil
	defer func() {
		if err != nil && !resumable {
			// Best effort, and past a cancelled ctx: a session left open is
			// swept by the server, and must not mask the error that ended it.
			_ = AbortMultipart(context.WithoutCancel(ctx), data, session)
		}
	}()

	partSize := session.PartSize
	parts := int(session.TotalParts)
	sent, err := sendParts(ctx, data, in, opts, session, stored, partSize, parts)
	if err != nil {
		return nil, err
	}
	completed := make([]*datav1.CompletedPart, parts)
	for i, p := range sent {
		completed[i] = &datav1.CompletedPart{PartNumber: int32(i + 1), Etag: p.etag, ChecksumValue: p.checksum} //nolint:gosec // parts ≤ 10000 by the contract
	}
	return CompleteMultipart(ctx, data, session, completed)
}

// openSession opens the multipart upload, or takes up the one opts.Resume
// names together with the parts storage already holds.
func openSession(ctx context.Context, data *DataPlane, in UploadInput, opts UploadOptions) (UploadSession, map[int32]*datav1.PartInfo, error) {
	if opts.Resume != nil {
		stored, err := storedParts(ctx, data, *opts.Resume)
		return *opts.Resume, stored, err
	}
	init, err := data.MultipartUpload.InitiateMultipartUpload(ctx, &datav1.InitiateMultipartUploadRequest{
		Parent:            in.Parent,
		Key:               in.Key,
		ContentType:       in.ContentType,
		SizeBytes:         in.Size,
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
		Metadata:          in.Metadata,
		Tags:              in.Tags,
	})
	if err != nil {
		return UploadSession{}, nil, err
	}
	session := UploadSession{
		ObjectName: init.GetObject().GetName(),
		UploadID:   init.GetUploadId(),
		PartSize:   init.GetRecommendedPartSize(),
		TotalParts: init.GetTotalParts(),
	}
	if session.PartSize <= 0 {
		session.PartSize = DefaultMultipartThreshold
	}
	if session.TotalParts <= 0 {
		session.TotalParts = int32((in.Size + session.PartSize - 1) / session.PartSize) //nolint:gosec // parts ≤ 10000 by the contract
	}
	if opts.OnSession != nil {
		opts.OnSession(session)
	}
	return session, nil, nil
}

// storedParts lists every part storage holds for the session, all pages.
func storedParts(ctx context.Context, data *DataPlane, s UploadSession) (map[int32]*datav1.PartInfo, error) {
	out := map[int32]*datav1.PartInfo{}
	token := ""
	for {
		resp, err := data.MultipartUpload.ListParts(ctx, &datav1.ListPartsRequest{
			ObjectName: s.ObjectName, UploadId: s.UploadID, Page: &commonv1.PageRequest{PageToken: token},
		})
		if err != nil {
			return nil, err
		}
		for _, p := range resp.GetParts() {
			out[p.GetPartNumber()] = p
		}
		if token = resp.GetPage().GetNextPageToken(); token == "" {
			return out, nil
		}
	}
}

// partETagLen is the length of a part's ETag when it is the hex MD5 of the
// part, as S3 writes it for a part stored without KMS encryption.
const partETagLen = 2 * md5.Size

// holds reports whether a part storage reports is the one to be sent: the
// same length and, when its ETag is a plain MD5, the same bytes. An ETag of
// another form cannot be checked here, so such a part is sent again — a
// repeated part replaces the stored one harmlessly.
func holds(stored *datav1.PartInfo, body source, length int64) (bool, error) {
	if stored == nil || stored.GetSizeBytes() != length || len(stored.GetEtag()) != partETagLen {
		return false, nil
	}
	sum := md5.New() //nolint:gosec // comparing with S3's MD5 ETag, not a security primitive
	if _, err := io.Copy(sum, body()); err != nil {
		return false, err
	}
	return hex.EncodeToString(sum.Sum(nil)) == stored.GetEtag(), nil
}

// part is one part to send: its index, and its bytes.
type part struct {
	index  int
	body   source
	length int64
}

// sentPart is what completion needs of a part: the ETag storage answered
// with and the checksum the part was presigned for.
type sentPart struct {
	etag, checksum string
}

// sendParts presigns and PUTs every part, PartConcurrency at a time, and
// returns them in part order. The first failure stops the rest.
func sendParts(ctx context.Context, data *DataPlane, in UploadInput, opts UploadOptions,
	session UploadSession, stored map[int32]*datav1.PartInfo, partSize int64, parts int,
) ([]sentPart, error) {
	workers := opts.PartConcurrency
	if workers <= 0 {
		workers = DefaultPartConcurrency
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sent := make([]sentPart, parts)
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
				done, err := sendPart(ctx, data, session, stored[int32(p.index+1)], p) //nolint:gosec // parts ≤ 10000 by the contract
				if err != nil {
					fail(err)
					continue
				}
				sent[p.index] = done
			}
		})
	}
feed:
	for i := range parts {
		offset := int64(i) * partSize
		p := part{index: i, length: min(partSize, in.Size-offset)}
		// A stream is read in order, so each part is read here, before it is
		// handed to a worker; the unbuffered channel bounds how many are held.
		body, err := in.content(offset, p.length)
		if err != nil {
			fail(err)
			break feed
		}
		p.body = body
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
	return sent, nil
}

// sendPart hashes a part and, unless storage already holds it, presigns its
// URL for that checksum and PUTs it, retrying through fresh URLs.
func sendPart(ctx context.Context, data *DataPlane, session UploadSession, stored *datav1.PartInfo, p part) (sentPart, error) {
	sum, err := hashed(p.body)
	if err != nil {
		return sentPart{}, err
	}
	have, err := holds(stored, p.body, p.length)
	if err != nil {
		return sentPart{}, err
	}
	if have {
		return sentPart{etag: stored.GetEtag(), checksum: sum}, nil
	}
	presign := func(ctx context.Context) (*commonv1.PresignedUrl, error) {
		return PresignPart(ctx, data, session, int32(p.index+1), sum) //nolint:gosec // parts ≤ 10000 by the contract
	}
	transfer := data.Transfer()
	var etag string
	err = withRetries(ctx, transfer.attempts, nil, presign, func(ctx context.Context, signed *commonv1.PresignedUrl) error {
		var err error
		etag, err = put(ctx, transfer, signed, "", p.body(), p.length)
		return err
	})
	if err != nil {
		return sentPart{}, err
	}
	if etag == "" {
		return sentPart{}, ErrNoETag
	}
	return sentPart{etag: etag, checksum: sum}, nil
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
