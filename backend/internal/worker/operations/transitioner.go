package operations

import (
	"context"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// Transitioner is the slice of *statemachine.Transitioner the batch
// executors use. Extracted as an interface so the compensation /
// state-transition branches are unit-testable with a fake — production
// passes the concrete *statemachine.Transitioner, which satisfies this.
type Transitioner interface {
	PromoteToAvailable(ctx context.Context, objectID uuid.UUID, etag string, sizeBytes int64, checksum, sequencer string, source statemachine.Source) (changed bool, err error)
	MarkFailed(ctx context.Context, objectID uuid.UUID, reason string) error
	SoftDelete(ctx context.Context, objectID uuid.UUID, resourceVersion int64) error
	Restore(ctx context.Context, objectID uuid.UUID) error
}

// Compile-time guard: the concrete type must satisfy the seam.
var _ Transitioner = (*statemachine.Transitioner)(nil)
