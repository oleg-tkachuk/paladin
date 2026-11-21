//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// An upload the store does not have is ErrUploadNotFound, whether the id is
// unknown or does not parse; the handler answers anything else as the store
// failing. The adapter used to answer it with a bare error the handler could
// not tell from an outage, and passed an unparseable id to Postgres, which
// refuses it as a malformed uuid rather than matching no row.
func TestGetSession_MissingIsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewMultipartRepo(sqlc.New(pool), pool)

	for name, id := range map[string]string{
		"unknown id":     uuid.NewV7().String(),
		"unparseable id": "not-a-uuid",
	} {
		if _, err := repo.GetSession(ctx, id); !errors.Is(err, multiparth.ErrUploadNotFound) {
			t.Errorf("%s: err = %v, want ErrUploadNotFound", name, err)
		}
	}
}
