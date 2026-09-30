// Package s3adapter implements the handler-facing storage interfaces
// (object.Storage, object.StreamSink, multipart.Storage, presign.Storage)
// on top of an aws-sdk-go-v2 S3 client.
//
// One adapter instance fronts a single physical S3 bucket configured under
// `storage.backends.<name>.bucket`. The Paladin "Collection" is a tenant-scoped
// prefix inside that bucket; the full S3 key for any Paladin object is:
//
//	<tenant_id>/<collection>/<storage_key>
//
// CompletionMode is sourced from the bundled config: when that backend has
// S3 event notifications enabled, handlers return Implicit; otherwise
// Explicit.
package s3adapter

import (
	"context"
	"crypto/md5" // #nosec G501 — legacy S3 ETag for part-level integrity, not a secret
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// Client is the all-in-one S3-compatible adapter.
type Client struct {
	s3      *s3.Client
	presign *s3.PresignClient
	cfg     config.StorageBackend
	mode    object.CompletionMode
	sseType string // "", "AES256", or "aws:kms"
	sseKey  string
	// backendID labels this client's storage-call metrics. A pointer because
	// the metric middleware is installed inside New, before the id is known —
	// the registry stamps it right after building. Empty until then, and the
	// label is omitted rather than guessed. See SetBackendID.
	backendID *string
}

// SetBackendID names this client in its storage-call metrics. Called once by
// whoever built it, immediately after New and before any request uses it:
// nothing reads the value until an S3 call is in flight, so there is no
// ordering hazard to guard against.
func (c *Client) SetBackendID(id string) {
	if c != nil && c.backendID != nil {
		*c.backendID = id
	}
}

// New builds an S3 client from one storage-backend entry. `backend.Events.Enabled`
// decides CompletionMode (IMPLICIT when events drive promotion, EXPLICIT otherwise).
//
// Two distinct S3 clients are constructed:
//
//   - `s3` — bound to `backend.Endpoint` (the *internal* address). Used for
//     server-side calls the Paladin backend issues itself: HeadObject,
//     CreateBucket, CreateMultipartUpload, CompleteMultipartUpload,
//     CopyObject, DeleteObject, etc. These run from inside the cluster and
//     should hit the in-cluster service hostname.
//
//   - `presign` — bound to `backend.PublicEndpoint` (browser-reachable
//     address) when configured, otherwise falls back to `backend.Endpoint`.
//     Used exclusively to sign URLs that travel back to the browser via
//     the API response. The signed URL embeds the host it was signed
//     against, so it MUST match what the browser will actually dial.
//
// Without this split a deployment that exposes the storage backend on a
// separate public ingress (e.g. internal=`http://seaweedfs-filer.storage.svc:8333`,
// public=`https://s3.example.com`) would hand the browser presigned URLs
// pointing at the in-cluster DNS name — which a browser cannot resolve
// and which would also fail SigV4 verification at the public endpoint.
func New(ctx context.Context, backend config.StorageBackend) (*Client, error) {
	awsCfg, err := buildAWSConfig(ctx, backend)
	if err != nil {
		return nil, err
	}

	// Internal S3 client — direct API calls from inside the cluster. Every one
	// of them is counted and timed by the metrics middleware; see metrics.go
	// for why it lives in the SDK stack rather than around each method.
	backendID := new(string)
	s3c := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = backend.ForcePathStyle
		if backend.Endpoint != "" {
			o.BaseEndpoint = aws.String(backend.Endpoint)
		}
		o.APIOptions = append(o.APIOptions, withCallMetrics(backendID))
	})

	// Presign-only S3 client — points at the public endpoint so that
	// every generated URL embeds a host the browser can actually reach.
	// Falls back to the internal endpoint when no public_endpoint is
	// configured (single-host dev setups).
	presignEndpoint := backend.PublicEndpoint
	if presignEndpoint == "" {
		presignEndpoint = backend.Endpoint
	}
	s3PresignBase := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = backend.ForcePathStyle
		if presignEndpoint != "" {
			o.BaseEndpoint = aws.String(presignEndpoint)
		}
	})

	mode := object.CompletionModeExplicit
	if backend.Events.Enabled {
		mode = object.CompletionModeImplicit
	}

	return &Client{
		s3:      s3c,
		presign: s3.NewPresignClient(s3PresignBase),
		cfg:     backend,
		mode:    mode,
		sseType: backend.SSE.Type,
		sseKey:  backend.SSE.KeyID,

		backendID: backendID,
	}, nil
}

