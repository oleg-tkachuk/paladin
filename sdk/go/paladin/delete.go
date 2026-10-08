package paladin

import (
	"context"
	"errors"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

// DeleteAttempts bounds how many times Delete reads an object again after it
// changed between the read and the delete.
const DeleteAttempts = 3

// DeleteOptions are Delete's choices.
type DeleteOptions struct {
	// Permanent destroys the object and its bytes instead of moving it to
	// the trash. A public collection takes only a permanent delete.
	Permanent bool
}

// Delete removes the object called name, supplying the resource_version the
// server requires on every delete: it reads the object, deletes it at the
// version read, and when the object changed in between reads it again, up to
// DeleteAttempts times. deleted reports whether this call removed it; an
// object already gone — NotFound on the read or the delete — is not an error,
// so an erasure that is retried, or that races another, succeeds.
func Delete(ctx context.Context, data *DataPlane, name string, opts DeleteOptions) (deleted bool, _ error) {
	var err error
	for range DeleteAttempts {
		var obj *datav1.Object
		obj, err = data.Object.GetObject(ctx, &datav1.GetObjectRequest{Name: name})
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		_, err = data.Object.DeleteObject(ctx, &datav1.DeleteObjectRequest{
			Name: name, ResourceVersion: obj.GetResourceVersion(), Permanent: opts.Permanent,
		})
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, ErrNotFound):
			return false, nil
		case !errors.Is(err, ErrVersionConflict):
			return false, err
		}
	}
	return false, err
}
