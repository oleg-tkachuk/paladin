package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/auth"
)

type fakeQuotaReader struct {
	q   admindomain.Quota
	err error
}

func (f *fakeQuotaReader) GetTenant(_ context.Context, _ uuid.UUID) (admindomain.Quota, error) {
	return f.q, f.err
}

func (f *fakeQuotaReader) GetBucket(_ context.Context, _, _ string) (admindomain.Quota, error) {
	return admindomain.Quota{}, errors.New("no bucket quota")
}

type fakeUploadObjectRequest struct {
	sizeHint int64
}

func (r *fakeUploadObjectRequest) GetSizeHintBytes() int64 { return r.sizeHint }

func TestQuotaSoftCheckPassThroughOnNonUploadProc(t *testing.T) {
	q := NewQuotaSoftCheck(&fakeQuotaReader{q: admindomain.Quota{MaxTotalBytes: 100}})
	if err := q.CheckUpload(context.Background(), "/some.other/Method", nil); err != nil {
		t.Fatalf("expected pass-through, got %v", err)
	}
}

func TestQuotaSoftCheckRejectsOverCap(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: tenantID})
	q := &QuotaSoftCheck{
		Reader: &fakeQuotaReader{q: admindomain.Quota{
			MaxTotalBytes:   1000,
			UsageTotalBytes: 950,
		}},
		UploadProcedures: map[string]struct{}{"/test/Upload": {}},
		Headroom:         0.05, // 5% → threshold 1050
	}
	// 950 + 200 = 1150 > 1050 → reject
	err := q.CheckUpload(ctx, "/test/Upload", &fakeUploadObjectRequest{sizeHint: 200})
	if err == nil {
		t.Fatal("expected reject, got nil")
	}
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Errorf("got code %v, want ResourceExhausted", connect.CodeOf(err))
	}
}

func TestQuotaSoftCheckAdmitsWithinHeadroom(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: tenantID})
	q := &QuotaSoftCheck{
		Reader: &fakeQuotaReader{q: admindomain.Quota{
			MaxTotalBytes:   1000,
			UsageTotalBytes: 950,
		}},
		UploadProcedures: map[string]struct{}{"/test/Upload": {}},
		Headroom:         0.10, // 10% → threshold 1100
	}
	// 950 + 100 = 1050 ≤ 1100 → admit
	if err := q.CheckUpload(ctx, "/test/Upload", &fakeUploadObjectRequest{sizeHint: 100}); err != nil {
		t.Errorf("expected admit, got %v", err)
	}
}

func TestQuotaSoftCheckObjectCountCap(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: tenantID})
	q := &QuotaSoftCheck{
		Reader: &fakeQuotaReader{q: admindomain.Quota{
			MaxObjectCount:   5,
			UsageObjectCount: 5,
		}},
		UploadProcedures: map[string]struct{}{"/test/Upload": {}},
	}
	err := q.CheckUpload(ctx, "/test/Upload", &fakeUploadObjectRequest{sizeHint: 1})
	if err == nil {
		t.Fatal("expected reject, got nil")
	}
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Errorf("got code %v, want ResourceExhausted", connect.CodeOf(err))
	}
}

func TestQuotaSoftCheckMissingQuotaIsUnlimited(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: tenantID})
	q := &QuotaSoftCheck{
		Reader:           &fakeQuotaReader{err: errors.New("not found")},
		UploadProcedures: map[string]struct{}{"/test/Upload": {}},
	}
	if err := q.CheckUpload(ctx, "/test/Upload", &fakeUploadObjectRequest{sizeHint: 1 << 30}); err != nil {
		t.Errorf("missing quota should pass, got %v", err)
	}
}

// ─── Per-day caps ───────────────────────────────────────────────────────────
//
// max_bytes_per_day / max_objects_per_day were stored, incremented and
// (since the reconciler landed) rolled at midnight, but nothing ever
// compared against them — an operator who set a daily budget got a number
// that moved and rejected nothing. These cover the enforcement.

const uploadProc = "/paladin.data.v1.ObjectService/UploadObject"

func tenantCtx(t *testing.T) context.Context {
	t.Helper()
	return auth.WithPrincipal(context.Background(),
		&auth.Principal{TenantID: uuid.Must(uuid.NewV7())})
}

// gated builds an interceptor whose only gated procedure is UploadObject,
// with headroom off so thresholds are exact and the assertions read as the
// arithmetic they are.
func gated(reader QuotaReader) *QuotaSoftCheck {
	return &QuotaSoftCheck{
		Reader:           reader,
		UploadProcedures: map[string]struct{}{uploadProc: {}},
	}
}