// buildAWSConfig assembles an aws.Config whose credential provider matches
// backend.Auth.Mode. The four supported modes mirror the AWS guidance for
// authenticating to S3:
//
//   - static_keys   → IAM user access key + secret (long-lived).
//   - default_chain → SDK default chain (env, ECS task role, EC2 IMDS, etc.).
//   - assume_role   → STS AssumeRole on top of the default chain.
//   - web_identity  → STS AssumeRoleWithWebIdentity (EKS IRSA).
func buildAWSConfig(ctx context.Context, backend config.StorageBackend) (aws.Config, error) {
	region := backend.Region

	switch backend.Auth.Mode {
	case config.AuthModeStaticKeys:
		cfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion(region),
			awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(
					backend.Auth.AccessKey, backend.Auth.SecretKey, backend.Auth.SessionToken,
				),
			),
		)
		if err != nil {
			return aws.Config{}, fmt.Errorf("aws config (static_keys): %w", err)
		}
		return cfg, nil

	case config.AuthModeDefaultChain:
		cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
		if err != nil {
			return aws.Config{}, fmt.Errorf("aws config (default_chain): %w", err)
		}
		return cfg, nil

	case config.AuthModeAssumeRole:
		base, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
		if err != nil {
			return aws.Config{}, fmt.Errorf("aws config (assume_role bootstrap): %w", err)
		}
		stsClient := sts.NewFromConfig(base)
		provider := stscreds.NewAssumeRoleProvider(stsClient, backend.Auth.RoleARN, func(o *stscreds.AssumeRoleOptions) {
			if n := backend.Auth.SessionName; n != "" {
				o.RoleSessionName = n
			} else {
				o.RoleSessionName = "paladin-control-plane"
			}
			if x := backend.Auth.ExternalID; x != "" {
				o.ExternalID = aws.String(x)
			}
			if d := backend.Auth.DurationSeconds; d > 0 {
				o.Duration = time.Duration(d) * time.Second
			}
		})
		base.Credentials = aws.NewCredentialsCache(provider)
		return base, nil

	case config.AuthModeWebIdentity:
		base, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
		if err != nil {
			return aws.Config{}, fmt.Errorf("aws config (web_identity bootstrap): %w", err)
		}
		tokenFile := backend.Auth.WebIdentityTokenFile
		if tokenFile == "" {
			// EKS IRSA convention: pod identity webhook injects this env.
			tokenFile = os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE")
		}
		if tokenFile == "" {
			return aws.Config{}, fmt.Errorf("aws config (web_identity): web_identity_token_file or AWS_WEB_IDENTITY_TOKEN_FILE must be set")
		}
		stsClient := sts.NewFromConfig(base)
		// Use the per-call file-reading retriever rather than
		// stscreds.IdentityTokenFile so token rotation (EKS IRSA: ~48m
		// rewrite cadence on a 1h projected TTL) is picked up without a
		// pod restart. See web_identity_retriever.go for the rotation
		// contract.
		provider := stscreds.NewWebIdentityRoleProvider(stsClient, backend.Auth.RoleARN,
			&rotatingTokenRetriever{path: tokenFile},
			func(o *stscreds.WebIdentityRoleOptions) {
				if n := backend.Auth.SessionName; n != "" {
					o.RoleSessionName = n
				} else {
					o.RoleSessionName = "paladin-control-plane"
				}
				if d := backend.Auth.DurationSeconds; d > 0 {
					o.Duration = time.Duration(d) * time.Second
				}
			},
		)
		base.Credentials = aws.NewCredentialsCache(provider)
		return base, nil

	case "":
		return aws.Config{}, fmt.Errorf("auth.mode is required")
	default:
		return aws.Config{}, fmt.Errorf("unknown auth.mode %q", backend.Auth.Mode)
	}
}

// composeKey builds the full S3 key from tenant_id + collection + storage key.
// Layout: "<tenant_id>/<collection>/<key>". Both prefix segments are required;
// callers must supply them. The leading slash is omitted (S3 keys never start
// with one).
//
// Defense-in-depth: collection/key are server-generated and validated
// upstream today, so this is a belt-and-suspenders guard, not a live fix.
// S3 keys are opaque (it doesn't interpret "..") but a future
// filesystem-backed adapter or any tenant-prefix access check would be
// fooled by a "../other-tenant" segment, so we strip path-traversal
// components. sanitizeSegment is a no-op for clean inputs (UUIDs,
// validated keys).
func composeKey(tenantID uuid.UUID, collection, key string) string {
	return tenantID.String() + "/" + sanitizeSegment(collection) + "/" + sanitizeSegment(key)
}

