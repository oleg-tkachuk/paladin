package audith

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

var errStoreDown = errors.New("connection reset")

func loggedHandler(repo admindomain.AuditRepository) (*Handler, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	h := NewHandler(repo, allowAuthorizer{})
	h.SetLogger(zap.New(core))
	return h, logs
}

// errorLines are the error lines of logs that name op.
func errorLines(logs *observer.ObservedLogs, op string) int {
	return logs.FilterLevelExact(zapcore.ErrorLevel).FilterField(zap.String("op", op)).Len()
}

// A listing the store fails used to reach the caller with no code (UNKNOWN)
// and no line saying why.
func TestListAuditLogAnswersAFailingStoreAsInternal(t *testing.T) {
	h, logs := loggedHandler(failingListRepo{err: errStoreDown})
	_, _, err := h.ListAuditLog(ctxWithPlatformAdmin(t), admindomain.ListAuditArgs{}, "")
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
	if errorLines(logs, opList) != 1 {
		t.Errorf("want one error line, got %v", logs.All())
	}
}

func TestCountAuditFacetsLogsAFailingStore(t *testing.T) {
	h, logs := loggedHandler(&fakeAuditRepo{countErr: errStoreDown})
	if _, err := h.CountAuditFacets(ctxWithPlatformAdmin(t), admindomain.ListAuditArgs{}, ""); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
	if errorLines(logs, opCountFacets) != 1 {
		t.Errorf("want one error line, got %v", logs.All())
	}
}

func TestGetAuditLogEntryLogsTheStoreAnswer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		code  connect.Code
		level zapcore.Level
	}{
		{"store failing", errStoreDown, connect.CodeInternal, zapcore.ErrorLevel},
		{"not there", admindomain.ErrNotFound, connect.CodeNotFound, zapcore.WarnLevel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			h, logs := loggedHandler(&oneEntryRepo{err: tc.err})
			if _, err := h.GetAuditLogEntry(ctxWithPlatformAdmin(t), id); connect.CodeOf(err) != tc.code {
				t.Fatalf("code = %v, want %v", connect.CodeOf(err), tc.code)
			}
			if logs.FilterLevelExact(tc.level).FilterField(zap.String("entry_id", id.String())).Len() != 1 {
				t.Errorf("want one %v line naming the entry, got %v", tc.level, logs.All())
			}
		})
	}
}

func TestExportAuditLogLogsAFailingStore(t *testing.T) {
	h, logs := loggedHandler(failingListRepo{err: errStoreDown})
	if _, err := h.ExportAuditLog(ctxWithPlatformAdmin(t), "", ""); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
	if errorLines(logs, opExport) != 1 {
		t.Errorf("want one error line, got %v", logs.All())
	}
}

// An export is a bulk read of the trail, so it says who took it and how much
// it held.
func TestExportAuditLogIsLogged(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	repo := &fakeAuditRepo{pages: [][]admindomain.AuditEntry{{mkEntry(base), mkEntry(base.Add(time.Second))}}}
	h, logs := loggedHandler(repo)
	if _, err := h.ExportAuditLog(ctxWithPlatformAdmin(t), "", "s3://x"); err != nil {
		t.Fatal(err)
	}
	lines := logs.FilterLevelExact(zapcore.InfoLevel).FilterField(zap.String("op", opExport)).All()
	if len(lines) != 1 {
		t.Fatalf("want one info line, got %v", logs.All())
	}
	f := lines[0].ContextMap()
	if f["rows"] != int64(2) || f["truncated"] != false || f["actor"] != "ops@example.com" {
		t.Errorf("line = %v, want rows, truncated and actor", f)
	}
}

// A cross-tenant listing is refused, and the refusal says which tenant was
// asked for.
func TestListScopeRefusalIsLogged(t *testing.T) {
	own := uuid.New()
	h, logs := loggedHandler(&fakeAuditRepo{})
	_, _, err := h.ListAuditLog(ctxAsTenantAdmin(own), admindomain.ListAuditArgs{TrailTenantID: uuid.New()}, "")
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", connect.CodeOf(err))
	}
	if logs.FilterLevelExact(zapcore.WarnLevel).Len() != 1 {
		t.Errorf("want one warn line, got %v", logs.All())
	}
}

func TestCrossTenantAudit(t *testing.T) {
	own, other := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name          string
		actor, trail  uuid.UUID
		wantForbidden bool
	}{
		{"none named", uuid.Nil(), uuid.Nil(), false},
		{"own actor", own, uuid.Nil(), false},
		{"own trail", uuid.Nil(), own, false},
		{"other actor", other, uuid.Nil(), true},
		{"other trail", uuid.Nil(), other, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := crossTenant(admindomain.ListAuditArgs{ActorTenantID: tc.actor, TrailTenantID: tc.trail}, own)
			if got := connect.CodeOf(err) == connect.CodePermissionDenied; got != tc.wantForbidden {
				t.Errorf("forbidden = %v (%v), want %v", got, err, tc.wantForbidden)
			}
			if !tc.wantForbidden && err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}
