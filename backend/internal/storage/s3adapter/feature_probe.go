package s3adapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
)

const (
	// probeBucketPrefix names the scratch bucket a probe creates and removes.
	probeBucketPrefix = "paladin-probe-"
	// probeKeyPrefix is where a probe writes when it has no scratch bucket.
	// No tenant key can start with it: every tenant key starts with a UUID.
	probeKeyPrefix = ".paladin-probe/"
	// probeRunBytes is the randomness in a probe run's name; it keeps two
	// probes of one store apart.
	probeRunBytes = 8
	// probePublicPrefix is the prefix the probe's policy opens, and
	// probePrivatePrefix one beside it that must stay closed.
	probePublicPrefix  = "public/"
	probePrivatePrefix = "private/"
	// probePostTTL bounds the probe's own presigned POST.
	probePostTTL = time.Minute
	// probeFormFile is the form field a POST upload carries its bytes in.
	probeFormFile = "file"
	// probeErrorExcerpt caps how much of an error body a result quotes.
	probeErrorExcerpt = 200
)

// probeBody is what every probe object holds; probeOtherBody is what a
// mismatched checksum is computed over.
var (
	probeBody      = []byte("paladin feature probe")
	probeOtherBody = []byte("not the probe body")
)

// ProbeFeatures exercises every catalog feature against the store and returns
// one result per feature, in catalog order (ADR-0026). It writes into a
// scratch bucket it creates and removes, or under probeKeyPrefix in the
// configured bucket when the store will not create one. The caller bounds it
// with a context deadline; a probe the deadline cuts short is Unknown.
func (c *Client) ProbeFeatures(ctx context.Context) []features.Result {
	p := &featureProbe{c: c, at: time.Now().UTC(), found: map[features.Feature]features.Result{}}
	p.run(ctx)
	out := make([]features.Result, 0, len(features.Catalog))
	for _, spec := range features.Catalog {
		r, ok := p.found[spec.Feature]
		if !ok {
			r = features.Result{Feature: spec.Feature, Support: features.Unknown, Message: "not probed"}
		}
		r.CheckedAt = p.at
		out = append(out, r)
	}
	return out
}

type featureProbe struct {
	c      *Client
	at     time.Time
	found  map[features.Feature]features.Result
	bucket string
	prefix string
	// written is every key the probe put, removed when it finishes.
	written []string
}

func (p *featureProbe) setf(f features.Feature, s features.Support, format string, args ...any) {
	p.found[f] = features.Result{Feature: f, Support: s, Message: fmt.Sprintf(format, args...)}
}

func (p *featureProbe) supported(f features.Feature) {
	p.found[f] = features.Result{Feature: f, Support: features.Supported}
}

// failed records what an error says about f: the store answering a request
// with a refusal means it does not do the thing; a transport failure, a
// timeout or a server error says nothing either way.
func (p *featureProbe) failed(f features.Feature, what string, err error) {
	p.setf(f, supportOf(err), "%s: %s", what, excerpt(err.Error()))
}

func (p *featureProbe) run(ctx context.Context) {
	run, err := probeRunName()
	if err != nil {
		for _, spec := range features.Catalog {
			p.setf(spec.Feature, features.Unknown, "name a probe run: %v", err)
		}
		return
	}
	scratch := probeBucketPrefix + run
	createErr := p.c.CreateBucket(ctx, "", scratch, "")
	if createErr == nil {
		p.bucket = scratch
		defer p.removeScratch(ctx, scratch)
	} else {
		p.failed(features.BucketCreate, "create a scratch bucket", createErr)
		p.setf(features.AnonymousReadPolicy, features.Unknown,
			"needs a scratch bucket, and the store would not create one")
		p.bucket = p.c.cfg.Bucket
		p.prefix = probeKeyPrefix + run + "/"
	}
	if p.bucket == "" {
		for _, f := range []features.Feature{features.ConditionalPut, features.ChecksumSHA256,
			features.MultipartUpload, features.ServerSideCopy, features.PresignedPost} {
			p.setf(f, features.Unknown, "no bucket to probe in: none is configured and a scratch bucket could not be created")
		}
		return
	}
	defer p.removeWritten(ctx)

	p.conditionalPut(ctx)
	p.checksum(ctx)
	p.multipartUpload(ctx)
	p.serverSideCopy(ctx)
	p.presignedPost(ctx)
	if createErr == nil {
		p.anonymousRead(ctx)
	}
}

