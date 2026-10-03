// Package uploadpolicy decides what an upload may be: how large, of which
// Content-Type, with which checksum, split into which parts, and how long its
// URLs may live.
//
// Two sources feed it. limits.* in the config is the operator's ceiling for
// the whole service; a bucket's constraints (BucketService.SetConstraints)
// narrow it for the collections bound to that bucket. A bucket can tighten a
// global limit, never loosen it: the global value is a hard ceiling the
// operator set for the service, and a bucket owner raising it would undo
// that. Every path that creates an object — UploadObject, multipart, copy —
// checks the same Policy, so no path is the way around another.
package uploadpolicy

import (
	"errors"
	"fmt"
	"mime"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
)

// BucketConstraints is a bucket's upload constraints as BucketService stores
// them in buckets.constraints. A zero field means "inherit the global limit"
// — the contract the admin API publishes (proto BucketConstraints). The field
// names are the JSON keys of the stored document, so this is the one
// definition both the admin plane that writes it and the data plane that
// enforces it read.
type BucketConstraints struct {
	MaxObjectSizeBytes        int64
	MinPartSizeBytes          int64
	MaxPartSizeBytes          int64
	MaxParts                  int32
	AllowedContentTypes       []string
	MaxPresignPutTTL          time.Duration
	MaxPresignGetTTL          time.Duration
	RequiredChecksumAlgorithm string // "" | "CRC32C" | "SHA256" | "MD5"
}

// Limits is the global ceiling, from limits.* in the config.
type Limits struct {
	// MaxObjectSize bounds an object uploaded in one PUT or POST;
	// MaxMultipartSize bounds one assembled from parts.
	MaxObjectSize       int64
	MaxMultipartSize    int64
	MinPartSize         int64
	MaxPartSize         int64
	MaxParts            int64
	AllowedContentTypes []string
}

// SigV4MaxPresignExpiry is the longest X-Amz-Expires SigV4 accepts (604800
// seconds). A longer URL signs without complaint and is refused by the object
// store only when somebody uses it.
const SigV4MaxPresignExpiry = 7 * 24 * time.Hour

// Checksum algorithm names a bucket may require.
const (
	ChecksumCRC32C = "CRC32C"
	ChecksumSHA256 = "SHA256"
	ChecksumMD5    = "MD5"
)

// Validate refuses constraints no upload could satisfy, or that do not mean
// what they say: a part bound outside S3's, a minimum above the maximum, an
// unparseable media type, an algorithm Paladin does not know, a TTL ceiling
// SigV4 cannot express. Zero fields inherit and are always valid.
func (c BucketConstraints) Validate() error {
	if c.MaxObjectSizeBytes < 0 {
		return fmt.Errorf("max_object_size_bytes %d must not be negative", c.MaxObjectSizeBytes)
	}
	if c.MinPartSizeBytes != 0 && (c.MinPartSizeBytes < S3MinPartSize || c.MinPartSizeBytes > S3MaxPartSize) {
		return fmt.Errorf("min_part_size_bytes %d must be within S3's part bounds [%d, %d]", c.MinPartSizeBytes, S3MinPartSize, S3MaxPartSize)
	}
	if c.MaxPartSizeBytes != 0 && (c.MaxPartSizeBytes < S3MinPartSize || c.MaxPartSizeBytes > S3MaxPartSize) {
		return fmt.Errorf("max_part_size_bytes %d must be within S3's part bounds [%d, %d]", c.MaxPartSizeBytes, S3MinPartSize, S3MaxPartSize)
	}
	if c.MinPartSizeBytes != 0 && c.MaxPartSizeBytes != 0 && c.MinPartSizeBytes > c.MaxPartSizeBytes {
		return fmt.Errorf("min_part_size_bytes %d exceeds max_part_size_bytes %d", c.MinPartSizeBytes, c.MaxPartSizeBytes)
	}
	if c.MaxParts < 0 || int64(c.MaxParts) > S3MaxParts {
		return fmt.Errorf("max_parts %d must be within [0, %d]", c.MaxParts, S3MaxParts)
	}
	for _, ct := range c.AllowedContentTypes {
		if _, err := normalizeMediaType(ct); err != nil {
			return fmt.Errorf("allowed_content_types %q: %w", ct, err)
		}
	}
	for _, d := range []struct {
		key string
		v   time.Duration
	}{{"max_presign_put_ttl", c.MaxPresignPutTTL}, {"max_presign_get_ttl", c.MaxPresignGetTTL}} {
		if d.v < 0 || d.v > SigV4MaxPresignExpiry {
			return fmt.Errorf("%s %s must be within [0, %s]", d.key, d.v, SigV4MaxPresignExpiry)
		}
	}
	switch strings.ToUpper(c.RequiredChecksumAlgorithm) {
	case "", ChecksumCRC32C, ChecksumSHA256, ChecksumMD5:
	default:
		return fmt.Errorf("required_checksum_algorithm %q: want %s, %s or %s", c.RequiredChecksumAlgorithm, ChecksumCRC32C, ChecksumSHA256, ChecksumMD5)
	}
	return nil
}