func TestQuotaSoftCheck_DailyByteCapRejects(t *testing.T) {
	ctx := tenantCtx(t)
	q := gated(&fakeQuotaReader{q: admindomain.Quota{
		MaxBytesPerDay:  1000,
		UsageBytesToday: 900,
	}})

	err := q.CheckUpload(ctx, uploadProc, &fakeUploadObjectRequest{sizeHint: 200})
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted", connect.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "max_bytes_per_day") {
		t.Errorf("message %q should name the cap that rejected", err)
	}
}

func TestQuotaSoftCheck_DailyByteCapAdmitsUnderBudget(t *testing.T) {
	ctx := tenantCtx(t)
	q := gated(&fakeQuotaReader{q: admindomain.Quota{
		MaxBytesPerDay:  1000,
		UsageBytesToday: 900,
	}})
	// 900 + 100 = 1000, exactly at the cap — the comparison is strict `>`,
	// so the request that lands the budget precisely on its limit is the
	// last one admitted.
	if err := q.CheckUpload(ctx, uploadProc, &fakeUploadObjectRequest{sizeHint: 100}); err != nil {
		t.Errorf("expected admit at exactly the cap, got %v", err)
	}
}

// Headroom exists to absorb concurrent presigns racing a lagging counter.
// It applies to byte caps in both scopes and both time windows.
func TestQuotaSoftCheck_DailyByteCapHonoursHeadroom(t *testing.T) {
	ctx := tenantCtx(t)
	q := gated(&fakeQuotaReader{q: admindomain.Quota{
		MaxBytesPerDay:  1000,
		UsageBytesToday: 950,
	}})
	q.Headroom = 0.10 // threshold 1100

	if err := q.CheckUpload(ctx, uploadProc, &fakeUploadObjectRequest{sizeHint: 100}); err != nil {
		t.Errorf("950+100 = 1050 is inside the 1100 headroom; got %v", err)
	}
	if err := q.CheckUpload(ctx, uploadProc, &fakeUploadObjectRequest{sizeHint: 200}); err == nil {
		t.Error("950+200 = 1150 exceeds the 1100 headroom; want reject")
	}
}

func TestQuotaSoftCheck_DailyObjectCapRejects(t *testing.T) {
	ctx := tenantCtx(t)
	q := gated(&fakeQuotaReader{q: admindomain.Quota{
		MaxObjectsPerDay:  5,
		UsageObjectsToday: 5,
	}})

	err := q.CheckUpload(ctx, uploadProc, &fakeUploadObjectRequest{sizeHint: 1})
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted", connect.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "max_objects_per_day") {
		t.Errorf("message %q should name the cap that rejected", err)
	}
}

// Count caps are exact — headroom is for byte caps raced by concurrent
// presigns, and silently letting a 5-object budget admit a 6th would be a
// surprising reading of "5".
func TestQuotaSoftCheck_DailyObjectCapIgnoresHeadroom(t *testing.T) {
	ctx := tenantCtx(t)
	q := gated(&fakeQuotaReader{q: admindomain.Quota{
		MaxObjectsPerDay:  5,
		UsageObjectsToday: 5,
	}})
	q.Headroom = 0.50

	if err := q.CheckUpload(ctx, uploadProc, &fakeUploadObjectRequest{sizeHint: 1}); err == nil {
		t.Error("count caps must not get headroom slack")
	}
}

// A quota row with only lifetime caps set must not be affected by the new
// daily comparisons — the zero value of a daily cap means "no cap", and a
// tenant with a big lifetime budget and no daily budget must keep working.
func TestQuotaSoftCheck_UnsetDailyCapsAreUnlimited(t *testing.T) {
	ctx := tenantCtx(t)
	q := gated(&fakeQuotaReader{q: admindomain.Quota{
		MaxTotalBytes:     1 << 40,
		UsageBytesToday:   1 << 30,
		UsageObjectsToday: 1_000_000,
	}})

	if err := q.CheckUpload(ctx, uploadProc, &fakeUploadObjectRequest{sizeHint: 1 << 20}); err != nil {
		t.Errorf("unset daily caps must not reject, got %v", err)
	}
}

// ─── Regenerate-URL is not a create ─────────────────────────────────────────

type fakeRegenerateRequest struct{ name string }

func (r *fakeRegenerateRequest) GetName() string { return r.name }

