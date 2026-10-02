package admin

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// Every path an Update RPC accepts must be a field of the message it updates.
func TestUpdateMaskPathsAreProtoFields(t *testing.T) {
	cases := []struct {
		rpc   string
		msg   protoreflect.MessageDescriptor
		paths []string
	}{
		{"UpdateBackend", (&pb.StorageBackend{}).ProtoReflect().Descriptor(), updateBackendPaths},
		{"UpdateBucket", (&pb.Bucket{}).ProtoReflect().Descriptor(), updateBucketPaths},
		{"UpdateCollection", (&pb.Collection{}).ProtoReflect().Descriptor(), updateCollectionPaths},
		{"UpdateTenant", (&pb.Tenant{}).ProtoReflect().Descriptor(), updateTenantPaths},
		{"UpdateSubscription", (&pb.EventSubscription{}).ProtoReflect().Descriptor(), updateEventSubscriptionPaths},
	}
	for _, tc := range cases {
		if bad := convx.UnknownMaskFields(tc.msg, tc.paths); len(bad) > 0 {
			t.Errorf("%s accepts %v, which %s has no field for", tc.rpc, bad, tc.msg.Name())
		}
	}
}
