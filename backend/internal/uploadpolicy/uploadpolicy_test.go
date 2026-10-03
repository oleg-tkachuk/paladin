package uploadpolicy

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
)

const (
	mib = int64(1 << 20)
	gib = int64(1 << 30)
)

// global is limits.* as the CUE defaults produce it.
var global = Limits{
	MaxObjectSize:    100_000_000,
	MaxMultipartSize: 1_000_000_000_000,
	MinPartSize:      S3MinPartSize,
	MaxPartSize:      S3MaxPartSize,
	MaxParts:         S3MaxParts,
}

func wantInvalid(t *testing.T, err error) {
	t.Helper()
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestCheckSingle(t *testing.T) {
	cases := []struct {
		name   string
		limits Limits
		bucket BucketConstraints
		upload Upload
		ok     bool
	}{
		{"within every limit", global, BucketConstraints{}, Upload{SizeBytes: 1 << 20, ContentType: "image/png", ChecksumAlgorithm: "SHA256"}, true},
		{"an empty object", global, BucketConstraints{}, Upload{SizeBytes: 0, ContentType: "text/plain"}, true},
		{"exactly max_object_size", global, BucketConstraints{}, Upload{SizeBytes: global.MaxObjectSize}, true},
		{"one byte over max_object_size", global, BucketConstraints{}, Upload{SizeBytes: global.MaxObjectSize + 1}, false},
		{"a negative size", global, BucketConstraints{}, Upload{SizeBytes: -1}, false},
		{"the bucket narrows the cap", global, BucketConstraints{MaxObjectSizeBytes: 1000}, Upload{SizeBytes: 1001}, false},
		// A bucket owner cannot lift the operator's ceiling.
		{"the bucket cannot widen the cap", global, BucketConstraints{MaxObjectSizeBytes: 10 * global.MaxObjectSize}, Upload{SizeBytes: global.MaxObjectSize + 1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := For(tc.limits, tc.bucket).CheckSingle(tc.upload)
			if tc.ok {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
				return
			}
			wantInvalid(t, err)
		})
	}
}

func TestContentTypeAllowlists(t *testing.T) {
	withGlobal := global
	withGlobal.AllowedContentTypes = []string{"image/png", "Text/Plain", "application/pdf"}
	cases := []struct {
		name   string
		limits Limits
		bucket []string
		ct     string
		ok     bool
	}{
		{"no list accepts anything", global, nil, "application/x-anything", true},
		{"global list accepts a listed type", withGlobal, nil, "image/png", true},
		{"parameters and case do not matter", withGlobal, nil, "TEXT/plain; charset=utf-8", true},
		{"global list refuses an unlisted type", withGlobal, nil, "image/jpeg", false},
		{"bucket list alone applies", global, []string{"image/jpeg"}, "image/jpeg", true},
		{"bucket list alone refuses", global, []string{"image/jpeg"}, "image/png", false},
		{"both lists: the intersection is accepted", withGlobal, []string{"image/png", "image/jpeg"}, "image/png", true},
		{"both lists: a bucket-only type is refused", withGlobal, []string{"image/png", "image/jpeg"}, "image/jpeg", false},
		{"disjoint lists accept nothing", withGlobal, []string{"video/mp4"}, "video/mp4", false},
		{"an unparseable type is refused under a list", withGlobal, nil, "not a type", false},
		{"a bare token is refused under a list", withGlobal, nil, "png", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := For(tc.limits, BucketConstraints{AllowedContentTypes: tc.bucket}).CheckSingle(Upload{ContentType: tc.ct})
			if tc.ok {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
				return
			}
			wantInvalid(t, err)
		})
	}
}

func TestContentTypeRefusalIsNamed(t *testing.T) {
	err := For(global, BucketConstraints{AllowedContentTypes: []string{"image/png"}}).CheckSingle(Upload{ContentType: "image/gif"})
	if !errors.Is(err, ErrContentTypeNotAllowed) {
		t.Fatalf("err = %v, want ErrContentTypeNotAllowed", err)
	}
}

