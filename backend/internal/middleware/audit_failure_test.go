package middleware

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// An audit writer that always fails, which is the state this test is about.
type failingAuditWriter struct{ calls int }

func (w *failingAuditWriter) Insert(context.Context, admindomain.AuditEntry) error {
	w.calls++
	return errors.New("relation \"audit_log\" does not exist")
}

func (w *failingAuditWriter) InsertWithOutbox(context.Context, admindomain.AuditEntry,
	func(context.Context, pgx.Tx) error) error {
	w.calls++
	return errors.New("relation \"audit_log\" does not exist")
}

// The compliance trail must not fail the RPC — and must not fail in silence.
//
// The interceptor discarded this error with `_ =`, so a broken audit table
// stopped the trail with no error, no metric and no log line. That is the one
// failure mode which makes an audit log worse than not having one: its absence
// reads as "nothing happened".
func TestAuditWriteFailureIsLoggedAndDoesNotFailTheRPC(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	w := &failingAuditWriter{}

	client := paladiniamv1connect.NewUserSettingsServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterUserSettingsServiceHandler(s, &stubSettings{})
	}, AuditWithMirror(w, "paladin-iam", false, nil)))
	// The context logger is what the interceptor writes through; the
	// in-process transport serves the call on this context.
	ctx := logger.WithContext(principalCtx(uuid.New()), zap.New(core))

	// A mutation, so the interceptor actually tries to write a row.
	if _, err := client.UpdateMine(ctx,
		&iamv1.UpdateMineRequest{}); err != nil {
		t.Fatalf("the RPC must succeed even when the audit write fails: %v", err)
	}
	if w.calls == 0 {
		t.Fatal("the audit writer was never called — this test would pass for " +
			"the wrong reason, since nothing failed to be logged")
	}

	found := logs.FilterMessage("audit entry not written").All()
	if len(found) != 1 {
		t.Fatalf("a failed audit write produced %d log lines, want 1 — the "+
			"compliance trail stopped and nothing said so", len(found))
	}
	if found[0].Level != zap.ErrorLevel {
		t.Errorf("logged at %v, want error: an audit write that fails is a "+
			"defect with an external consequence, not routine noise", found[0].Level)
	}
}