// S3's multipart rules, which every supported backend follows. The configured
// part limits are clamped into them: a policy that asked for a part S3
// refuses would plan uploads that cannot complete.
const (
	S3MinPartSize int64 = 5 << 20
	S3MaxPartSize int64 = 5 << 30
	S3MaxParts    int64 = 10000
)

// Validate checks the global limits are usable: every ceiling positive, the
// part bounds ordered and within S3's.
func (l Limits) Validate() error {
	for _, v := range []struct {
		key string
		n   int64
	}{
		{"max_object_size", l.MaxObjectSize},
		{"max_multipart_size", l.MaxMultipartSize},
		{"min_part_size", l.MinPartSize},
		{"max_part_size", l.MaxPartSize},
		{"max_parts", l.MaxParts},
	} {
		if v.n <= 0 {
			return fmt.Errorf("limits.%s %d must be positive", v.key, v.n)
		}
	}
	if l.MinPartSize < S3MinPartSize {
		return fmt.Errorf("limits.min_part_size %d is below S3's minimum part %d", l.MinPartSize, S3MinPartSize)
	}
	if l.MaxPartSize > S3MaxPartSize {
		return fmt.Errorf("limits.max_part_size %d is above S3's maximum part %d", l.MaxPartSize, S3MaxPartSize)
	}
	if l.MinPartSize > l.MaxPartSize {
		return fmt.Errorf("limits.min_part_size %d exceeds max_part_size %d", l.MinPartSize, l.MaxPartSize)
	}
	if l.MaxParts > S3MaxParts {
		return fmt.Errorf("limits.max_parts %d is above S3's %d", l.MaxParts, S3MaxParts)
	}
	for _, ct := range l.AllowedContentTypes {
		if _, err := normalizeMediaType(ct); err != nil {
			return fmt.Errorf("limits.allowed_content_types %q: %w", ct, err)
		}
	}
	return nil
}

// Policy is the effective rule set for one bucket: the global limits narrowed
// by the bucket's constraints.
type Policy struct {
	maxSingle, maxMultipart    int64
	minPart, maxPart, maxParts int64
	// allowed is nil when any Content-Type is accepted; otherwise the
	// normalised media types that are.
	allowed          []string
	maxPutTTL        time.Duration // zero → no bucket ceiling
	maxGetTTL        time.Duration // zero → no bucket ceiling
	requiredChecksum string        // "" → any algorithm
}

// For combines the global limits with a bucket's constraints. The bucket's
// max_object_size_bytes bounds every object in it, however it is uploaded,
// so it narrows both the single-request and the multipart ceiling.
func For(global Limits, bucket BucketConstraints) Policy {
	p := Policy{
		maxSingle:        tighten(global.MaxObjectSize, bucket.MaxObjectSizeBytes),
		maxMultipart:     tighten(global.MaxMultipartSize, bucket.MaxObjectSizeBytes),
		minPart:          max(global.MinPartSize, bucket.MinPartSizeBytes),
		maxPart:          tighten(global.MaxPartSize, bucket.MaxPartSizeBytes),
		maxParts:         tighten(global.MaxParts, int64(bucket.MaxParts)),
		maxPutTTL:        bucket.MaxPresignPutTTL,
		maxGetTTL:        bucket.MaxPresignGetTTL,
		requiredChecksum: strings.ToUpper(bucket.RequiredChecksumAlgorithm),
	}
	p.allowed = intersectContentTypes(normalizeAll(global.AllowedContentTypes), normalizeAll(bucket.AllowedContentTypes))
	return p
}

// tighten returns the smaller of a global ceiling and a bucket's, where a
// bucket value of zero or less inherits the global one.
func tighten(global, bucket int64) int64 {
	if bucket <= 0 {
		return global
	}
	return min(global, bucket)
}

func normalizeAll(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, ct := range in {
		if n, err := normalizeMediaType(ct); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// intersectContentTypes: an empty list accepts anything, so the effective
// list is the other one; when both name types, only those in both are
// accepted. Two disjoint lists accept nothing, which is what the operator
// and the bucket owner jointly asked for.
func intersectContentTypes(global, bucket []string) []string {
	switch {
	case global == nil:
		return bucket
	case bucket == nil:
		return global
	}
	out := []string{}
	for _, ct := range bucket {
		if slices.Contains(global, ct) {
			out = append(out, ct)
		}
	}
	return out
}

// normalizeMediaType lower-cases a media type and drops its parameters:
// "Text/Plain; charset=utf-8" is text/plain for every allowlist purpose.
// mime.ParseMediaType also accepts a bare token, so the type/subtype shape
// is checked here.
func normalizeMediaType(ct string) (string, error) {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return "", err
	}
	typ, sub, ok := strings.Cut(mt, "/")
	if !ok || typ == "" || sub == "" {
		return "", fmt.Errorf("%q is not a type/subtype media type", mt)
	}
	return mt, nil
}

// Upload is what a caller asks to create.
type Upload struct {
	SizeBytes   int64
	ContentType string
	// ChecksumAlgorithm is the upload's algorithm name ("SHA256", "CRC32C",
	// "MD5").
	ChecksumAlgorithm string
}

func invalid(format string, args ...any) error {
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(format, args...))
}

