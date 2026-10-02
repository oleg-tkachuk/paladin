package paladin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

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
	// errorBodyLimit bounds how much of a refused transfer's body an error quotes.
	errorBodyLimit = 512
)

// Errors from Upload and Download.
var (
	ErrNoUploadURL = errors.New("paladin: the server returned no upload URL")
	ErrNoETag      = errors.New("paladin: storage returned no ETag for a part; it must expose the ETag header")
	ErrUploadSize  = errors.New("paladin: upload size must be positive")
)

// TransferError is a presigned request that storage refused.
type TransferError struct {
	Method string
	Status int
	Body   string
}

func (e *TransferError) Error() string {
	return fmt.Sprintf("paladin: storage refused %s: %d %s", e.Method, e.Status, e.Body)
}

// UploadInput describes one object to upload. Body is read at offsets, so a
// multipart upload can send parts in parallel: an *os.File, *bytes.Reader or
// io.NewSectionReader all fit.
type UploadInput struct {
	Parent      string // tenants/{tenant}/collections/{collection}
	Key         string // empty: the server names the object by its id
	ContentType string
	Size        int64
	Body        io.ReaderAt
	Metadata    map[string]string
	Tags        map[string]string
}

// UploadOptions tune Upload; the zero value takes the defaults.
type UploadOptions struct {
	// HTTPClient sends the presigned requests to storage; nil means
	// http.DefaultClient. These go to the storage backend, not to Paladin,
	// so the Paladin client's credentials are not on them.
	HTTPClient         *http.Client
	MultipartThreshold int64
	PartConcurrency    int
}

func (o UploadOptions) client() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	return http.DefaultClient
}

// Upload stores in.Body as a new object and returns it once complete: one
// presigned PUT up to the multipart threshold, multipart above it. A failed
// multipart upload is aborted.
func Upload(ctx context.Context, data *DataPlane, in UploadInput, opts UploadOptions) (*datav1.Object, error) {
	if in.Size <= 0 {
		return nil, ErrUploadSize
	}
	threshold := opts.MultipartThreshold
	if threshold <= 0 {
		threshold = DefaultMultipartThreshold
	}
	if in.Size > threshold {
		return uploadMultipart(ctx, data, in, opts)
	}
	return uploadSingle(ctx, data, in, opts)
}

func uploadSingle(ctx context.Context, data *DataPlane, in UploadInput, opts UploadOptions) (*datav1.Object, error) {
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
	etag, err := put(ctx, opts.client(), url, in.ContentType, io.NewSectionReader(in.Body, 0, in.Size), in.Size)
	if err != nil {
		return nil, err
	}
	done, err := data.Object.CompleteObject(ctx, connect.NewRequest(&datav1.CompleteObjectRequest{
		Name: object.GetName(), Etag: etag,
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
	next := make(chan int)
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
			for i := range next {
				etag, err := sendPart(ctx, data, in, opts, name, uploadID, partSize, i)
				if err != nil {
					fail(err)
					continue
				}
				etags[i] = etag
			}
		})
	}
feed:
	for i := range parts {
		select {
		case next <- i:
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

func sendPart(ctx context.Context, data *DataPlane, in UploadInput, opts UploadOptions,
	name, uploadID string, partSize int64, index int,
) (string, error) {
	signed, err := data.MultipartUpload.PresignPart(ctx, connect.NewRequest(&datav1.PresignPartRequest{
		ObjectName: name, UploadId: uploadID, PartNumber: int32(index + 1), //nolint:gosec // parts ≤ 10000 by the contract
	}))
	if err != nil {
		return "", err
	}
	if signed.Msg.GetUploadUrl().GetUrl() == "" {
		return "", ErrNoUploadURL
	}
	offset := int64(index) * partSize
	length := min(partSize, in.Size-offset)
	etag, err := put(ctx, opts.client(), signed.Msg.GetUploadUrl(), "", io.NewSectionReader(in.Body, offset, length), length)
	if err != nil {
		return "", err
	}
	if etag == "" {
		return "", ErrNoETag
	}
	return etag, nil
}

// put sends body to a presigned URL and returns the ETag storage answered with.
func put(ctx context.Context, c *http.Client, url *commonv1.PresignedUrl, contentType string, body io.Reader, size int64) (string, error) {
	method := url.GetMethod()
	if method == "" {
		method = http.MethodPut
	}
	req, err := http.NewRequestWithContext(ctx, method, url.GetUrl(), body)
	if err != nil {
		return "", err
	}
	req.ContentLength = size
	if contentType != "" {
		req.Header.Set(headerContentType, contentType)
	}
	// Covered by the signature: storage refuses the request without them.
	for k, v := range url.GetRequiredHeaders() {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := refused(method, resp); err != nil {
		return "", err
	}
	return strings.Trim(resp.Header.Get(headerETag), etagQuote), nil
}

func refused(method string, resp *http.Response) error {
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
	return &TransferError{Method: method, Status: resp.StatusCode, Body: string(body)}
}

// Download opens the object's content through a presigned URL. The caller
// closes the reader. httpClient sends that request; nil means
// http.DefaultClient.
func Download(ctx context.Context, data *DataPlane, name string, httpClient *http.Client) (io.ReadCloser, *datav1.Object, error) {
	resp, err := data.Object.DownloadObject(ctx, connect.NewRequest(&datav1.DownloadObjectRequest{Name: name}))
	if err != nil {
		return nil, nil, err
	}
	url := resp.Msg.GetDownloadUrl()
	if url.GetUrl() == "" {
		return nil, nil, ErrNoUploadURL
	}
	method := url.GetMethod()
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, url.GetUrl(), nil)
	if err != nil {
		return nil, nil, err
	}
	for k, v := range url.GetRequiredHeaders() {
		req.Header.Set(k, v)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	got, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	if err := refused(method, got); err != nil {
		_ = got.Body.Close()
		return nil, nil, err
	}
	return got.Body, resp.Msg.GetObject(), nil
}
