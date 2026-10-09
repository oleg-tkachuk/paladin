package capabilityh

import (
	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// limes's issuance errors, by kind. Anything Issue or
// Delegate returns that matches none of them is the store's or the signer's
// failure and maps to Internal.
func init() {
	apiutil.RegisterError(limes.ErrInvalidRequest, connect.CodeInvalidArgument, commonv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	// NotFound, not FailedPrecondition: the request names a resource that
	// does not exist. The reason tells it from any other NotFound, so a caller
	// waiting on a tenant being provisioned can retry on it alone.
	apiutil.RegisterError(limes.ErrUnknownTenant, connect.CodeNotFound, commonv1.ErrorReason_ERROR_REASON_TENANT_NOT_FOUND)
	apiutil.RegisterError(limes.ErrTenantDeleted, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_TENANT_ALREADY_DELETED)
}
