package data

import (
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

// Every path UpdateObject accepts must be a field of its request.
func TestUpdateMaskPathsAreProtoFields(t *testing.T) {
	msg := (&pb.UpdateObjectRequest{}).ProtoReflect().Descriptor()
	if bad := convx.UnknownMaskFields(msg, updateObjectPaths); len(bad) > 0 {
		t.Errorf("UpdateObject accepts %v, which %s has no field for", bad, msg.Name())
	}
}
