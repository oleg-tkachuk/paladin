package operations

import (
	"errors"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// conflictAttempts is how many times a batch tries one object whose version
// another write changed between the read and the change, each try on a
// fresh read: a conflict is a race, not a refusal, and one more read
// usually settles it. An object that still conflicts after them is a
// failure of the batch, with the conflict as its reason.
const conflictAttempts = 3

// errObjectNotFound is the reason a batch gives an id that names no object
// of its collection.
var errObjectNotFound = errors.New("not found")

// isVersionConflict reports whether err is an object's version changed under
// a batch's write: the state machine's conflict, or the repository's
// resource_version mismatch.
func isVersionConflict(err error) bool {
	return errors.Is(err, statemachine.ErrConflict) || errors.Is(err, objecth.ErrVersionMismatch)
}
