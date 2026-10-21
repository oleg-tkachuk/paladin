//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/migrations"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// Batches that finished before 096 get the count of the objects they left
// behind from their response's failed; a response that is not a batch's,
// and one that is not JSON at all, keep 0 rather than failing the migration.
func TestOperationFailedCountBackfill(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	tenant := mustCreateTenant(t, h.PoolMigrate, "failed-count-backfill")

	rows := map[string]struct {
		state    string
		response string
		want     int64
	}{
		"a partial batch": {"SUCCEEDED", `{"total":2,"succeeded":1,"failed":1}`, 1},
		"a clean batch":   {"SUCCEEDED", `{"total":2,"succeeded":2,"failed":0}`, 0},
		"not a batch":     {"SUCCEEDED", `{"ok":true}`, 0},
		"not JSON":        {"SUCCEEDED", "\xff\xfe", 0},
		"a failed one":    {"FAILED", `{"error":"boom","failed":5}`, 0},
	}
	ids := map[string]uuid.UUID{}
	for name, r := range rows {
		id := uuid.Must(uuid.NewV7())
		ids[name] = id
		if _, err := h.PoolMigrate.Exec(ctx,
			`INSERT INTO operations (id, tenant_id, type, state, response, done_at) VALUES ($1, $2, 'BatchDelete', $3, $4, now())`,
			id, tenant, r.state, []byte(r.response)); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
	}

	raw, err := migrations.FS.ReadFile("097_operation_failed_count_backfill.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	up, _, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("the backfill migration has no Down marker")
	}
	if _, err := h.PoolMigrate.Exec(ctx, up); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	for name, r := range rows {
		var got int64
		if err := h.PoolMigrate.QueryRow(ctx, `SELECT failed_count FROM operations WHERE id = $1`, ids[name]).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if got != r.want {
			t.Errorf("%s: failed_count = %d, want %d", name, got, r.want)
		}
	}
}
