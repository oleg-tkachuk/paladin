package objecth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// An object deleted straight from its bucket lands in the trash through a
// storage event. Restoring it would list an object whose every download
// fails, so the restore is refused while its bytes are missing — and only
// then: a backend that cannot be reached says so instead.
func TestRestoreChecksTheObjectsBytes(t *testing.T) {
	t.Parallel()
	obj := Object{ObjectID: uuid.New(), Collection: "docs", Key: "a.txt"}
	for name, tc := range map[string]struct {
		headErr    error
		wantCode   connect.Code
		wantReason commonv1.ErrorReason
	}{
		"bytes present": {},
		"bytes missing": {
			headErr:    fmt.Errorf("head: %w", ErrStoredObjectNotFound),
			wantCode:   connect.CodeFailedPrecondition,
			wantReason: commonv1.ErrorReason_ERROR_REASON_OBJECT_BYTES_MISSING,
		},
		"backend unreachable": {
			headErr:  errors.New("dial tcp: connection refused"),
			wantCode: connect.CodeUnavailable,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := &Handler{repo: &fakeObjectRepo{}, storage: headStorage{err: tc.headErr}}
			err := h.assertBytesPresent(context.Background(), uuid.New(), obj)
			if tc.wantCode == 0 {
				if err != nil {
					t.Fatalf("assertBytesPresent = %v, want nil", err)
				}
				return
			}
			if got := connect.CodeOf(err); got != tc.wantCode {
				t.Fatalf("code = %v, want %v (%v)", got, tc.wantCode, err)
			}
			if tc.wantReason != commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED {
				if got := errorReason(err); got != tc.wantReason {
					t.Errorf("reason = %v, want %v", got, tc.wantReason)
				}
			}
		})
	}
}

func errorReason(err error) commonv1.ErrorReason {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED
	}
	for _, d := range cerr.Details() {
		if v, derr := connectproto.UnmarshalErrorDetail(d); derr == nil {
			if info, ok := v.(*errdetails.ErrorInfo); ok {
				return commonv1.ErrorReason(commonv1.ErrorReason_value[info.GetReason()])
			}
		}
	}
	return commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED
}

// trashedObjectStub serves one trashed object with nothing live at its key.
type trashedObjectStub struct{ restoreParentStub }

func (*trashedObjectStub) LiveCollision(context.Context, uuid.UUID, string, string) (bool, error) {
	return false, nil
}

// The check runs inside RestoreObject: a trashed object whose bytes are gone
// is refused before anything is restored.
func TestRestoreObjectRefusesAnObjectWithoutBytes(t *testing.T) {
	t.Parallel()
	tenantID := uuid.New()
	objectID := uuid.NewV7()
	h := &Handler{
		policy: allowAll{},
		repo: &trashedObjectStub{restoreParentStub{obj: Object{
			ObjectID: objectID, TenantID: tenantID, Collection: "docs", Key: "a.txt",
			State: statemachine.StateDeleted, ResourceVersion: 4,
		}}},
		storage: headStorage{err: ErrStoredObjectNotFound},
	}
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
	_, err := h.RestoreObject(ctx, "docs", objectID.String(), "4")
	if got := errorReason(err); got != commonv1.ErrorReason_ERROR_REASON_OBJECT_BYTES_MISSING {
		t.Fatalf("RestoreObject = %v (reason %v), want OBJECT_BYTES_MISSING", err, got)
	}
}
