package capabilityh

import (
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/oleg-tkachuk/limes"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// A page token handed out for another tenant's listing — which pgstore
// refuses since limes v0.125 — is the caller's to fix, not the server's.
func TestCursorOfAnotherListingIsInvalidArgument(t *testing.T) {
	store := &recordingStore{listErr: limes.ErrCursorMismatch}
	h := mkHandler(t, store, nil, &allowAuthorizer{})
	_, err := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{TenantId: uuid.New().String()})
	assertKind(t, err, connect.CodeInvalidArgument, commonv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
}