// RegenerateUploadUrl re-binds a presigned PUT to an EXISTING pending row.
// Counting it as a new object would let a client burn its object budget by
// retrying a presign, and would double-count against the daily budget.
func TestQuotaSoftCheck_RegenerateDoesNotConsumeObjectSlots(t *testing.T) {
	ctx := tenantCtx(t)
	q := &QuotaSoftCheck{
		Reader: &fakeQuotaReader{q: admindomain.Quota{
			MaxObjectCount:    5,
			UsageObjectCount:  5,
			MaxObjectsPerDay:  5,
			UsageObjectsToday: 5,
		}},
		UploadProcedures: map[string]struct{}{regenerateUploadURLProc: {}},
	}

	err := q.CheckUpload(ctx, regenerateUploadURLProc,
		&fakeRegenerateRequest{name: "tenants/" + uuid.NewString() + "/objectKeys/ok/objects/abc"})
	if err != nil {
		t.Errorf("regenerate must not consume an object slot, got %v", err)
	}
}

// ...but a regenerate on a tenant that is over its BYTE cap still rejects,
// because those bytes are already stored. Size hint is 0 for this
// procedure, so the check is against current usage alone.
func TestQuotaSoftCheck_RegenerateStillHonoursByteCap(t *testing.T) {
	ctx := tenantCtx(t)
	q := &QuotaSoftCheck{
		Reader: &fakeQuotaReader{q: admindomain.Quota{
			MaxTotalBytes:   1000,
			UsageTotalBytes: 2000,
		}},
		UploadProcedures: map[string]struct{}{regenerateUploadURLProc: {}},
	}

	if err := q.CheckUpload(ctx, regenerateUploadURLProc, &fakeRegenerateRequest{name: "ok"}); err == nil {
		t.Error("want reject: usage is already double the byte cap")
	}
}

// ─── Bucket-scoped enforcement ──────────────────────────────────────────────
//
// Bucket quota rows existed, were settable, and (once the reconciler
// landed) carried correct usage — but QuotaSoftCheck only read GetTenant,
// so a bucket cap rejected nothing. These cover the second scope.

// scopedReader answers tenant and bucket scopes independently so a test can
// assert which one rejected, and records what bucket it was asked about.
type scopedReader struct {
	tenant     admindomain.Quota
	tenantErr  error
	bucket     admindomain.Quota
	bucketErr  error
	askedBEnd  string
	askedBcket string
}

func (s *scopedReader) GetTenant(context.Context, uuid.UUID) (admindomain.Quota, error) {
	return s.tenant, s.tenantErr
}

func (s *scopedReader) GetBucket(_ context.Context, backendID, bucketName string) (admindomain.Quota, error) {
	s.askedBEnd, s.askedBcket = backendID, bucketName
	return s.bucket, s.bucketErr
}

type fakeBindings struct {
	backendID string
	bucket    string
	err       error
	calls     int
	askedKey  string
}

func (f *fakeBindings) LookupBucket(_ context.Context, _ uuid.UUID, objectKey string, _ bool) (string, string, error) {
	f.calls++
	f.askedKey = objectKey
	return f.backendID, f.bucket, f.err
}

type fakeUploadWithParent struct {
	parent   string
	sizeHint int64
}

func (r *fakeUploadWithParent) GetParent() string       { return r.parent }
func (r *fakeUploadWithParent) GetSizeHintBytes() int64 { return r.sizeHint }

func TestQuotaSoftCheck_BucketCapRejects(t *testing.T) {
	ctx := tenantCtx(t)
	reader := &scopedReader{
		tenantErr: errors.New("no tenant quota"), // tenant unlimited
		bucket:    admindomain.Quota{MaxTotalBytes: 1000, UsageTotalBytes: 950},
	}
	bindings := &fakeBindings{backendID: "primary", bucket: "bkt-1"}
	q := gated(reader)
	q.WithBucketScope(bindings)

	err := q.CheckUpload(ctx, uploadProc,
		&fakeUploadWithParent{parent: "tenants/" + uuid.NewString() + "/objectKeys/ok-1", sizeHint: 200})
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted", connect.CodeOf(err))
	}
	// The message must say which scope stopped them — the whole point of
	// having two.
	if !strings.HasPrefix(err.Error(), "resource_exhausted: bucket quota exceeded") {
		t.Errorf("message %q should attribute the rejection to the bucket scope", err)
	}
	if bindings.askedKey != "ok-1" {
		t.Errorf("resolved objectKey = %q, want ok-1", bindings.askedKey)
	}
	if reader.askedBEnd != "primary" || reader.askedBcket != "bkt-1" {
		t.Errorf("read quota for (%q,%q), want (primary,bkt-1)",
			reader.askedBEnd, reader.askedBcket)
	}
}