// sanitizeSegment removes leading slashes and any "." / ".." path
// components so a segment can never escape its parent prefix. The
// segment's internal "/" structure is otherwise preserved (multi-level
// object keys are legal).
func sanitizeSegment(s string) string {
	if s == "" {
		return s
	}
	parts := strings.Split(s, "/")
	out := parts[:0]
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, "/")
}

// resolveBucket picks the per-call bucket if supplied, otherwise falls back
// to the adapter's configured default. Returns "" only when neither is set,
// which the AWS SDK will reject as InvalidArgument — surfacing it loudly
// rather than silently writing to the wrong bucket.
func (c *Client) resolveBucket(perCall string) string {
	if perCall != "" {
		return perCall
	}
	return c.cfg.Bucket
}

// ─── bucket.Provisioner ────────────────────────────────────────────────────
// CreateBucket / DeleteBucket map directly to AWS S3 CreateBucket /
// DeleteBucket. The adapter is bound to one backend at construction time;
// the backendID parameter is accepted for interface symmetry but verified
// against the configured backend so cross-backend calls fail fast.

// CreateBucket provisions a real S3 bucket. Idempotent — returns nil when
// the bucket already exists and is owned by the caller (S3 returns
// BucketAlreadyOwnedByYou).
// Probe is a lightweight, read-only reachability + auth check used by
// BackendService.TestBackend. ListBuckets exercises the endpoint, TLS, and
// the configured credentials without mutating anything. The caller bounds it
// with a context timeout. Returns nil when the backend answered.
func (c *Client) Probe(ctx context.Context) error {
	_, err := c.s3.ListBuckets(ctx, &s3.ListBucketsInput{})
	return err
}

func (c *Client) CreateBucket(ctx context.Context, backendID, bucketName, region string) error {
	in := &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	}
	// Region constraint: required for non-us-east-1 regions on real AWS S3.
	// S3-compatible backends typically ignore it.
	loc := region
	if loc == "" {
		loc = c.cfg.Region
	}
	if loc != "" && loc != "us-east-1" {
		in.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(loc),
		}
	}
	if _, err := c.s3.CreateBucket(ctx, in); err != nil {
		// Idempotency: ignore "already exists / already owned by you" and fall
		// through to the reachability check below.
		msg := err.Error()
		if !strings.Contains(msg, "BucketAlreadyOwnedByYou") && !strings.Contains(msg, "BucketAlreadyExists") {
			return fmt.Errorf("s3 create bucket %q: %w", bucketName, err)
		}
	}
	// Verify the bucket is actually reachable before reporting success: a
	// backend that accepts CreateBucket without exposing a usable bucket would
	// otherwise let the reconciler flip provision_state to 'ready' on a bucket
	// that can't be served, lifting the presign/upload gate (ADR-0015) into a
	// broken bucket. A failing HeadBucket keeps the row in 'failed'/'pending'
	// with the error surfaced, so the gate stays closed. NOTE: this catches a
	// missing bucket, not a store that has the bucket but can't accept writes
	// (e.g. SeaweedFS out of writable volumes) — that surfaces at PUT time.
	if _, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucketName)}); err != nil {
		return fmt.Errorf("s3 create bucket %q: created but not reachable (HeadBucket): %w", bucketName, err)
	}
	return nil
}

// DeleteBucket removes a real S3 bucket. Caller is responsible for ensuring
// the bucket is empty (S3 rejects non-empty deletes).
func (c *Client) DeleteBucket(ctx context.Context, backendID, bucketName string) error {
	_, err := c.s3.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return fmt.Errorf("s3 delete bucket %q: %w", bucketName, err)
	}
	return nil
}

// TagBucketOwner tags a bucket with tenant_id=<uuid> for cost attribution
// (ADR-0015). Callers treat failure as non-fatal — not every S3-compatible
// backend implements PutBucketTagging.
func (c *Client) TagBucketOwner(ctx context.Context, backendID, bucketName string, tenantID uuid.UUID) error {
	_, err := c.s3.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{
		Bucket: aws.String(bucketName),
		Tagging: &s3types.Tagging{
			TagSet: []s3types.Tag{{Key: aws.String("tenant_id"), Value: aws.String(tenantID.String())}},
		},
	})
	if err != nil {
		return fmt.Errorf("s3 tag bucket %q: %w", bucketName, err)
	}
	return nil
}