func probeRunName() (string, error) {
	b := make([]byte, probeRunBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (p *featureProbe) key(name string) string { return p.prefix + name }

// put writes the probe body at key with mut applied to the request.
func (p *featureProbe) put(ctx context.Context, key string, mut func(*s3.PutObjectInput)) error {
	in := &s3.PutObjectInput{
		Bucket:        aws.String(p.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(probeBody),
		ContentLength: aws.Int64(int64(len(probeBody))),
	}
	if mut != nil {
		mut(in)
	}
	_, err := p.c.s3.PutObject(ctx, in)
	if err == nil {
		p.written = append(p.written, key)
	}
	return err
}

func (p *featureProbe) conditionalPut(ctx context.Context) {
	key := p.key("conditional")
	if err := p.put(ctx, key, nil); err != nil {
		p.failed(features.ConditionalPut, "a plain PUT", err)
		return
	}
	err := p.put(ctx, key, func(in *s3.PutObjectInput) { in.IfNoneMatch = aws.String(ifNoneMatchAny) })
	switch status, _ := statusOf(err); {
	case err == nil:
		p.setf(features.ConditionalPut, features.Unsupported,
			"a PUT with If-None-Match: * replaced an existing object")
	case status == http.StatusPreconditionFailed:
		p.supported(features.ConditionalPut)
	default:
		p.failed(features.ConditionalPut, "a PUT with If-None-Match: * over an existing object", err)
	}
}

func sha256Base64(b []byte) string {
	sum := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (p *featureProbe) checksum(ctx context.Context) {
	if err := p.put(ctx, p.key("checksum-match"), func(in *s3.PutObjectInput) {
		in.ChecksumSHA256 = aws.String(sha256Base64(probeBody))
	}); err != nil {
		p.failed(features.ChecksumSHA256, "a PUT under the right x-amz-checksum-sha256", err)
		return
	}
	err := p.put(ctx, p.key("checksum-mismatch"), func(in *s3.PutObjectInput) {
		in.ChecksumSHA256 = aws.String(sha256Base64(probeOtherBody))
	})
	switch status, _ := statusOf(err); {
	case err == nil:
		p.setf(features.ChecksumSHA256, features.Unsupported,
			"stored bytes whose x-amz-checksum-sha256 did not match them")
	case status == http.StatusBadRequest:
		p.supported(features.ChecksumSHA256)
	default:
		p.failed(features.ChecksumSHA256, "a PUT under a wrong x-amz-checksum-sha256", err)
	}
}

func (p *featureProbe) multipartUpload(ctx context.Context) {
	key := p.key("multipart")
	created, err := p.c.s3.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(p.bucket), Key: aws.String(key),
	})
	if err != nil {
		p.failed(features.MultipartUpload, "create a multipart upload", err)
		return
	}
	abort := func() {
		_, _ = p.c.s3.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
			Bucket: aws.String(p.bucket), Key: aws.String(key), UploadId: created.UploadId,
		})
	}
	const partNumber = 1
	part, err := p.c.s3.UploadPart(ctx, &s3.UploadPartInput{
		Bucket: aws.String(p.bucket), Key: aws.String(key), UploadId: created.UploadId,
		PartNumber: aws.Int32(partNumber), Body: bytes.NewReader(probeBody),
		ContentLength: aws.Int64(int64(len(probeBody))),
	})
	if err != nil {
		abort()
		p.failed(features.MultipartUpload, "upload a part", err)
		return
	}
	if _, err := p.c.s3.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String(p.bucket), Key: aws.String(key), UploadId: created.UploadId,
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: []s3types.CompletedPart{
			{ETag: part.ETag, PartNumber: aws.Int32(partNumber)},
		}},
	}); err != nil {
		abort()
		p.failed(features.MultipartUpload, "complete a multipart upload", err)
		return
	}
	p.written = append(p.written, key)
	p.checkStored(ctx, features.MultipartUpload, key, "the completed upload")
}

