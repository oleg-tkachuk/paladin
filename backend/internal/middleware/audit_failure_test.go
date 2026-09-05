package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	iamv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/internal/logger"
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

	mux := http.NewServeMux()
	path, handler := paladiniamv1connect.NewUserSettingsServiceHandler(&stubSettings{},
		connect.WithInterceptors(
			// The context logger is what the interceptor writes through.
			connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
				return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
					return next(logger.WithContext(ctx, zap.New(core)), req)
				}
			}),
			principalInjector(uuid.New()),
			AuditWithMirror(w, "paladin-iam", false, nil),
		),
	)
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := paladiniamv1connect.NewUserSettingsServiceClient(srv.Client(), srv.URL)
	// A mutation, so the interceptor actually tries to write a row.
	if _, err := client.UpdateMine(context.Background(),
		connect.NewRequest(&iamv1.UpdateMineRequest{})); err != nil {
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