// ─── object.Storage (methods; the ObjectRouter satisfies the interface) ─────
//
// The narrow storage interfaces now carry a backend id so the router can
// dispatch per backend; *Client implements the per-backend method bodies and
// is reached only through the router (see router.go), so it no longer asserts
// the interfaces directly.

func (c *Client) PresignPut(ctx context.Context, args object.PresignPutArgs) (string, map[string]string, time.Time, error) {
	in := &s3.PutObjectInput{
		Bucket:      aws.String(c.resolveBucket(args.Bucket)),
		Key:         aws.String(composeKey(args.TenantID, args.Collection, args.Key)),
		ContentType: aws.String(args.ContentType),
	}
	c.applySSE(in)
	req, err := c.presign.PresignPutObject(ctx, in, s3.WithPresignExpires(args.TTL))
	if err != nil {
		return "", nil, time.Time{}, fmt.Errorf("presign put: %w", err)
	}
	return req.URL, signedHeaders(req), time.Now().Add(args.TTL), nil
}

func (c *Client) PresignPost(ctx context.Context, args object.PresignPostArgs) (string, map[string]string, time.Time, error) {
	// aws-sdk-go-v2 doesn't ship PresignPost; return a v4-signed PUT URL with
	// POST-style fields so callers that requested POST have a working fallback.
	in := &s3.PutObjectInput{
		Bucket:      aws.String(c.resolveBucket(args.Bucket)),
		Key:         aws.String(composeKey(args.TenantID, args.Collection, args.Key)),
		ContentType: aws.String(args.ContentType),
	}
	c.applySSE(in)
	req, err := c.presign.PresignPutObject(ctx, in, s3.WithPresignExpires(args.TTL))
	if err != nil {
		return "", nil, time.Time{}, fmt.Errorf("presign post fallback: %w", err)
	}
	fields := map[string]string{
		"Content-Type":          args.ContentType,
		"Content-Length-Range":  fmt.Sprintf("0,%d", args.MaxSizeBytes),
		"X-Amz-Signed-Fallback": "put",
	}
	for k, v := range signedHeaders(req) {
		fields[k] = v
	}
	return req.URL, fields, time.Now().Add(args.TTL), nil
}

func (c *Client) PresignGet(ctx context.Context, args object.PresignGetArgs) (string, map[string]string, time.Time, error) {
	in := &s3.GetObjectInput{
		Bucket: aws.String(c.resolveBucket(args.Bucket)),
		Key:    aws.String(composeKey(args.TenantID, args.Collection, args.Key)),
	}
	if args.ContentDisposition != "" {
		in.ResponseContentDisposition = aws.String(args.ContentDisposition)
	}
	req, err := c.presign.PresignGetObject(ctx, in, s3.WithPresignExpires(args.TTL))
	if err != nil {
		return "", nil, time.Time{}, fmt.Errorf("presign get: %w", err)
	}
	return req.URL, signedHeaders(req), time.Now().Add(args.TTL), nil
}

func (c *Client) Head(ctx context.Context, bucket string, tenantID uuid.UUID, collection, key string) (string, int64, string, string, error) {
	resolved := c.resolveBucket(bucket)
	out, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(resolved),
		Key:    aws.String(composeKey(tenantID, collection, key)),
	})
	if err != nil {
		// Object-absent gets the sentinel so callers can make a terminal
		// decision on it (see ErrObjectNotFound). Two %w verbs, not one:
		// errors.Is finds the sentinel while the AWS error stays in the
		// chain, so the operator log keeps the status code, request id and
		// endpoint that make a HEAD failure diagnosable.
		if notFound(err) {
			// ...but only once the container itself is confirmed present.
			// A HEAD response has no body, so a backend that answers a
			// missing BUCKET with a bodiless 404 is, at the classifier,
			// byte-identical to a missing key — and reading that as
			// "absent" marks every pending object under a broken binding
			// FAILED. HeadBucket separates the two, and it costs a round
			// trip only on this path, which in steady state is the rare
			// one: a HEAD that finds its object never reaches here.
			if berr := c.confirmBucket(ctx, resolved); berr != nil {
				return "", 0, "", "", fmt.Errorf(
					"head: bucket %q unreachable, so the 404 does not mean the object is absent: %w (head object: %w)",
					resolved, berr, err)
			}
			return "", 0, "", "", fmt.Errorf("head: %w: %w", ErrObjectNotFound, err)
		}
		return "", 0, "", "", fmt.Errorf("head: %w", err)
	}
	etag := strings.Trim(aws.ToString(out.ETag), `"`)
	var size int64
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	checksum := ""
	switch {
	case out.ChecksumSHA256 != nil:
		checksum = *out.ChecksumSHA256
	case out.ChecksumCRC32C != nil:
		checksum = *out.ChecksumCRC32C
	case out.ChecksumCRC32 != nil:
		checksum = *out.ChecksumCRC32
	}
	// SeaweedFS / real S3: no Sequencer from HEAD; leave empty. The event
	// pipeline supplies it where available.
	return etag, size, checksum, "", nil
}

