package quotah

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// observed is a handler over repo whose log lines, at level and above, the
// test reads back.
func observed(repo *fakeQuotaRepo, level zapcore.Level) (*Handler, *observer.ObservedLogs) {
	core, logs := observer.New(level)
	h := NewHandler(repo, allowAuthorizer{})
	h.SetLogger(zap.New(core))
	return h, logs
}

// The calls an outcome test makes, each as a platform admin.
func getTenant(h *Handler) error {
	_, err := h.GetTenantQuota(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), uuid.New())
	return err
}

func getBucket(h *Handler) error {
	_, err := h.GetBucketQuota(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), "primary", "shared")
	return err
}

func setTenant(h *Handler) error {
	_, err := h.SetQuota(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		admindomain.Quota{TenantID: uuid.New(), MaxTotalBytes: new(int64(1))}, []string{CapMaxTotalBytes})
	return err
}

func setBucket(h *Handler) error {
	_, err := h.SetQuota(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		admindomain.Quota{BackendID: "primary", BucketName: "shared", MaxTotalBytes: new(int64(1))}, []string{CapMaxTotalBytes})
	return err
}

func reset(h *Handler) error {
	return h.ResetUsage(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), uuid.New())
}

// failingEvents fails every event it is asked to record.
type failingEvents struct{ err error }

func (f failingEvents) RecordTx(context.Context, pgx.Tx, string, worker.Event) error { return f.err }

// A store that fails is an outage, answered internal and logged once at
// Error naming the operation — never a quota the caller is told is absent,
// in conflict, or refused.
func TestQuotaStoreFailuresAreInternalAndLogged(t *testing.T) {
	boom := errors.New("connection reset")
	tenantQuota := admindomain.Quota{QuotaID: uuid.New(), TenantID: uuid.New()}
	for _, tc := range []struct {
		name   string
		repo   *fakeQuotaRepo
		events EventProducer
		call   func(*Handler) error
		op     string
	}{
		{"get tenant", &fakeQuotaRepo{tenantErr: boom}, nil, getTenant, opGetTenantQuota},
		{"get bucket", &fakeQuotaRepo{bucketErr: boom}, nil, getBucket, opGetBucketQuota},
		{"set: read the stored caps", &fakeQuotaRepo{tenantErr: boom}, nil, setTenant, opSetQuota},
		{"set: tenant upsert", &fakeQuotaRepo{upsertErr: boom}, nil, setTenant, opSetQuota},
		{"set: tenant event", &fakeQuotaRepo{}, failingEvents{boom}, setTenant, opSetQuota},
		{"set: read back", &fakeQuotaRepo{readBackErr: boom}, nil, setTenant, opSetQuota},
		{"set: bucket upsert", &fakeQuotaRepo{upsertErr: boom}, nil, setBucket, opSetQuota},
		{"reset: read the quota", &fakeQuotaRepo{byIDErr: boom}, nil, reset, opResetUsage},
		{"reset: tenant counters", &fakeQuotaRepo{byID: tenantQuota, resetErr: boom}, nil, reset, opResetUsage},
		{"reset: bucket counters", &fakeQuotaRepo{byID: admindomain.Quota{QuotaID: uuid.New(), BackendID: "primary", BucketName: "shared"}, resetErr: boom}, nil, reset, opResetUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, logs := observed(tc.repo, zapcore.WarnLevel)
			if tc.events != nil {
				h.SetEventProducer(tc.events)
			}
			err := tc.call(h)
			if codeOf(err) != connect.CodeInternal || !errors.Is(err, boom) {
				t.Fatalf("err = %v (%v), want internal wrapping the store's", err, codeOf(err))
			}
			lines := logs.All()
			if len(lines) != 1 || lines[0].Level != zapcore.ErrorLevel || lines[0].ContextMap()["op"] != tc.op {
				t.Fatalf("want one error line for %s, got %v", tc.op, lines)
			}
		})
	}
}

