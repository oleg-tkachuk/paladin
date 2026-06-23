package middleware

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
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

type auditMsg struct {
	Name string `json:"name"`
}

// TestAuditWrite_SynchronousDurable: the interceptor must Insert the audit
// row before the wrapped handler call returns — there is no background
// buffer to lose on a crash. We assert the writer saw exactly one Insert
// by the time WrapUnary's func returns.
func TestAuditWrite_SynchronousDurable(t *testing.T) {
	t.Parallel()
	w := &recordingWriter{}
	ic := AuditWithMirror(w, "test", false, nil).(*auditInterceptor)

	next := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		// Handler has not returned yet → audit must not have run.
		if w.calls != 0 {
			t.Fatalf("audit wrote before handler returned: %d", w.calls)
		}
		return connect.NewResponse(&auditMsg{Name: "ok"}), nil
	}

	req := connect.NewRequest(&auditMsg{Name: "create"})
	_, err := ic.WrapUnary(next)(context.Background(), req)
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
	ic := AuditWithMirror(w, "test", false, nil).(*auditInterceptor)

	next := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&auditMsg{Name: "ok"}), nil
	}
	req := connect.NewRequest(&auditMsg{Name: "create"})
	resp, err := ic.WrapUnary(next)(context.Background(), req)
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