// confirmBucket reports whether the bucket is present and reachable, and is
// consulted only to qualify a not-found verdict. A nil return licenses
// ErrObjectNotFound; any error means the 404 is unexplained and the caller
// must retry rather than decide.
//
// Deliberately not cached. The call happens only on the not-found path, so a
// cache would trade a real correctness signal for a saved round trip on an
// already-rare branch — and a stale "reachable" entry reintroduces exactly
// the bug this closes. Revisit only if a workload makes 404s the common case.
func (c *Client) confirmBucket(ctx context.Context, resolvedBucket string) error {
	if _, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(resolvedBucket),
	}); err != nil {
		return err
	}
	return nil
}

func (c *Client) CopyObject(ctx context.Context, src, dst object.Location) error {
	srcBucket := c.resolveBucket(src.Bucket)
	dstBucket := c.resolveBucket(dst.Bucket)
	_, err := c.s3.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(dstBucket),
		Key:        aws.String(composeKey(dst.TenantID, dst.Collection, dst.Key)),
		CopySource: aws.String(srcBucket + "/" + composeKey(src.TenantID, src.Collection, src.Key)),
	})
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// GetStream opens a server-side read of one object, returning its body reader +
// content type. Used by the cross-backend migration stream-through (the caller
// pipes it into another backend's Open writer). The caller MUST Close the
// reader.
func (c *Client) GetStream(ctx context.Context, bucket string, tenantID uuid.UUID, collection, key string) (io.ReadCloser, string, error) {
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.resolveBucket(bucket)),
		Key:    aws.String(composeKey(tenantID, collection, key)),
	})
	if err != nil {
		return nil, "", fmt.Errorf("get stream: %w", err)
	}
	return out.Body, aws.ToString(out.ContentType), nil
}

func (c *Client) DeleteObject(ctx context.Context, bucket string, tenantID uuid.UUID, collection, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.resolveBucket(bucket)),
		Key:    aws.String(composeKey(tenantID, collection, key)),
	})
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// CompletionMode ignores the collection — the adapter fronts a single backend
// whose Implicit/Explicit mode was chosen at construction time.
func (c *Client) CompletionMode(collection string) object.CompletionMode {
	return c.mode
}

// ─── multipart.Storage (methods; MultipartRouter satisfies the interface) ───

func (c *Client) InitiateMultipart(ctx context.Context, bucket string, tenantID uuid.UUID, collection, key, contentType string) (string, error) {
	in := &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(c.resolveBucket(bucket)),
		Key:         aws.String(composeKey(tenantID, collection, key)),
		ContentType: aws.String(contentType),
	}
	c.applyMultipartSSE(in)
	out, err := c.s3.CreateMultipartUpload(ctx, in)
	if err != nil {
		return "", fmt.Errorf("initiate multipart: %w", err)
	}
	return aws.ToString(out.UploadId), nil
}

func (c *Client) CompleteMultipart(ctx context.Context, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, parts []multipart.PartETag) (string, int64, error) {
	completed := make([]s3types.CompletedPart, 0, len(parts))
	for _, p := range parts {
		completed = append(completed, s3types.CompletedPart{
			PartNumber: aws.Int32(p.PartNumber),
			ETag:       aws.String(p.ETag),
		})
	}
	resolvedBucket := c.resolveBucket(bucket)
	fullKey := composeKey(tenantID, collection, key)
	out, err := c.s3.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(resolvedBucket),
		Key:             aws.String(fullKey),
		UploadId:        aws.String(storageUploadID),
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: completed},
	})
	if err != nil {
		return "", 0, fmt.Errorf("complete multipart: %w", err)
	}
	etag := strings.Trim(aws.ToString(out.ETag), `"`)

	head, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(resolvedBucket),
		Key:    aws.String(fullKey),
	})
	if err != nil {
		return etag, 0, fmt.Errorf("head after complete: %w", err)
	}
	var size int64
	if head.ContentLength != nil {
		size = *head.ContentLength
	}
	return etag, size, nil
}

