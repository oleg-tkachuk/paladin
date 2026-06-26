//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// BenchmarkAuditInsertDurable is the ADR-0004 gate read against a REAL
// Postgres (the unit-level internal/middleware/BenchmarkAuditInterceptor
// models the same cost with a sleeping fake). Form (A) writes the audit row
// synchronously on the RPC response path, so this single indexed append IS
// the per-mutating-RPC audit tax. Read ns/op as the latency the audit insert
// adds to every Create/Update/Delete; if it ever pushes a mutating RPC's p99
// over budget, that triggers the documented fall-back to form (B) (staging
// table + projector).
//
// Two shapes:
//   - no_payload: actor/action/resource only (the common mutation audit row).
//   - with_payload: ~1 KB before/after JSON (a metadata-heavy update).
//
// Run:
//
//	go test -tags=integration -run=^$ -bench=BenchmarkAuditInsertDurable \
//	    -benchmem ./internal/integration/...
func BenchmarkAuditInsertDurable(b *testing.B) {
	ctx := context.Background()
	pool := startPostgres(b)
	repo := adapters.NewAuditRepoV2(sqlc.New(pool))

	tenantID := uuid.New()
	base := admindomain.AuditEntry{
		At:            time.Now().UTC(),
		ActorSubject:  "svc-account",
		ActorTenantID: tenantID,
		ActorAudience: "admin",
		Action:        "admin.BucketService.CreateBucket",
		ResourceName:  "storageBackends/primary/buckets/acme",
		RequestID:     "req-bench",
		SourceIP:      "10.0.0.1",
	}
	payload := make([]byte, 0, 1024)
	payload = append(payload, '{')
	for len(payload) < 1000 {
		payload = append(payload, []byte(`"k":"vvvvvvvvvv",`)...)
	}
	payload = append(payload, '}')

	run := func(b *testing.B, after []byte) {
		b.Helper()
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			e := base
			e.EntryID = uuid.Must(uuid.NewV7()) // unique PK per iteration
			e.AfterJSON = after
			if err := repo.Insert(ctx, e); err != nil {
				b.Fatalf("audit insert: %v", err)
			}
		}
	}

	b.Run("no_payload", func(b *testing.B) { run(b, nil) })
	b.Run("with_payload", func(b *testing.B) { run(b, payload) })
}
