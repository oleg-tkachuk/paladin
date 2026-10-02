package iam

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// Every path UpdateUser accepts must be a field of its request.
func TestUpdateMaskPathsAreProtoFields(t *testing.T) {
	msg := (&pb.UpdateUserRequest{}).ProtoReflect().Descriptor()
	if bad := convx.UnknownMaskFields(msg, updateUserPaths); len(bad) > 0 {
		t.Errorf("UpdateUser accepts %v, which %s has no field for", bad, msg.Name())
	}
}

// An unknown path is refused before the handler runs: the server here has no
// handler, so reaching it would panic.
func TestUpdateUserRefusesAnUnknownMaskPath(t *testing.T) {
	s := &UserServer{}
	_, err := s.UpdateUser(context.Background(), connect.NewRequest(&pb.UpdateUserRequest{
		Name:            "tenants/" + uuid.NewString() + "/users/" + uuid.NewString(),
		ResourceVersion: "1",
		UpdateMask:      &fieldmaskpb.FieldMask{Paths: []string{"display_nam"}},
	}))
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodeInvalidArgument ||
		!strings.Contains(ce.Message(), "update_mask") {
		t.Fatalf("err = %v, want InvalidArgument naming the update_mask", err)
	}
}
