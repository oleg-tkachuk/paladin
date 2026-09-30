package middleware

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

// latencyWriter simulates the cost of the durable audit Insert — a single
// indexed append — by sleeping for a fixed duration before returning. Used
// to model the response-path tax form (A) adds under a realistic DB.
type latencyWriter struct {
	d     time.Duration
	calls int
}

func (w *latencyWriter) Insert(_ context.Context, _ admindomain.AuditEntry) error {
	w.calls++
	if w.d > 0 {
		time.Sleep(w.d)
	}
	return nil
}

func (w *latencyWriter) InsertWithOutbox(ctx context.Context, e admindomain.AuditEntry, onInserted func(context.Context, pgx.Tx) error) error {
	if err := w.Insert(ctx, e); err != nil {
		return err
	}
	if onInserted != nil {
		return onInserted(ctx, nil)
	}
	return nil
}

// BenchmarkAuditInterceptor is the ADR-0004 gate: it quantifies the latency
// the synchronous, crash-durable audit insert (form A) adds to a mutating
// RPC's response path. Compare the variants:
//
//   - baseline_no_audit  — the wrapped handler alone (no interceptor).
//   - interceptor_noop   — interceptor overhead with an instant Insert
//     (entry construction + dispatch; isolates the non-DB cost).
//   - insert_100us / insert_500us — interceptor with a simulated indexed
//     append, modelling realistic and pessimistic DB latency.
//
// The delta (insert_* minus baseline) is the per-RPC audit tax. ADR-0004
// keeps form (A) only while that tax leaves the mutating RPCs' p99 within
// budget; if a real-DB run here (swap latencyWriter for the Postgres
// AuditRepository) shows it does not, that triggers the fall-back to
// form (B) — the staging-table + projector.
//
// Run: go test ./internal/middleware/ -run '^$' -bench BenchmarkAuditInterceptor -benchmem
func BenchmarkAuditInterceptor(b *testing.B) {
	next := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&auditMsg{Name: "ok"}), nil
	}
	req := connect.NewRequest(&auditMsg{Name: "create"})

	b.Run("baseline_no_audit", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = next(context.Background(), req)
		}
	})

	cases := []struct {
		name   string
		writer AuditWriter
	}{
		{"interceptor_noop", &recordingWriter{}},
		{"insert_100us", &latencyWriter{d: 100 * time.Microsecond}},
		{"insert_500us", &latencyWriter{d: 500 * time.Microsecond}},
	}
	for _, tc := range cases {
		ic := AuditWithMirror(tc.writer, "test", false, nil).(*auditInterceptor)
		h := ic.WrapUnary(next)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = h(context.Background(), req)
			}
		})
	}
}