// Tenant scope is evaluated first, so when both are exhausted the tenant
// message wins — that is the cap an operator more likely set deliberately.
func TestQuotaSoftCheck_TenantScopeReportedFirst(t *testing.T) {
	ctx := tenantCtx(t)
	q := gated(&scopedReader{
		tenant: admindomain.Quota{MaxTotalBytes: 10, UsageTotalBytes: 100},
		bucket: admindomain.Quota{MaxTotalBytes: 10, UsageTotalBytes: 100},
	})
	q.WithBucketScope(&fakeBindings{backendID: "primary", bucket: "bkt-1"})

	err := q.CheckUpload(ctx, uploadProc, &fakeUploadWithParent{parent: "ok-1", sizeHint: 1})
	if err == nil || !strings.Contains(err.Error(), "tenant quota exceeded") {
		t.Errorf("err = %v, want the tenant-scope message", err)
	}
}

// A canonical (shape A) name already carries the binding, so the interceptor
// must not spend a database round-trip re-deriving it.
func TestQuotaSoftCheck_CanonicalNameSkipsBindingLookup(t *testing.T) {
	ctx := tenantCtx(t)
	reader := &scopedReader{
		tenantErr: errors.New("none"),
		bucket:    admindomain.Quota{MaxObjectCount: 1, UsageObjectCount: 5},
	}
	bindings := &fakeBindings{}
	q := gated(reader)
	q.WithBucketScope(bindings)

	name := "storageBackends/be-2/buckets/bkt-2/tenants/" + uuid.NewString() + "/objectKeys/ok-9"
	if err := q.CheckUpload(ctx, uploadProc, &fakeUploadWithParent{parent: name, sizeHint: 1}); err == nil {
		t.Fatal("want reject from the bucket object-count cap")
	}
	if bindings.calls != 0 {
		t.Errorf("binding lookup called %d times for a canonical name, want 0", bindings.calls)
	}
	if reader.askedBEnd != "be-2" || reader.askedBcket != "bkt-2" {
		t.Errorf("read quota for (%q,%q), want (be-2,bkt-2)", reader.askedBEnd, reader.askedBcket)
	}
}

// Every way the bucket lookup can fail must fail OPEN. A routing problem is
// the handler's to report accurately; surfacing it here as
// ResourceExhausted would tell the caller to delete data that isn't the
// problem.
func TestQuotaSoftCheck_BucketResolutionFailuresFailOpen(t *testing.T) {
	overCap := admindomain.Quota{MaxTotalBytes: 1, UsageTotalBytes: 999}

	cases := []struct {
		name     string
		bindings BucketBindingLookup
		reader   *scopedReader
		msg      any
	}{
		{
			name:     "no bindings wired",
			bindings: nil,
			reader:   &scopedReader{tenantErr: errors.New("none"), bucket: overCap},
			msg:      &fakeUploadWithParent{parent: "ok-1", sizeHint: 1},
		},
		{
			name:     "request carries no object key name",
			bindings: &fakeBindings{backendID: "primary", bucket: "bkt-1"},
			reader:   &scopedReader{tenantErr: errors.New("none"), bucket: overCap},
			msg:      &fakeUploadObjectRequest{sizeHint: 1}, // no parent / name
		},
		{
			name:     "object key is unbound",
			bindings: &fakeBindings{err: errors.New("objectKey not found")},
			reader:   &scopedReader{tenantErr: errors.New("none"), bucket: overCap},
			msg:      &fakeUploadWithParent{parent: "ok-1", sizeHint: 1},
		},
		{
			name:     "bucket has no quota row",
			bindings: &fakeBindings{backendID: "primary", bucket: "bkt-1"},
			reader:   &scopedReader{tenantErr: errors.New("none"), bucketErr: errors.New("no row")},
			msg:      &fakeUploadWithParent{parent: "ok-1", sizeHint: 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tenantCtx(t)
			q := gated(tc.reader)
			q.Bindings = tc.bindings
			if err := q.CheckUpload(ctx, uploadProc, tc.msg); err != nil {
				t.Errorf("want pass-through, got %v", err)
			}
		})
	}
}

// The daily caps apply to the bucket scope too — the check is the same
// function, and this pins that so a future refactor can't quietly make
// daily budgets tenant-only.
func TestQuotaSoftCheck_BucketDailyCapRejects(t *testing.T) {
	ctx := tenantCtx(t)
	q := gated(&scopedReader{
		tenantErr: errors.New("none"),
		bucket:    admindomain.Quota{MaxObjectsPerDay: 2, UsageObjectsToday: 2},
	})
	q.WithBucketScope(&fakeBindings{backendID: "primary", bucket: "bkt-1"})

	err := q.CheckUpload(ctx, uploadProc, &fakeUploadWithParent{parent: "ok-1", sizeHint: 1})
	if err == nil || !strings.Contains(err.Error(), "bucket daily quota exceeded") {
		t.Errorf("err = %v, want a bucket-scope daily rejection", err)
	}
}