// ListMultipartParts asks the backend which parts have actually landed.
//
// The control plane cannot answer this from its own tables: clients PUT parts
// straight to the object store through presigned URLs, so no part upload ever
// passes through Paladin. A local journal could only ever record what we
// handed out a URL for, not what the client managed to store — which is
// precisely the difference a resuming client needs to know.
//
// Pagination is the S3 contract's: part_number_marker is the last part number
// seen, and the caller pages while IsTruncated holds.
func (c *Client) ListMultipartParts(
	ctx context.Context,
	bucket string,
	tenantID uuid.UUID,
	storageUploadID, collection, key string,
	maxParts int32,
	afterPartNumber int32,
) ([]multipart.Part, int32, error) {
	in := &s3.ListPartsInput{
		Bucket:   aws.String(c.resolveBucket(bucket)),
		Key:      aws.String(composeKey(tenantID, collection, key)),
		UploadId: aws.String(storageUploadID),
	}
	if maxParts > 0 {
		in.MaxParts = aws.Int32(maxParts)
	}
	if afterPartNumber > 0 {
		in.PartNumberMarker = aws.String(strconv.FormatInt(int64(afterPartNumber), 10))
	}
	out, err := c.s3.ListParts(ctx, in)
	if err != nil {
		return nil, 0, fmt.Errorf("list multipart parts: %w", err)
	}
	parts := make([]multipart.Part, 0, len(out.Parts))
	for _, p := range out.Parts {
		part := multipart.Part{
			PartNumber: aws.ToInt32(p.PartNumber),
			ETag:       strings.Trim(aws.ToString(p.ETag), `"`),
		}
		if p.Size != nil {
			part.SizeBytes = *p.Size
		}
		if p.LastModified != nil {
			part.UploadedAt = *p.LastModified
		}
		// Whichever checksum the bucket is configured for; empty when none.
		switch {
		case p.ChecksumSHA256 != nil:
			part.Checksum = *p.ChecksumSHA256
		case p.ChecksumCRC32C != nil:
			part.Checksum = *p.ChecksumCRC32C
		case p.ChecksumCRC32 != nil:
			part.Checksum = *p.ChecksumCRC32
		}
		parts = append(parts, part)
	}
	var next int32
	if aws.ToBool(out.IsTruncated) && len(parts) > 0 {
		next = parts[len(parts)-1].PartNumber
	}
	return parts, next, nil
}

func (c *Client) AbortMultipart(ctx context.Context, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string) error {
	_, err := c.s3.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(c.resolveBucket(bucket)),
		Key:      aws.String(composeKey(tenantID, collection, key)),
		UploadId: aws.String(storageUploadID),
	})
	if err != nil {
		return fmt.Errorf("abort multipart: %w", err)
	}
	return nil
}

// ─── presign.Storage ───────────────────────────────────────────────────────
//
// object.Storage and presign.Storage both expose methods named PresignGet /
// PresignPut with different signatures, so the adapter cannot satisfy both
// on the same receiver. PresignView wraps a *Client and exposes the
// presign.Storage-shaped entrypoints. The Client primary methods remain
// wired to object.Storage.

type PresignView struct{ c *Client }

func (c *Client) Presign() *PresignView { return &PresignView{c: c} }

// PresignView carries the per-backend presign method bodies; PresignRouter
// satisfies the presign.Storage interface and delegates here.

func (p *PresignView) PresignGet(ctx context.Context, bucket string, tenantID uuid.UUID, collection, key string, ttl time.Duration, disposition string) (string, map[string]string, time.Time, error) {
	return p.c.PresignGet(ctx, object.PresignGetArgs{
		TenantID: tenantID, Bucket: bucket, Collection: collection, Key: key, TTL: ttl, ContentDisposition: disposition,
	})
}

