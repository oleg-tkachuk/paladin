package middleware

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/auth"
)

// QuotaReader is the read-only slice of the QuotaRepository the interceptor
// needs. Defined narrowly so tests can substitute fakes without pulling the
// full admindomain.
type QuotaReader interface {
	GetTenant(ctx context.Context, tenantID uuid.UUID) (admindomain.Quota, error)
}

// QuotaSoftCheck is a Connect interceptor that rejects upload requests when
// the caller's tenant is at (or near) its hard byte / object cap.
//
// "Soft" means: the check uses cached usage from the accounting worker — it
// can lag real S3 state by a few seconds. To absorb concurrent presigns,
// the threshold is `MaxTotalBytes * (1 + Headroom)`. A worker-driven
// post-completion check still hard-rejects overshoot on the back end.
//
// Headroom is a fraction (e.g. 0.10 = 10%). Set <=0 to disable headroom.
//
// The interceptor only fires on procedures listed in `UploadProcedures`,
// keyed by the full path "/paladin.data.v1.ObjectService/UploadObject" etc. —
// all other RPCs pass through unchanged.
type QuotaSoftCheck struct {
	Reader           QuotaReader
	UploadProcedures map[string]struct{}
	Headroom         float64
}

// NewQuotaSoftCheck builds an interceptor with sensible defaults: gate on
// UploadObject + InitiateMultipartUpload + RegenerateUploadUrl with 10%
// headroom.
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

func (q *QuotaSoftCheck) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if _, ok := q.UploadProcedures[req.Spec().Procedure]; !ok {
			return next(ctx, req)
		}
		if err := q.checkUpload(ctx, req); err != nil {
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

// checkUpload reads the size hint from the request via reflection-style
// interface assertions and rejects when projected usage exceeds the cap.
// Missing tenant quota = no-cap; unknown size = pass (worker enforces post-hoc).
func (q *QuotaSoftCheck) checkUpload(ctx context.Context, req connect.AnyRequest) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil || p.TenantID == uuid.Nil {
		// No tenant = nothing to enforce against — let downstream auth reject.
		return nil
	}
	quota, err := q.Reader.GetTenant(ctx, p.TenantID)
	if err != nil {
		// Tenant-quota row absent = unlimited. Other errors degrade open
		// (presigns are not blast-radius events).
		return nil
	}
	if quota.MaxTotalBytes <= 0 && quota.MaxObjectCount <= 0 {
		return nil
	}
	sizeHint := extractSizeHint(req.Any())
	threshold := float64(quota.MaxTotalBytes) * (1 + maxF(q.Headroom, 0))
	if quota.MaxTotalBytes > 0 && float64(quota.UsageTotalBytes+sizeHint) > threshold {
		return connect.NewError(connect.CodeResourceExhausted,
			fmt.Errorf("tenant quota exceeded: %d + %d > %d (max_total_bytes)",
				quota.UsageTotalBytes, sizeHint, quota.MaxTotalBytes))
	}
	if quota.MaxObjectCount > 0 && quota.UsageObjectCount+1 > quota.MaxObjectCount {
		return connect.NewError(connect.CodeResourceExhausted,
			errors.New("tenant quota exceeded: object count cap reached"))
	}
	return nil
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

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// silence unused
var _ = strings.HasPrefix
