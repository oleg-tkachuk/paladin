package uploadpolicy

import (
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
)

// contentTypeSep separates the entries of the fuzzed allowlist.
const contentTypeSep = ","

// splitList is the allowlist one fuzzed string spells; empty is none.
func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, contentTypeSep)
}

// policyFrom is the Policy a valid configuration and bucket build, or false
// when either would be refused before any upload reached it: the product only
// ever plans against limits that passed Validate.
func policyFrom(l Limits, b BucketConstraints) (Policy, bool) {
	if l.Validate() != nil || b.Validate() != nil {
		return Policy{}, false
	}
	return For(l, b), true
}

// Any multipart upload against any valid limits: refused as InvalidArgument,
// or a plan whose parts are within every bound and cover the object exactly —
// all but the last part full, the last one not empty.
func FuzzPlanMultipart(f *testing.F) {
	f.Add(int64(1<<30), global.MaxMultipartSize, global.MinPartSize, global.MaxPartSize, global.MaxParts, int64(0), int32(0))
	f.Add(int64(9_999_999_999_999), global.MaxMultipartSize, global.MinPartSize, global.MaxPartSize, global.MaxParts, int64(0), int32(1))
	f.Add(int64(1), global.MaxMultipartSize, global.MinPartSize, global.MaxPartSize, global.MaxParts, 64*mib, int32(10))
	f.Fuzz(func(t *testing.T, size, maxMultipart, minPart, maxPart, maxParts, bucketMinPart int64, bucketMaxParts int32) {
		l := Limits{
			MaxObjectSize:    global.MaxObjectSize,
			MaxMultipartSize: maxMultipart,
			MinPartSize:      minPart,
			MaxPartSize:      maxPart,
			MaxParts:         maxParts,
		}
		p, ok := policyFrom(l, BucketConstraints{MinPartSizeBytes: bucketMinPart, MaxParts: bucketMaxParts})
		if !ok {
			t.Skip("limits the product refuses to load")
		}
		plan, err := p.PlanMultipart(Upload{SizeBytes: size})
		if err != nil {
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("PlanMultipart(%d) = %v, want InvalidArgument", size, err)
			}
			return
		}
		n := int64(plan.TotalParts)
		if plan.PartSize < max(p.minPart, S3MinPartSize) || plan.PartSize > min(p.maxPart, S3MaxPartSize) {
			t.Fatalf("PlanMultipart(%d) = %+v, part size outside [%d, %d]", size, plan, p.minPart, p.maxPart)
		}
		if n < 1 || n > min(p.maxParts, S3MaxParts) {
			t.Fatalf("PlanMultipart(%d) = %+v, part count outside [1, %d]", size, plan, p.maxParts)
		}
		if (n-1)*plan.PartSize >= size || n*plan.PartSize < size {
			t.Fatalf("PlanMultipart(%d) = %+v does not cover the object exactly", size, plan)
		}
	})
}

// Any upload's content type and checksum against any valid allowlists:
// refused as InvalidArgument, or a type both lists accept and the algorithm
// the bucket requires.
func FuzzCheckSingle(f *testing.F) {
	f.Add("image/png", "SHA256", "", "image/png,text/plain", "")
	f.Add("Text/Plain; charset=utf-8", "sha256", "text/plain", "", "SHA256")
	f.Add("text", "MD5", "", "text/plain", "CRC32C")
	f.Fuzz(func(t *testing.T, contentType, algo, globalTypes, bucketTypes, required string) {
		l := global
		l.AllowedContentTypes = splitList(globalTypes)
		p, ok := policyFrom(l, BucketConstraints{AllowedContentTypes: splitList(bucketTypes), RequiredChecksumAlgorithm: required})
		if !ok {
			t.Skip("allowlists the product refuses to load")
		}
		err := p.CheckSingle(Upload{ContentType: contentType, ChecksumAlgorithm: algo})
		if err != nil {
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("CheckSingle(%q, %q) = %v, want InvalidArgument", contentType, algo, err)
			}
			return
		}
		if p.allowed != nil {
			mt, nerr := normalizeMediaType(contentType)
			if nerr != nil || !slices.Contains(p.allowed, mt) {
				t.Fatalf("CheckSingle admitted %q outside %v", contentType, p.allowed)
			}
		}
		if required != "" && !strings.EqualFold(algo, required) {
			t.Fatalf("CheckSingle admitted %q where the bucket requires %q", algo, required)
		}
	})
}
