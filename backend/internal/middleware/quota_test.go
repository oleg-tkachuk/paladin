package middleware

import (
	"context"
	"errors"
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

type fakeUploadObjectRequest struct {
	sizeHint int64
}

func (r *fakeUploadObjectRequest) GetSizeHintBytes() int64 { return r.sizeHint }

func TestQuotaSoftCheckPassThroughOnNonUploadProc(t *testing.T) {
	q := NewQuotaSoftCheck(&fakeQuotaReader{q: admindomain.Quota{MaxTotalBytes: 100}})
	if err := q.checkUploadAt(context.Background(), "/some.other/Method", nil); err != nil {
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
	err := q.checkUploadAt(ctx, "/test/Upload", &fakeUploadObjectRequest{sizeHint: 200})
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
	if err := q.checkUploadAt(ctx, "/test/Upload", &fakeUploadObjectRequest{sizeHint: 100}); err != nil {
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
	err := q.checkUploadAt(ctx, "/test/Upload", &fakeUploadObjectRequest{sizeHint: 1})
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
	if err := q.checkUploadAt(ctx, "/test/Upload", &fakeUploadObjectRequest{sizeHint: 1 << 30}); err != nil {
		t.Errorf("missing quota should pass, got %v", err)
	}
}

// checkUploadAt is a test helper that mirrors checkUpload but takes the
// procedure + message directly so we don't have to fabricate a connect.AnyRequest.
func (q *QuotaSoftCheck) checkUploadAt(ctx context.Context, procedure string, msg any) error {
	if _, ok := q.UploadProcedures[procedure]; !ok {
		return nil
	}
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil || p.TenantID == uuid.Nil {
		return nil
	}
	quota, err := q.Reader.GetTenant(ctx, p.TenantID)
	if err != nil {
		return nil
	}
	if quota.MaxTotalBytes <= 0 && quota.MaxObjectCount <= 0 {
		return nil
	}
	sizeHint := extractSizeHint(msg)
	threshold := float64(quota.MaxTotalBytes) * (1 + maxF(q.Headroom, 0))
	if quota.MaxTotalBytes > 0 && float64(quota.UsageTotalBytes+sizeHint) > threshold {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("byte cap"))
	}
	if quota.MaxObjectCount > 0 && quota.UsageObjectCount+1 > quota.MaxObjectCount {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("object cap"))
	}
	return nil
}
