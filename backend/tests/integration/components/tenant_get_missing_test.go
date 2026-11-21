//go:build integration

package components

import (
	"context"
	"errors"
	"strings"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A tenant that is not there is the domain's ErrNotFound, which the handlers
// answer NOT_FOUND with "tenant not found". Get used to hand back the driver's
// pgx.ErrNoRows, and GetTenant answered "no rows in result set".
func TestTenantGet_MissingIsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)

	_, err := repo.Get(ctx, uuid.New())
	if !errors.Is(err, tenanth.ErrNotFound) {
		t.Fatalf("get a missing tenant: %v, want ErrNotFound", err)
	}
	if errors.Is(err, pgx.ErrNoRows) || strings.Contains(err.Error(), pgx.ErrNoRows.Error()) {
		t.Errorf("get a missing tenant: %q carries the driver's error", err)
	}
}