func TestRequiredChecksumAlgorithm(t *testing.T) {
	p := For(global, BucketConstraints{RequiredChecksumAlgorithm: "crc32c"})
	if err := p.CheckSingle(Upload{ChecksumAlgorithm: "CRC32C"}); err != nil {
		t.Fatalf("the required algorithm was refused: %v", err)
	}
	wantInvalid(t, p.CheckSingle(Upload{ChecksumAlgorithm: "SHA256"}))
	wantInvalid(t, p.CheckSingle(Upload{}))
	if err := For(global, BucketConstraints{}).CheckSingle(Upload{ChecksumAlgorithm: "MD5"}); err != nil {
		t.Fatalf("no requirement refused MD5: %v", err)
	}
}

func TestCheckCopy(t *testing.T) {
	p := For(global, BucketConstraints{AllowedContentTypes: []string{"image/png"}})
	// A copy is not a single request: it may exceed max_object_size, up to
	// the largest object the bucket accepts.
	if err := p.CheckCopy(Upload{SizeBytes: 2 * global.MaxObjectSize, ContentType: "image/png"}); err != nil {
		t.Fatalf("copy above the single-request cap refused: %v", err)
	}
	wantInvalid(t, p.CheckCopy(Upload{SizeBytes: global.MaxMultipartSize + 1, ContentType: "image/png"}))
	wantInvalid(t, p.CheckCopy(Upload{SizeBytes: 1, ContentType: "image/jpeg"}))
	wantInvalid(t, For(global, BucketConstraints{MaxObjectSizeBytes: 10}).CheckCopy(Upload{SizeBytes: 11}))
}

// The cases planParts pinned, now under S3's own limits, plus the configured
// ones it ignored.
func TestPlanMultipart(t *testing.T) {
	cases := []struct {
		name      string
		limits    Limits
		bucket    BucketConstraints
		size      int64
		wantSize  int64
		wantParts int32
	}{
		{"single byte still gets one part", global, BucketConstraints{}, 1, 5 * mib, 1},
		{"exactly one minimum part", global, BucketConstraints{}, 5 * mib, 5 * mib, 1},
		{"one byte over splits in two", global, BucketConstraints{}, 5*mib + 1, 5 * mib, 2},
		{"12 MiB is three parts", global, BucketConstraints{}, 12 * mib, 5 * mib, 3},
		{"the part-count cap keeps the minimum size", global, BucketConstraints{}, S3MaxParts * S3MinPartSize, 5 * mib, 10000},
		{"one byte past it doubles the size", global, BucketConstraints{}, S3MaxParts*S3MinPartSize + 1, 10 * mib, 5001},
		{"the bucket's minimum part", global, BucketConstraints{MinPartSizeBytes: 64 * mib}, 100 * mib, 64 * mib, 2},
		{"the bucket's max_parts doubles the size", global, BucketConstraints{MaxParts: 4}, 100 * mib, 40 * mib, 3},
		{"doubling past max_part_size settles on max_part_size", global, BucketConstraints{MaxPartSizeBytes: 48 * mib, MaxParts: 3}, 140 * mib, 48 * mib, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := For(tc.limits, tc.bucket).PlanMultipart(Upload{SizeBytes: tc.size})
			if err != nil {
				t.Fatal(err)
			}
			if plan.PartSize != tc.wantSize || plan.TotalParts != tc.wantParts {
				t.Fatalf("plan = %d × %d, want %d × %d", plan.PartSize, plan.TotalParts, tc.wantSize, tc.wantParts)
			}
			if int64(plan.TotalParts)*plan.PartSize < tc.size {
				t.Fatalf("plan %d × %d does not cover %d bytes", plan.PartSize, plan.TotalParts, tc.size)
			}
		})
	}
}