// checkStored records f supported when key holds the probe body's length, and
// unsupported when the store reports something else there.
func (p *featureProbe) checkStored(ctx context.Context, f features.Feature, key, what string) {
	head, err := p.c.s3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(p.bucket), Key: aws.String(key)})
	if err != nil {
		p.failed(f, "read back "+what, err)
		return
	}
	if got := aws.ToInt64(head.ContentLength); got != int64(len(probeBody)) {
		p.setf(f, features.Unsupported, "%s holds %d bytes, want %d", what, got, len(probeBody))
		return
	}
	p.supported(f)
}

func (p *featureProbe) serverSideCopy(ctx context.Context) {
	src, dst := p.key("copy-source"), p.key("copy-destination")
	if err := p.put(ctx, src, nil); err != nil {
		p.failed(features.ServerSideCopy, "write the copy's source", err)
		return
	}
	if _, err := p.c.s3.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket: aws.String(p.bucket), Key: aws.String(dst), CopySource: aws.String(p.bucket + "/" + src),
	}); err != nil {
		p.failed(features.ServerSideCopy, "copy within the store", err)
		return
	}
	p.written = append(p.written, dst)
	p.checkStored(ctx, features.ServerSideCopy, dst, "the copy")
}

// presignedPost signs a form upload against the internal endpoint — the probe
// asks what the store does, not whether this process can reach the public
// host — and submits it.
func (p *featureProbe) presignedPost(ctx context.Context) {
	key := p.key("post")
	signed, err := s3.NewPresignClient(p.c.s3).PresignPostObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(p.bucket), Key: aws.String(key),
	}, func(o *s3.PresignPostOptions) { o.Expires = probePostTTL })
	if err != nil {
		p.setf(features.PresignedPost, features.Unknown, "sign a form upload: %v", err)
		return
	}
	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	for k, v := range signed.Values {
		if err := w.WriteField(k, v); err != nil {
			p.setf(features.PresignedPost, features.Unknown, "build the form: %v", err)
			return
		}
	}
	fw, err := w.CreateFormFile(probeFormFile, "probe")
	if err == nil {
		_, err = fw.Write(probeBody)
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		p.setf(features.PresignedPost, features.Unknown, "build the form: %v", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, signed.URL, &form)
	if err != nil {
		p.setf(features.PresignedPost, features.Unknown, "build the request: %v", err)
		return
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := p.c.s3.Options().HTTPClient.Do(req)
	if err != nil {
		p.setf(features.PresignedPost, features.Unknown, "submit the form: %v", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, probeErrorExcerpt))
		p.setf(features.PresignedPost, supportOfStatus(resp.StatusCode),
			"a form upload answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		return
	}
	p.written = append(p.written, key)
	p.checkStored(ctx, features.PresignedPost, key, "the form upload")
}

// anonymousRead asks whether a policy opening one prefix to unsigned GETs is
// enforced, and only there. A store that serves unsigned requests before any
// policy is set fails too: on it a public collection could not be told apart
// from a private one.
func (p *featureProbe) anonymousRead(ctx context.Context) {
	open, closed := probePublicPrefix+"object", probePrivatePrefix+"object"
	for _, k := range []string{open, closed} {
		if err := p.put(ctx, k, nil); err != nil {
			p.failed(features.AnonymousReadPolicy, "write an object to read", err)
			return
		}
	}
	if err := p.readAnonymously(ctx, open); err == nil {
		p.setf(features.AnonymousReadPolicy, features.Unsupported,
			"the store serves unsigned requests with no policy set, so nothing it holds is private")
		return
	} else if !refused(err) {
		p.failed(features.AnonymousReadPolicy, "an unsigned GET before any policy", err)
		return
	}
	policy, err := renderAnonymousReadPolicy(p.bucket, probePublicPrefix)
	if err != nil {
		p.setf(features.AnonymousReadPolicy, features.Unknown, "%v", err)
		return
	}
	if _, err := p.c.s3.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String(p.bucket), Policy: aws.String(policy),
	}); err != nil {
		p.failed(features.AnonymousReadPolicy, "set a bucket policy", err)
		return
	}
	if err := p.readAnonymously(ctx, open); err != nil {
		if refused(err) {
			p.setf(features.AnonymousReadPolicy, features.Unsupported,
				"the store accepted the policy and refused the unsigned GET it allows")
			return
		}
		p.failed(features.AnonymousReadPolicy, "an unsigned GET the policy allows", err)
		return
	}
	switch err := p.readAnonymously(ctx, closed); {
	case err == nil:
		p.setf(features.AnonymousReadPolicy, features.Unsupported,
			"a policy opening one prefix opened the whole bucket")
	case refused(err):
		p.supported(features.AnonymousReadPolicy)
	default:
		p.failed(features.AnonymousReadPolicy, "an unsigned GET outside the policy", err)
	}
}

