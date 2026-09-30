package middleware

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/resolve"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
)

// QuotaReader is the read-only slice of the QuotaRepository the interceptor
// needs. Defined narrowly so tests can substitute fakes without pulling the
// full admindomain.
type QuotaReader interface {
	GetTenant(ctx context.Context, tenantID uuid.UUID) (admindomain.Quota, error)
	GetBucket(ctx context.Context, backendID, bucketName string) (admindomain.Quota, error)
}

// BucketBindingLookup resolves a Collection to the physical bucket it writes
// through, so a bucket-scoped quota can be found for an upload that names
// only its Collection. Satisfied by the object repository's LookupBucket.
//
// Optional: leave it nil and the interceptor checks tenant-scoped quotas
// only. That is the pre-bucket-enforcement behaviour, kept reachable
// because turning bucket caps from decorative into load-bearing changes
// what a deployment rejects.
type BucketBindingLookup interface {
	LookupBucket(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (backendID, bucket string, err error)
}

// QuotaSoftCheck is a Connect interceptor that rejects upload requests when
// the caller is at (or near) a configured cap.
//
// Four caps, checked in one pass, per scope:
//
//	max_total_bytes     — bytes stored          (headroom applies)
//	max_object_count    — objects stored
//	max_bytes_per_day   — bytes admitted today  (headroom applies)
//	max_objects_per_day — objects admitted today
//
// Two scopes: the caller's tenant, and — when Bindings is wired — the
// bucket the upload's Collection resolves to. Tenant is checked first so its
// message wins when both are exhausted; a tenant cap is the one an operator
// is more likely to have set deliberately.
//
// "Soft" means: the check reads the denormalised usage columns rather than
// measuring storage, so it lags. Two sources of lag, both bounded: the
// upload path's own increment is best-effort (it swallows write errors so a
// blip cannot undo a state transition), and worker.QuotaReconciler
// recomputes the columns from live objects only every
// worker.jobs.quota_reconcile.interval. To absorb concurrent presigns on
// top of that, byte thresholds are `cap * (1 + Headroom)`.
//
// Headroom deliberately applies to byte caps only. A byte cap is raced by
// concurrent presigns whose sizes are known up front, so admitting a little
// overshoot beats falsely rejecting; a count cap moves one at a time and
// needs no such slack. Count caps are therefore exact.
//
// There is no second, post-completion enforcement pass — an earlier version
// of this comment claimed one. Overshoot within the headroom window is
// accepted and corrected by the next reconcile, which is the trade the
// "soft" in the name is naming.
//
// Headroom is a fraction (e.g. 0.10 = 10%). Set <=0 to disable headroom.
//
// The interceptor only fires on procedures listed in `UploadProcedures`,
// keyed by the full path "/paladin.data.v1.ObjectService/UploadObject" etc. —
// all other RPCs pass through unchanged.
type QuotaSoftCheck struct {
	Reader           QuotaReader
	Bindings         BucketBindingLookup
	UploadProcedures map[string]struct{}
	Headroom         float64
}

// NewQuotaSoftCheck builds an interceptor with sensible defaults: gate on
// UploadObject + InitiateMultipartUpload + RegenerateUploadUrl with 10%
// headroom. Bucket-scoped enforcement stays off until WithBucketScope wires
// a binding lookup.
func NewQuotaSoftCheck(reader QuotaReader) *QuotaSoftCheck {
	return &QuotaSoftCheck{
		Reader: reader,
		UploadProcedures: map[string]struct{}{
			"/paladin.data.v1.ObjectService/UploadObject":                     {},
			"/paladin.data.v1.MultipartUploadService/InitiateMultipartUpload": {},
			"/paladin.data.v1.PresignService/RegenerateUploadUrl":             {},
		},
		Headroom: 0.10,
	}
}

// WithBucketScope enables bucket-scoped enforcement by giving the
// interceptor a way to resolve a Collection to its (backend, bucket).
// Without it, bucket quota rows are maintained and displayed but reject
// nothing.
func (q *QuotaSoftCheck) WithBucketScope(bindings BucketBindingLookup) *QuotaSoftCheck {
	q.Bindings = bindings
	return q
}

func (q *QuotaSoftCheck) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := q.CheckUpload(ctx, req.Spec().Procedure, req.Any()); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (q *QuotaSoftCheck) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (q *QuotaSoftCheck) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// regenerateUploadURLProc re-binds a presigned PUT to a Collection's
// EXISTING pending row. It creates nothing, so it must not consume a slot
// against either object-count cap — and it carries no size, so the byte
// caps see 0 and pass. Singled out by name because the request message has
// no field distinguishing it from a create.
const regenerateUploadURLProc = "/paladin.data.v1.PresignService/RegenerateUploadUrl"

// CheckUpload gates on procedure, then runs every configured cap for every
// scope in play. WrapUnary is a thin shell over it.
//
// Exported because it takes the procedure + message rather than a
// connect.AnyRequest, whose unexported marker method makes it
// unimplementable outside the connect package. That would leave
// enforcement testable only through a full server round-trip; this way the
// integration suite can drive the real decision path against real
// repositories, and the unit tests exercise the same function the
// interceptor calls rather than a copy of it.
func (q *QuotaSoftCheck) CheckUpload(ctx context.Context, procedure string, msg any) error {
	if _, ok := q.UploadProcedures[procedure]; !ok {
		return nil
	}
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil || p.TenantID == uuid.Nil {
		// No tenant = nothing to enforce against — let downstream auth reject.
		return nil
	}

	sizeHint := extractSizeHint(msg)
	// A regenerate only re-binds an existing PENDING row, so it adds no
	// object. Everything else on the gated list creates one.
	newObject := procedure != regenerateUploadURLProc

	tenantID := p.TenantID.String()

	// Tenant-quota row absent = unlimited. Other read errors degrade open
	// (presigns are not blast-radius events).
	//
	// Only an actual comparison is counted. A missing quota row made no
	// decision, and counting it as "allowed" would bury the allow/reject ratio
	// under traffic the quota system never looked at — the ratio being the
	// whole reason this metric exists.
	if quota, err := q.Reader.GetTenant(ctx, p.TenantID); err == nil {
		if err := checkCaps(quota, "tenant", sizeHint, newObject, q.Headroom); err != nil {
			metrics.RecordQuotaDecision(ctx, tenantID, "tenant", "rejected")
			return err
		}
		metrics.RecordQuotaDecision(ctx, tenantID, "tenant", "allowed")
	}

	if quota, ok := q.bucketQuota(ctx, msg); ok {
		if err := checkCaps(quota, "bucket", sizeHint, newObject, q.Headroom); err != nil {
			metrics.RecordQuotaDecision(ctx, tenantID, "bucket", "rejected")
			return err
		}
		metrics.RecordQuotaDecision(ctx, tenantID, "bucket", "allowed")
	}
	return nil
}

// bucketQuota resolves the upload's target bucket and reads its quota row.
// Every failure along the way — no binding lookup wired, an unparseable
// name, an unbound Collection, no quota row for the bucket — returns
// ok=false and lets the request through. The handler behind this
// interceptor re-resolves the same binding and produces the accurate error
// for a genuinely broken route; turning a routing problem into
// ResourceExhausted here would only mislead the caller.
func (q *QuotaSoftCheck) bucketQuota(ctx context.Context, msg any) (admindomain.Quota, bool) {
	if q.Bindings == nil {
		return admindomain.Quota{}, false
	}
	name := extractCollectionName(msg)
	if name == "" {
		return admindomain.Quota{}, false
	}
	// ParseCollectionName, not ResolveCollectionName: the connectshim records
	// one shape observation per request already, and recording a second
	// here would double-count every upload in the shape distribution.
	ref, err := resolve.ParseCollectionName(ctx, name)
	if err != nil {
		return admindomain.Quota{}, false
	}
	backendID, bucketName := ref.BackendID, ref.BucketName
	if backendID == "" || bucketName == "" {
		// Non-canonical name shape: the (backend, bucket) has to come from
		// the Collection's binding. write=true matches what this request is
		// about to do, so a disabled or draining backend surfaces as an
		// error here — which we swallow, leaving the handler to report it.
		backendID, bucketName, err = q.Bindings.LookupBucket(ctx, ref.TenantID, ref.Collection, true)
		if err != nil {
			return admindomain.Quota{}, false
		}
	}
	quota, err := q.Reader.GetBucket(ctx, backendID, bucketName)
	if err != nil {
		return admindomain.Quota{}, false
	}
	return quota, true
}

// checkCaps runs the four comparisons for one quota row. `scope` names the
// row in the rejection message so a caller can tell a tenant cap from a
// bucket cap without guessing which one stopped them.
func checkCaps(quota admindomain.Quota, scope string, sizeHint int64, newObject bool, headroom float64) error {
	if quota.MaxTotalBytes > 0 {
		threshold := float64(quota.MaxTotalBytes) * (1 + maxF(headroom, 0))
		if float64(quota.UsageTotalBytes+sizeHint) > threshold {
			return exhausted("%s quota exceeded: %d + %d > %d (max_total_bytes)",
				scope, quota.UsageTotalBytes, sizeHint, quota.MaxTotalBytes)
		}
	}
	if quota.MaxObjectCount > 0 && newObject && quota.UsageObjectCount+1 > quota.MaxObjectCount {
		return exhausted("%s quota exceeded: %d + 1 > %d (max_object_count)",
			scope, quota.UsageObjectCount, quota.MaxObjectCount)
	}
	// Daily caps meter admission, not storage: they count what was accepted
	// today and are zeroed at the UTC day boundary by the quota reconciler.
	// An upload that is later deleted still spent its budget — that is what
	// stops a caller cycling upload/delete to bypass the cap.
	if quota.MaxBytesPerDay > 0 {
		threshold := float64(quota.MaxBytesPerDay) * (1 + maxF(headroom, 0))
		if float64(quota.UsageBytesToday+sizeHint) > threshold {
			return exhausted("%s daily quota exceeded: %d + %d > %d (max_bytes_per_day)",
				scope, quota.UsageBytesToday, sizeHint, quota.MaxBytesPerDay)
		}
	}
	if quota.MaxObjectsPerDay > 0 && newObject && quota.UsageObjectsToday+1 > quota.MaxObjectsPerDay {
		return exhausted("%s daily quota exceeded: %d + 1 > %d (max_objects_per_day)",
			scope, quota.UsageObjectsToday, quota.MaxObjectsPerDay)
	}
	return nil
}

func exhausted(format string, args ...any) error {
	return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf(format, args...))
}