func TestPlanMultipartRefusals(t *testing.T) {
	cases := []struct {
		name   string
		limits Limits
		bucket BucketConstraints
		size   int64
	}{
		{"no size", global, BucketConstraints{}, 0},
		{"above max_multipart_size", global, BucketConstraints{}, global.MaxMultipartSize + 1},
		{"above the bucket's object size", global, BucketConstraints{MaxObjectSizeBytes: 50 * mib}, 50*mib + 1},
		{"more parts than allowed at the largest part", global, BucketConstraints{MaxPartSizeBytes: 5 * mib, MaxParts: 10}, 50*mib + 1},
		{"inconsistent bucket part bounds", global, BucketConstraints{MinPartSizeBytes: 64 * mib, MaxPartSizeBytes: 32 * mib}, mib},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := For(tc.limits, tc.bucket).PlanMultipart(Upload{SizeBytes: tc.size})
			wantInvalid(t, err)
		})
	}
}

func TestTTLCeilings(t *testing.T) {
	p := For(global, BucketConstraints{MaxPresignPutTTL: time.Minute, MaxPresignGetTTL: 2 * time.Minute})
	if p.PutTTLCeiling() != time.Minute || p.GetTTLCeiling() != 2*time.Minute {
		t.Fatalf("ceilings = %v/%v", p.PutTTLCeiling(), p.GetTTLCeiling())
	}
	if q := For(global, BucketConstraints{}); q.PutTTLCeiling() != 0 || q.GetTTLCeiling() != 0 {
		t.Fatal("a bucket with no ceiling reported one")
	}
}

func TestLimitsValidate(t *testing.T) {
	if err := global.Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Limits)
	}{
		{"zero max_object_size", func(l *Limits) { l.MaxObjectSize = 0 }},
		{"zero max_multipart_size", func(l *Limits) { l.MaxMultipartSize = 0 }},
		{"min_part_size of 5MB, below S3's 5 MiB", func(l *Limits) { l.MinPartSize = 5_000_000 }},
		{"max_part_size above S3's", func(l *Limits) { l.MaxPartSize = 6 * gib }},
		{"min above max", func(l *Limits) { l.MinPartSize = 64 * mib; l.MaxPartSize = 32 * mib }},
		{"max_parts above S3's", func(l *Limits) { l.MaxParts = S3MaxParts + 1 }},
		{"zero max_parts", func(l *Limits) { l.MaxParts = 0 }},
		{"an unparseable allowed type", func(l *Limits) { l.AllowedContentTypes = []string{"not a type"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := global
			tc.mutate(&l)
			if err := l.Validate(); err == nil {
				t.Fatal("Validate accepted an unusable limit")
			}
		})
	}
}

func TestBucketConstraintsValidate(t *testing.T) {
	ok := []BucketConstraints{
		{},
		{MaxObjectSizeBytes: 1, MinPartSizeBytes: S3MinPartSize, MaxPartSizeBytes: S3MaxPartSize, MaxParts: 10000,
			AllowedContentTypes: []string{"image/png"}, MaxPresignPutTTL: time.Minute, MaxPresignGetTTL: SigV4MaxPresignExpiry,
			RequiredChecksumAlgorithm: "sha256"},
	}
	for i, c := range ok {
		if err := c.Validate(); err != nil {
			t.Errorf("valid[%d]: %v", i, err)
		}
	}
	bad := map[string]BucketConstraints{
		"negative object size":     {MaxObjectSizeBytes: -1},
		"min part below S3":        {MinPartSizeBytes: S3MinPartSize - 1},
		"max part above S3":        {MaxPartSizeBytes: S3MaxPartSize + 1},
		"min part above max part":  {MinPartSizeBytes: 64 * mib, MaxPartSizeBytes: 32 * mib},
		"max parts above S3":       {MaxParts: 10001},
		"negative max parts":       {MaxParts: -1},
		"unparseable content type": {AllowedContentTypes: []string{"nope"}},
		"negative ttl":             {MaxPresignGetTTL: -time.Second},
		"ttl beyond SigV4":         {MaxPresignPutTTL: SigV4MaxPresignExpiry + time.Second},
		"unknown algorithm":        {RequiredChecksumAlgorithm: "SHA1"},
	}
	for name, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
