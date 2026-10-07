package middleware

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// recordingWriter captures Insert calls so the test can assert the audit
// row was written on the response path (synchronous + durable, ADR-0004).
type recordingWriter struct {
	calls int
	last  admindomain.AuditEntry
	err   error
}

func (w *recordingWriter) Insert(_ context.Context, e admindomain.AuditEntry) error {
	w.calls++
	w.last = e
	return w.err
}

func (w *recordingWriter) InsertWithOutbox(ctx context.Context, e admindomain.AuditEntry, onInserted func(context.Context, pgx.Tx) error) error {
	if err := w.Insert(ctx, e); err != nil {
		return err
	}
	if onInserted != nil {
		return onInserted(ctx, nil)
	}
	return nil
}

// auditedSpec is a mutation's Spec, which the audit interceptor records.
var auditedSpec = connect.Spec{
	StreamType: connect.StreamTypeUnary,
	Procedure:  paladiniamv1connect.UserSettingsServiceUpdateMineProcedure,
}

// newAuditedRequest and newAuditedResponse are the mutation's messages.
func newAuditedRequest() proto.Message  { return &iamv1.UpdateMineRequest{Timezone: "Europe/Kyiv"} }
func newAuditedResponse() proto.Message { return &iamv1.UserSettings{Timezone: "Europe/Kyiv"} }

// newAudit is the interceptor AuditWithMirror(w, "test", false, nil) builds,
// held as itself so a test reaches its whole-call function.
func newAudit(w AuditWriter) *auditInterceptor {
	return &auditInterceptor{w: w, audience: "test"}
}

// TestAuditWrite_SynchronousDurable: the interceptor must Insert the audit
// row before the wrapped handler call returns — there is no background
// buffer to lose on a crash. We assert the writer saw exactly one Insert
// by the time the interceptor's whole-call func returns.
func TestAuditWrite_SynchronousDurable(t *testing.T) {
	t.Parallel()
	w := &recordingWriter{}
	ic := newAudit(w)

	next := func(context.Context, connect.Spec, proto.Message) (proto.Message, error) {
		// Handler has not returned yet → audit must not have run.
		if w.calls != 0 {
			t.Fatalf("audit wrote before handler returned: %d", w.calls)
		}
		return newAuditedResponse(), nil
	}

	_, err := ic.unary(next)(context.Background(), auditedSpec, newAuditedRequest())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// Row is durable (committed) before the response reached the caller.
	if w.calls != 1 {
		t.Fatalf("want exactly one synchronous Insert, got %d", w.calls)
	}
}

// TestAuditWrite_InsertErrorDoesNotFailRPC: a durable write that errors is
// still best-effort — the RPC result is unchanged (we never want an audit
// hiccup to fail a caller's mutation that already committed).
func TestAuditWrite_InsertErrorDoesNotFailRPC(t *testing.T) {
	t.Parallel()
	w := &recordingWriter{err: errors.New("db down")}
	ic := newAudit(w)

	next := func(context.Context, connect.Spec, proto.Message) (proto.Message, error) {
		return newAuditedResponse(), nil
	}
	resp, err := ic.unary(next)(context.Background(), auditedSpec, newAuditedRequest())
	if err != nil {
		t.Fatalf("audit Insert error must not fail the RPC, got %v", err)
	}
	if resp == nil {
		t.Fatal("response dropped")
	}
	if w.calls != 1 {
		t.Fatalf("want one Insert attempt, got %d", w.calls)
	}
}