// A refusal keeps its code and is logged once at Warn, never at Error.
func TestQuotaRefusalsKeepTheirCodeAndWarn(t *testing.T) {
	missing := fmt.Errorf("quota: %w", admindomain.ErrNotFound)
	for _, tc := range []struct {
		name string
		repo *fakeQuotaRepo
		call func(*Handler) error
		want connect.Code
	}{
		{"get tenant: none", &fakeQuotaRepo{tenantErr: missing}, getTenant, connect.CodeNotFound},
		{"get bucket: none", &fakeQuotaRepo{bucketErr: missing}, getBucket, connect.CodeNotFound},
		{"set: version moved", &fakeQuotaRepo{upsertErr: admindomain.ErrVersionMismatch}, setTenant, connect.CodeAborted},
		{"set: no such tenant", &fakeQuotaRepo{upsertErr: missing}, setTenant, connect.CodeNotFound},
		{"set: no such bucket", &fakeQuotaRepo{upsertErr: missing}, setBucket, connect.CodeNotFound},
		{"set: a mask naming no cap", &fakeQuotaRepo{}, func(h *Handler) error {
			_, err := h.SetQuota(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), admindomain.Quota{TenantID: uuid.New()}, nil)
			return err
		}, connect.CodeInvalidArgument},
		{"set: no scope", &fakeQuotaRepo{}, func(h *Handler) error {
			_, err := h.SetQuota(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), admindomain.Quota{}, []string{CapMaxTotalBytes})
			return err
		}, connect.CodeInvalidArgument},
		{"set: role", &fakeQuotaRepo{}, func(h *Handler) error {
			_, err := h.SetQuota(ctxAs(uuid.New(), apiutil.RoleTenantUser), admindomain.Quota{TenantID: uuid.New()}, nil)
			return err
		}, connect.CodePermissionDenied},
		{"get tenant: another tenant's", &fakeQuotaRepo{}, func(h *Handler) error {
			_, err := h.GetTenantQuota(ctxAs(uuid.New(), apiutil.RoleTenantAdmin), uuid.New())
			return err
		}, connect.CodePermissionDenied},
		{"get bucket: role", &fakeQuotaRepo{}, func(h *Handler) error {
			_, err := h.GetBucketQuota(ctxAs(uuid.New(), apiutil.RoleTenantUser), "primary", "shared")
			return err
		}, connect.CodePermissionDenied},
		{"reset: role", &fakeQuotaRepo{}, func(h *Handler) error {
			return h.ResetUsage(ctxAs(uuid.New(), apiutil.RoleBucketAdmin), uuid.New())
		}, connect.CodePermissionDenied},
		{"reset: no such quota", &fakeQuotaRepo{byIDErr: missing}, reset, connect.CodeNotFound},
		{"reset: gone before the reset", &fakeQuotaRepo{byID: admindomain.Quota{QuotaID: uuid.New(), TenantID: uuid.New()}, resetErr: missing}, reset, connect.CodeNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, logs := observed(tc.repo, zapcore.WarnLevel)
			err := tc.call(h)
			if codeOf(err) != tc.want {
				t.Fatalf("code = %v (%v), want %v", codeOf(err), err, tc.want)
			}
			lines := logs.All()
			if len(lines) != 1 || lines[0].Level != zapcore.WarnLevel {
				t.Fatalf("want one warning, got %v", lines)
			}
		})
	}
}

// Cedar refusing is a refusal too, logged at Warn.
func TestQuotaCedarDenialWarns(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	h := NewHandler(&fakeQuotaRepo{}, denyAuthorizer{})
	h.SetLogger(zap.New(core))
	if err := getTenant(h); codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
	if logs.FilterLevelExact(zapcore.WarnLevel).Len() != 1 {
		t.Fatalf("want one warning, got %v", logs.All())
	}
}

// Each change is logged at Info: what scope, which quota, by whom.
func TestQuotaChangesAreLogged(t *testing.T) {
	tenant, quotaID := uuid.New(), uuid.New()
	repo := &fakeQuotaRepo{
		tenantQuota: admindomain.Quota{QuotaID: quotaID, TenantID: tenant},
		bucketQuota: admindomain.Quota{QuotaID: quotaID, BackendID: "primary", BucketName: "shared"},
		byID:        admindomain.Quota{QuotaID: quotaID, TenantID: tenant},
	}
	h, logs := observed(repo, zapcore.InfoLevel)
	ctx := ctxAs(uuid.New(), apiutil.RolePlatformAdmin)
	if _, err := h.SetQuota(ctx, admindomain.Quota{TenantID: tenant, MaxTotalBytes: new(int64(1))}, []string{CapMaxTotalBytes}); err != nil {
		t.Fatal(err)
	}
	if err := setBucket(h); err != nil {
		t.Fatal(err)
	}
	if err := h.ResetUsage(ctx, quotaID); err != nil {
		t.Fatal(err)
	}
	set := logs.FilterMessage(msgQuotaSet).All()
	if len(set) != 2 {
		t.Fatalf("want two %q lines, got %v", msgQuotaSet, logs.All())
	}
	if m := set[0].ContextMap(); m["tenant_id"] != tenant.String() || m["quota_id"] != quotaID.String() || m["actor"] != "tester" {
		t.Errorf("tenant quota set line = %v", m)
	}
	if m := set[1].ContextMap(); m["backend_id"] != "primary" || m["bucket_name"] != "shared" || m["actor"] != "tester" {
		t.Errorf("bucket quota set line = %v", m)
	}
	resets := logs.FilterMessage(msgQuotaUsageReset).All()
	if len(resets) != 1 || resets[0].ContextMap()["quota_id"] != quotaID.String() || resets[0].ContextMap()["tenant_id"] != tenant.String() {
		t.Errorf("want one %q line naming the quota and its tenant, got %v", msgQuotaUsageReset, logs.All())
	}
}

// Caps written and then not found on the read back are the server's to
// explain, not a quota the caller is told is absent.
func TestQuotaWrittenButNotReadBackIsInternal(t *testing.T) {
	h, logs := observed(&fakeQuotaRepo{readBackErr: admindomain.ErrNotFound}, zapcore.ErrorLevel)
	if err := setTenant(h); codeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v (%v), want internal", codeOf(err), err)
	}
	if logs.Len() != 1 {
		t.Fatalf("want one error line, got %v", logs.All())
	}
}

// A nil logger keeps the one the handler has rather than leaving it nil.
func TestSetLoggerIgnoresNil(t *testing.T) {
	h := NewHandler(&fakeQuotaRepo{tenantErr: errors.New("connection reset")}, allowAuthorizer{})
	h.SetLogger(nil)
	if err := getTenant(h); codeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want internal", codeOf(err))
	}
}