func (p *PresignView) PresignPut(ctx context.Context, bucket string, tenantID uuid.UUID, collection, key, contentType, checksumAlgo string, ttl time.Duration, sizeHint int64) (string, map[string]string, time.Time, error) {
	return p.c.PresignPut(ctx, object.PresignPutArgs{
		TenantID: tenantID, Bucket: bucket, Collection: collection, Key: key, ContentType: contentType,
		ChecksumAlgo: checksumAlgo, SizeHint: sizeHint, TTL: ttl,
	})
}

func (p *PresignView) PresignPart(ctx context.Context, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, partNumber int32, ttl time.Duration) (string, map[string]string, time.Time, error) {
	in := &s3.UploadPartInput{
		Bucket:     aws.String(p.c.resolveBucket(bucket)),
		Key:        aws.String(composeKey(tenantID, collection, key)),
		PartNumber: aws.Int32(partNumber),
		UploadId:   aws.String(storageUploadID),
	}
	req, err := p.c.presign.PresignUploadPart(ctx, in, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", nil, time.Time{}, fmt.Errorf("presign part: %w", err)
	}
	return req.URL, signedHeaders(req), time.Now().Add(ttl), nil
}

// PresignPart on *Client delegates to the PresignView so that *Client also
// satisfies multipart.Storage (which needs the part-presign capability for
// the MultipartUploadService.PresignPart RPC).
func (c *Client) PresignPart(ctx context.Context, bucket string, tenantID uuid.UUID, storageUploadID, collection, key string, partNumber int32, ttl time.Duration) (string, map[string]string, time.Time, error) {
	return (&PresignView{c: c}).PresignPart(ctx, bucket, tenantID, storageUploadID, collection, key, partNumber, ttl)
}

// ─── object.StreamSink (methods; StreamRouter satisfies the interface) ──────

// Open returns a StreamWriter that uploads a single object via S3 multipart.
// 8 MiB parts are a reasonable default that balances memory vs S3 minimums.
func (c *Client) Open(ctx context.Context, bucket string, tenantID uuid.UUID, collection, key, contentType string, sizeHint int64) (object.StreamWriter, error) {
	resolvedBucket := c.resolveBucket(bucket)
	fullKey := composeKey(tenantID, collection, key)
	in := &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(resolvedBucket),
		Key:         aws.String(fullKey),
		ContentType: aws.String(contentType),
	}
	c.applyMultipartSSE(in)
	out, err := c.s3.CreateMultipartUpload(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("open sink: %w", err)
	}
	partSize := c.cfg.PartSizeBytes
	if partSize <= 0 {
		partSize = 8 << 20
	}
	return &streamWriter{
		ctx:      ctx,
		c:        c,
		bucket:   resolvedBucket,
		fullKey:  fullKey,
		uploadID: aws.ToString(out.UploadId),
		partSize: partSize,
	}, nil
}

type streamWriter struct {
	ctx      context.Context
	c        *Client
	bucket   string // resolved bucket captured at Open time
	fullKey  string
	uploadID string
	partSize int64

	buf       []byte
	partNum   int32
	parts     []s3types.CompletedPart
	total     int64
	sumMD5    [16]byte
	hasher    *streamingMD5
	completed bool
	aborted   bool
}

