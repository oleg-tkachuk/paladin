package s3adapter

import (
	"errors"
	"net/http"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// This file holds the adapter's exported error contract.
//
// Why its own file rather than a var next to Head() in s3.go: ErrObjectNotFound
// is consumed across package boundaries — store/postgres/adapters classifies on
// it, and the reconciler's terminal MarkFailed decision hangs off that
// classification. That makes it public API, and public API buried two hundred
// lines into a 30 KB all-in-one file is API nobody finds. The classification
// logic below is also the security-critical half of this change, so it gets to
// sit next to its own tests.
//
// Message style follows the newer sentinels in this tree (eventingest,
// ratelimit): "<package>: <what>". The older ones (api/v1/tenant) omit the
// prefix; the prefix wins here because this error surfaces in operator logs
// far from the code that raised it.

// ErrObjectNotFound reports that the backend has no object at the requested
// key. It means exactly that — the request reached the backend, the backend
// answered, and the answer was "absent".
//
// It does NOT mean "the HEAD failed". Callers use this to make terminal
// decisions (ReconcilerV2 flips PENDING → FAILED on it, and FAILED objects
// stop being served and become eligible for reclamation), so anything that
// might instead be a reachability, credential, or availability problem must
// stay a plain error and be retried. See notFound for the classification, and
// Client.Head for the bucket-reachability check that qualifies its one
// undecidable case before this sentinel is returned.
var ErrObjectNotFound = errors.New("s3adapter: object not found")

// codeNoSuchBucket is the S3 error code for a bucket the backend does not have.
const codeNoSuchBucket = "NoSuchBucket"

// bucketGone reports whether err is the backend saying the bucket does not
// exist. Only the typed error or its code count: a bare 404 can equally be a
// wrong endpoint or path-style mismatch, and reading that as "already deleted"
// would drop the row while the bucket lives on.
func bucketGone(err error) bool {
	var noSuchBucket *s3types.NoSuchBucket
	if errors.As(err, &noSuchBucket) {
		return true
	}
	var coder interface{ ErrorCode() string }
	return errors.As(err, &coder) && coder.ErrorCode() == codeNoSuchBucket
}

// bucketLevelCodes are S3 error codes that mean "the container is wrong or
// gone", never "this key is absent". They are checked first and always lose:
// a missing bucket looks superficially like a missing object (both 404) but
// implies every object under it is unreachable. Classifying one as
// not-found would mark an entire tenant's pending uploads FAILED on a
// misconfigured binding.
var bucketLevelCodes = map[string]struct{}{
	codeNoSuchBucket:    {},
	"InvalidBucketName": {},
}

// objectLevelCodes are the codes that unambiguously mean "no such key".
// HeadObject normally yields NotFound (there is no response body to carry a
// code, so the SDK synthesises one from the status); NoSuchKey is included
// because S3-compatible backends — this deployment runs garage and
// seaweedfs — are inconsistent about which they return for a HEAD.
var objectLevelCodes = map[string]struct{}{
	"NotFound":  {},
	"NoSuchKey": {},
}

// notFound reports whether err is the backend saying "no such object".
//
// Deliberately layered, rejections first:
//
//  1. Bucket-level codes lose outright, before anything else can match them.
//  2. The SDK's typed errors are the happy path on AWS-shaped responses.
//  3. Error *code* is checked before HTTP status, because a code is specific
//     where a status is not.
//  4. A bare 404 is accepted last, for backends whose responses the SDK could
//     not map to a typed error at all.
//
// Steps 3 and 4 match on anonymous interfaces rather than importing
// smithy-go's APIError and aws/transport/http's ResponseError. Both of those
// are transitive dependencies today (smithy-go is `// indirect` in go.mod),
// and matching structurally keeps this working across SDK releases that move
// or re-wrap those types — the shape is the contract, not the package path.
//
// One case this function cannot decide, by construction: a HEAD has no
// response body, so a backend that answers a missing *bucket* with a bodiless
// 404 and no error code is indistinguishable from a missing object here. Step
// 1 catches every backend that names the code; one that does not would be
// misread. That last gap is closed one layer up — Client.Head confirms the
// bucket with HeadBucket before it lets ErrObjectNotFound out — so a false
// accept below is caught rather than acted on. This function's job is to be
// cheap and right whenever the wire carries enough to be right.
func notFound(err error) bool {
	if err == nil {
		return false
	}

	// (1) Bucket-level typed errors — reject before any accept can fire.
	var noSuchBucket *s3types.NoSuchBucket
	if errors.As(err, &noSuchBucket) {
		return false
	}

	// (2) Object-level typed errors.
	var nf *s3types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	var noSuchKey *s3types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}

	// (3) Error code, when the response carried one.
	var coder interface{ ErrorCode() string }
	if errors.As(err, &coder) {
		code := coder.ErrorCode()
		if _, bucketLevel := bucketLevelCodes[code]; bucketLevel {
			return false
		}
		if _, objectLevel := objectLevelCodes[code]; objectLevel {
			return true
		}
		// A code we don't recognise is not a licence to guess. Anything
		// else — AccessDenied, SlowDown, InternalError — stays transient
		// even if it happens to arrive with a 404.
		return false
	}

	// (4) No code at all: fall back to the status line.
	var httpStatus interface{ HTTPStatusCode() int }
	if errors.As(err, &httpStatus) {
		return httpStatus.HTTPStatusCode() == http.StatusNotFound
	}

	// Transport-level failure — dial error, timeout, context cancellation.
	// Never not-found.
	return false
}