// ErrContentTypeNotAllowed is wrapped by every allowlist refusal.
var ErrContentTypeNotAllowed = errors.New("content type is not allowed")

func (p Policy) checkCommon(u Upload) error {
	if u.SizeBytes < 0 {
		return invalid("size %d must not be negative", u.SizeBytes)
	}
	if p.allowed != nil {
		mt, err := normalizeMediaType(u.ContentType)
		if err != nil {
			return invalid("content type %q: %w", u.ContentType, err)
		}
		if !slices.Contains(p.allowed, mt) {
			return connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("%w: %q (accepted: %s)", ErrContentTypeNotAllowed, mt, strings.Join(p.allowed, ", ")))
		}
	}
	if p.requiredChecksum != "" && !strings.EqualFold(u.ChecksumAlgorithm, p.requiredChecksum) {
		return invalid("this bucket requires checksum algorithm %s, got %q", p.requiredChecksum, u.ChecksumAlgorithm)
	}
	return nil
}

// CheckSingle admits an upload made in one PUT or POST.
func (p Policy) CheckSingle(u Upload) error {
	if err := p.checkCommon(u); err != nil {
		return err
	}
	if u.SizeBytes > p.maxSingle {
		return invalid("size %d exceeds the single-request upload limit %d; use a multipart upload", u.SizeBytes, p.maxSingle)
	}
	return nil
}

// MaxSingleRequestSize is the largest object one PUT or POST may carry here.
func (p Policy) MaxSingleRequestSize() int64 { return p.maxSingle }

// CheckCopy admits a server-side copy into this bucket: the new object obeys
// the destination's type and checksum rules like any other upload, and may be
// no larger than the largest object the bucket accepts at all.
func (p Policy) CheckCopy(u Upload) error {
	if err := p.checkCommon(u); err != nil {
		return err
	}
	if u.SizeBytes > p.maxMultipart {
		return invalid("size %d exceeds the maximum object size %d", u.SizeBytes, p.maxMultipart)
	}
	return nil
}

// PartPlan is how a multipart upload is sliced.
type PartPlan struct {
	PartSize   int64
	TotalParts int32
}

// PlanMultipart admits a multipart upload and slices it: the smallest part
// size at or above the minimum, doubling while the part count is over the
// limit. The caller does not choose — the response promises the plan.
func (p Policy) PlanMultipart(u Upload) (PartPlan, error) {
	if u.SizeBytes <= 0 {
		return PartPlan{}, invalid("size %d: a multipart upload needs its size", u.SizeBytes)
	}
	if err := p.checkCommon(u); err != nil {
		return PartPlan{}, err
	}
	if u.SizeBytes > p.maxMultipart {
		return PartPlan{}, invalid("size %d exceeds the maximum multipart size %d", u.SizeBytes, p.maxMultipart)
	}
	minPart := max(p.minPart, S3MinPartSize)
	maxPart := min(p.maxPart, S3MaxPartSize)
	maxParts := min(p.maxParts, S3MaxParts)
	if minPart > maxPart {
		return PartPlan{}, invalid("this bucket's part limits are inconsistent: minimum %d above maximum %d", minPart, maxPart)
	}
	partSize := minPart
	for ceilDiv(u.SizeBytes, partSize) > maxParts {
		partSize *= 2
	}
	if partSize > maxPart {
		// Doubling overshot; the largest allowed part is the last thing to try.
		partSize = maxPart
	}
	n := ceilDiv(u.SizeBytes, partSize)
	if n > maxParts {
		return PartPlan{}, invalid("size %d needs %d parts of at most %d bytes, over the limit of %d parts",
			u.SizeBytes, n, maxPart, maxParts)
	}
	return PartPlan{PartSize: partSize, TotalParts: int32(n)}, nil
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

// PutTTLCeiling and GetTTLCeiling are the bucket's presign TTL ceilings;
// zero when the bucket sets none and the global max_ttl alone applies.
func (p Policy) PutTTLCeiling() time.Duration { return p.maxPutTTL }
func (p Policy) GetTTLCeiling() time.Duration { return p.maxGetTTL }