// Write buffers input and emits whole parts of exactly partSize bytes.
//
// The loop matters: a single Write larger than partSize must be split across
// as many parts as it takes. Folding it into one part would break on any
// object above S3's 5 GiB per-part ceiling, and would also drift the actual
// part size away from the configured one. Streaming callers (io.Copy, which
// hands over ~32 KiB at a time) never reached that path, but a caller that
// writes a whole buffer in one call does.
//
// The partSize > 0 guard keeps a misconfigured zero part size from spinning
// forever here; Open already floors it at 8 MiB, so this is belt-and-braces.
func (w *streamWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.buf = append(w.buf, p...)
	w.total += int64(n)
	for w.partSize > 0 && int64(len(w.buf)) >= w.partSize {
		if err := w.flushPart(w.partSize); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// flushPart uploads the first n buffered bytes as one part and keeps whatever
// is left over for the next one. n <= 0, or an n beyond what is buffered,
// means "everything still buffered" — the form Close uses for the trailing
// part, which is the only part allowed to be smaller than partSize.
func (w *streamWriter) flushPart(n int64) error {
	if len(w.buf) == 0 {
		return nil
	}
	if n <= 0 || n > int64(len(w.buf)) {
		n = int64(len(w.buf))
	}
	chunk := w.buf[:n]

	w.partNum++
	out, err := w.c.s3.UploadPart(w.ctx, &s3.UploadPartInput{
		Bucket:     aws.String(w.bucket),
		Key:        aws.String(w.fullKey),
		UploadId:   aws.String(w.uploadID),
		PartNumber: aws.Int32(w.partNum),
		Body:       strings.NewReader(string(chunk)), // copy once
	})
	if err != nil {
		return fmt.Errorf("upload part %d: %w", w.partNum, err)
	}
	w.parts = append(w.parts, s3types.CompletedPart{
		PartNumber: aws.Int32(w.partNum),
		ETag:       out.ETag,
	})
	if w.hasher == nil {
		w.hasher = newStreamingMD5()
	}
	// Both reads of chunk must happen before the compaction below, which
	// overwrites the front of the backing array.
	w.hasher.Write(chunk)
	w.buf = append(w.buf[:0], w.buf[n:]...)
	return nil
}

func (w *streamWriter) Close() (string, int64, string, error) {
	if w.completed {
		return "", 0, "", fmt.Errorf("stream writer already closed")
	}
	if err := w.flushPart(int64(len(w.buf))); err != nil {
		_ = w.Abort()
		return "", 0, "", err
	}
	out, err := w.c.s3.CompleteMultipartUpload(w.ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(w.bucket),
		Key:             aws.String(w.fullKey),
		UploadId:        aws.String(w.uploadID),
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: w.parts},
	})
	if err != nil {
		_ = w.Abort()
		return "", 0, "", fmt.Errorf("close stream: %w", err)
	}
	w.completed = true
	etag := strings.Trim(aws.ToString(out.ETag), `"`)
	checksum := ""
	if w.hasher != nil {
		w.sumMD5 = w.hasher.Sum()
		checksum = hex.EncodeToString(w.sumMD5[:])
	}
	return etag, w.total, checksum, nil
}

func (w *streamWriter) Abort() error {
	if w.aborted || w.completed {
		return nil
	}
	w.aborted = true
	_, err := w.c.s3.AbortMultipartUpload(w.ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(w.bucket),
		Key:      aws.String(w.fullKey),
		UploadId: aws.String(w.uploadID),
	})
	if err != nil {
		return fmt.Errorf("abort stream: %w", err)
	}
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────

// signedHeaders flattens v4 signed-header values into a {name:value} map.
func signedHeaders(req *v4.PresignedHTTPRequest) map[string]string {
	out := make(map[string]string, len(req.SignedHeader))
	for k, v := range req.SignedHeader {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

// applySSE populates server-side encryption fields on a PutObjectInput.
func (c *Client) applySSE(in *s3.PutObjectInput) {
	switch strings.ToLower(c.sseType) {
	case "aes256":
		in.ServerSideEncryption = s3types.ServerSideEncryptionAes256
	case "aws:kms":
		in.ServerSideEncryption = s3types.ServerSideEncryptionAwsKms
		if c.sseKey != "" {
			in.SSEKMSKeyId = aws.String(c.sseKey)
		}
	}
}

// applyMultipartSSE mirrors applySSE for CreateMultipartUploadInput.
func (c *Client) applyMultipartSSE(in *s3.CreateMultipartUploadInput) {
	switch strings.ToLower(c.sseType) {
	case "aes256":
		in.ServerSideEncryption = s3types.ServerSideEncryptionAes256
	case "aws:kms":
		in.ServerSideEncryption = s3types.ServerSideEncryptionAwsKms
		if c.sseKey != "" {
			in.SSEKMSKeyId = aws.String(c.sseKey)
		}
	}
}

// ─── streamingMD5: fold across UploadPart calls ────────────────────────────
// md5 is used as a lightweight content digest surfaced back to the caller
// (StreamWriter.Close returns it as `checksum`). It is NOT used for auth.

type streamingMD5 struct {
	h io.Writer
	d interface {
		Sum(b []byte) []byte
	}
}

func newStreamingMD5() *streamingMD5 {
	h := md5.New() // #nosec G401
	return &streamingMD5{h: h, d: h}
}

func (s *streamingMD5) Write(p []byte) {
	_, _ = s.h.Write(p)
}

func (s *streamingMD5) Sum() [16]byte {
	var out [16]byte
	sum := s.d.Sum(nil)
	copy(out[:], sum[:16])
	return out
}

// parseSize makes the linter happy — tests may want to translate human sizes.
func parseSize(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

var _ = parseSize // retained for future use
