// Package features is the catalog of the S3 features Paladin depends on and
// what a probe of a backend found for each (ADR-0026). A store's subset of S3
// is observed, never assumed from its name.
package features

import (
	"errors"
	"time"
)

// ErrUnsupported is an operation refused because the backend's last probe
// has not shown a feature it needs. Probing again may change the answer.
var ErrUnsupported = errors.New("storage backend feature not supported")

// Feature names one S3 behaviour Paladin relies on. The value is what
// storage_backend_features.feature stores.
type Feature string

// The catalog's features. StorageFeature in the admin proto mirrors this list;
// a test holds the two equal.
const (
	// ConditionalPut: a PUT carrying If-None-Match: * is refused with 412 when
	// the key exists, so an upload never replaces an object.
	ConditionalPut Feature = "conditional_put"
	// ChecksumSHA256: the store verifies x-amz-checksum-sha256 and refuses
	// bytes that do not match it.
	ChecksumSHA256 Feature = "checksum_sha256"
	// MultipartUpload: create, upload a part, complete.
	MultipartUpload Feature = "multipart_upload"
	// ServerSideCopy: CopyObject within the store.
	ServerSideCopy Feature = "server_side_copy"
	// PresignedPost: a browser form upload signed with a POST policy.
	PresignedPost Feature = "presigned_post"
	// BucketCreate: the credentials may create and delete a bucket.
	BucketCreate Feature = "bucket_create"
	// AnonymousReadPolicy: a bucket policy granting anonymous s3:GetObject on
	// a prefix is enforced — an unsigned GET succeeds inside the prefix and is
	// refused outside it.
	AnonymousReadPolicy Feature = "anonymous_read_policy"
)

// Support is what a probe of one feature found.
type Support string

const (
	// Supported: the store behaved as the feature requires.
	Supported Support = "supported"
	// Unsupported: the store answered, and the answer was wrong.
	Unsupported Support = "unsupported"
	// Unknown: the probe could not tell — never probed, no scratch bucket,
	// a denied permission, a timeout.
	Unknown Support = "unknown"
)

// Spec is one catalog entry.
type Spec struct {
	Feature Feature
	// Required: a guarantee Paladin makes depends on it, so a backend without
	// it is incompatible. Otherwise one operation needs it and is refused
	// where it is missing.
	Required bool
	// Enables names, as a noun phrase an operator's warning can quote, what
	// the feature is needed for.
	Enables string
}

// Catalog lists every feature in the order the console shows them.
var Catalog = []Spec{
	{ConditionalPut, true, "refusing an upload that would replace an existing object"},
	{ChecksumSHA256, true, "refusing bytes that do not match their declared checksum"},
	{MultipartUpload, true, "uploads above the multipart threshold"},
	{ServerSideCopy, false, "copying objects"},
	{PresignedPost, false, "browser form uploads"},
	{BucketCreate, false, "provisioning buckets on the backend"},
	{AnonymousReadPolicy, false, "public collections"},
}

// Lookup returns the catalog entry for f.
func Lookup(f Feature) (Spec, bool) {
	for _, s := range Catalog {
		if s.Feature == f {
			return s, true
		}
	}
	return Spec{}, false
}

// Result is one feature's probe outcome.
type Result struct {
	Feature Feature
	Support Support
	// Message says why, when the outcome is not Supported.
	Message   string
	CheckedAt time.Time
}

// EveryFeature returns one result per catalog feature, in catalog order: the
// recorded one where there is one, Unknown otherwise. A recorded feature the
// catalog no longer lists is dropped.
func EveryFeature(recorded []Result) []Result {
	byFeature := make(map[Feature]Result, len(recorded))
	for _, r := range recorded {
		byFeature[r.Feature] = r
	}
	out := make([]Result, 0, len(Catalog))
	for _, s := range Catalog {
		r, ok := byFeature[s.Feature]
		if !ok {
			r = Result{Feature: s.Feature, Support: Unknown}
		}
		out = append(out, r)
	}
	return out
}

// SupportOf returns what was recorded for f, Unknown when nothing was.
func SupportOf(recorded []Result, f Feature) Support {
	for _, r := range recorded {
		if r.Feature == f {
			return r.Support
		}
	}
	return Unknown
}

// Compatibility summarises a backend's required features.
type Compatibility string

const (
	// Unverified: no required feature is Unsupported, and not all are
	// Supported yet.
	Unverified Compatibility = "unverified"
	// Compatible: every required feature is Supported.
	Compatible Compatibility = "compatible"
	// Incompatible: a required feature is Unsupported.
	Incompatible Compatibility = "incompatible"
)

// Assess summarises recorded results over the catalog's required features.
func Assess(recorded []Result) Compatibility {
	verdict := Compatible
	for _, s := range Catalog {
		if !s.Required {
			continue
		}
		switch SupportOf(recorded, s.Feature) {
		case Unsupported:
			return Incompatible
		case Unknown:
			verdict = Unverified
		case Supported:
		}
	}
	return verdict
}