// readAnonymously GETs key unsigned and checks the bytes are the probe's.
func (p *featureProbe) readAnonymously(ctx context.Context, key string) error {
	out, err := p.c.anonymous.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(p.bucket), Key: aws.String(key)})
	if err != nil {
		return err
	}
	defer func() { _ = out.Body.Close() }()
	got, err := io.ReadAll(out.Body)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, probeBody) {
		return fmt.Errorf("an unsigned GET of %s served %d other bytes", key, len(got))
	}
	return nil
}

func (p *featureProbe) removeWritten(ctx context.Context) {
	for _, k := range p.written {
		_, _ = p.c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(p.bucket), Key: aws.String(k)})
	}
	p.written = nil
}

// removeScratch deletes the scratch bucket, after run's deferred
// removeWritten has emptied it. A store that let the probe create
// a bucket it cannot delete does not support BucketCreate as Paladin uses it:
// provisioning would leak buckets.
func (p *featureProbe) removeScratch(ctx context.Context, bucket string) {
	_, _ = p.c.s3.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{Bucket: aws.String(bucket)})
	if _, err := p.c.s3.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)}); err != nil {
		p.setf(features.BucketCreate, supportOf(err),
			"created scratch bucket %s and could not delete it: %s", bucket, excerpt(err.Error()))
		return
	}
	p.supported(features.BucketCreate)
}

// statusOf returns the HTTP status an S3 error carries, if it carries one.
// Matched structurally, as notFound does, so an SDK release that re-wraps
// the error types does not change the answer.
func statusOf(err error) (int, bool) {
	var withStatus interface{ HTTPStatusCode() int }
	if errors.As(err, &withStatus) {
		return withStatus.HTTPStatusCode(), true
	}
	return 0, false
}

// refused reports whether an unsigned request was refused for want of
// authorisation.
func refused(err error) bool {
	status, ok := statusOf(err)
	return ok && (status == http.StatusForbidden || status == http.StatusUnauthorized)
}

// supportOf reads an error as a probe outcome: a refusal from the store means
// the feature is missing; anything without a status says nothing.
func supportOf(err error) features.Support {
	status, ok := statusOf(err)
	if !ok {
		return features.Unknown
	}
	return supportOfStatus(status)
}

// supportOfStatus: a 4xx is the store refusing, and 501 Not Implemented is
// the store saying so; any other 5xx is a failure that proves nothing.
func supportOfStatus(status int) features.Support {
	if status/100 == 4 || status == http.StatusNotImplemented {
		return features.Unsupported
	}
	return features.Unknown
}

func excerpt(s string) string {
	if len(s) <= probeErrorExcerpt {
		return s
	}
	return s[:probeErrorExcerpt] + "…"
}
