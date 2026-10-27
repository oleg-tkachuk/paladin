package authh

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// refusingAudit refuses every row, as audit_log's insert policy did for the
// rows this handler writes outside the interceptor.
type refusingAudit struct{}

func (refusingAudit) Insert(context.Context, admindomain.AuditEntry) error {
	return errors.New("new row violates row-level security policy")
}

// A row the handler writes itself that is refused says so in the log, named
// by its action: these rows record a replayed token or a tenant switch, and
// nothing else does.
func TestUnwrittenAuditRowsAreLogged(t *testing.T) {
	user := authstore.User{UserID: uuid.New(), TenantID: uuid.New()}
	for action, write := range map[string]func(h *Handler, ctx context.Context){
		actionRefreshReuse: func(h *Handler, ctx context.Context) {
			h.onRefreshReuse(ctx, uuid.New(), user.UserID, user.TenantID)
		},
		actionRefreshReplayed: func(h *Handler, ctx context.Context) {
			h.onRefreshReplayed(ctx, uuid.New(), user.UserID, user.TenantID)
		},
		actionTenantSwitched: func(h *Handler, ctx context.Context) {
			h.auditTenantSwitch(ctx, user, user)
		},
	} {
		t.Run(action, func(t *testing.T) {
			core, logs := observer.New(zap.ErrorLevel)
			ctx := logger.WithContext(context.Background(), zap.New(core))
			h := newHandler(&fakeUsers{}, &fakeRefresh{}, &stubMinter{}).WithReuseAudit(refusingAudit{}, zap.New(core))

			write(h, ctx)

			got := logs.FilterMessage("audit entry not written").All()
			if len(got) != 1 {
				t.Fatalf("logged %d unwritten-audit lines, want 1", len(got))
			}
			if a := got[0].ContextMap()["action"]; a != action {
				t.Errorf("action = %v, want %s", a, action)
			}
		})
	}
}