// extractSizeHint reads a `size_hint_bytes` (UploadObject) or `size_bytes`
// (InitiateMultipartUpload) field via interface assertions. RegenerateUploadUrl
// has no size context — defaults to 0, which is a deliberate pass-through
// (regenerated PUTs only re-bind an existing PENDING row).
func extractSizeHint(msg any) int64 {
	if msg == nil {
		return 0
	}
	type withSizeHint interface{ GetSizeHintBytes() int64 }
	type withSize interface{ GetSizeBytes() int64 }
	if m, ok := msg.(withSizeHint); ok {
		return m.GetSizeHintBytes()
	}
	if m, ok := msg.(withSize); ok {
		return m.GetSizeBytes()
	}
	return 0
}

// extractCollectionName pulls the Collection resource name out of a gated
// request. UploadObject and InitiateMultipartUpload carry it as `parent`;
// RegenerateUploadUrl carries an OBJECT name in `name`, so the trailing
// "/objects/{id}" is trimmed back to the Collection it belongs to.
func extractCollectionName(msg any) string {
	if msg == nil {
		return ""
	}
	type withParent interface{ GetParent() string }
	type withName interface{ GetName() string }
	if m, ok := msg.(withParent); ok {
		if p := m.GetParent(); p != "" {
			return p
		}
	}
	if m, ok := msg.(withName); ok {
		n := m.GetName()
		if i := strings.Index(n, "/objects/"); i >= 0 {
			return n[:i]
		}
		return n
	}
	return ""
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
