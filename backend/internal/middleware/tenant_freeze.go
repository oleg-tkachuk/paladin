package middleware

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// ErrTenantFrozen is a change to a tenant in the trash.
var ErrTenantFrozen = errors.New("the tenant is in the trash and takes no changes; " +
	"restore it to change it, or restore, empty and purge it to remove it")

func init() {
	apiutil.RegisterError(ErrTenantFrozen, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_TENANT_ALREADY_DELETED)
}

// TenantSlugs finds a tenant by its slug as the handlers do: the live tenant
// holding it, else the most recently trashed one.
type TenantSlugs interface {
	TenantIDBySlug(ctx context.Context, slug string) (id uuid.UUID, found bool, err error)
}

// TenantFreeze refuses a change to a tenant in the trash — FailedPrecondition,
// TENANT_ALREADY_DELETED — whoever asks for it. The trash is a tenant frozen
// until someone decides: restoring returns exactly what was trashed, so
// nothing new may enter meanwhile. Reads, withdrawals (revoking a credential,
// cancelling work) and the tenant's own lifecycle stay open: an admin
// inspects, exports, takes access away, restores or purges.
//
// The tenants a call acts on are the ones it names, in a resource name or a
// tenant field; a call naming none is left to TenantGate, which has already
// refused a trashed tenant's own credentials. A tenant that does not exist is
// left to the handler's NotFound. A state that cannot be read refuses the
// change as Unavailable.
//
// Install it after TenantGate. Unary only: no change is a streaming RPC
// (TestNoChangeIsStreaming).
func TenantFreeze(states auth.TenantStateReader, slugs TenantSlugs) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if err := checkFrozen(ctx, states, slugs, req.Spec().Procedure, req.Any()); err != nil {
				return nil, err
			}
			return next(ctx, req)
		}
	}
}

func checkFrozen(ctx context.Context, states auth.TenantStateReader, slugs TenantSlugs, procedure string, msg any) error {
	// An RPC nobody classified is treated as a change: fail closed.
	if kind, ok := procedureKinds[procedure]; ok && kind != Changes {
		return nil
	}
	m, ok := msg.(proto.Message)
	if !ok {
		return nil
	}
	refs := requestTenants(m.ProtoReflect())
	for slug := range refs.slugs {
		id, found, err := slugs.TenantIDBySlug(ctx, slug)
		if err != nil {
			return unreadable(ctx, err)
		}
		if found {
			refs.ids[id] = true
		}
	}
	for id := range refs.ids {
		state, err := states.TenantState(ctx, id)
		if err != nil {
			return unreadable(ctx, err)
		}
		if state == auth.TenantTrashed {
			return apiutil.MapError(fmt.Errorf("%w (tenant %s)", ErrTenantFrozen, id))
		}
	}
	return nil
}

func unreadable(ctx context.Context, err error) error {
	logger.FromContext(ctx).Warn("tenant state unreadable; refusing the change", zap.Error(err))
	return connect.NewError(connect.CodeUnavailable, errTenantStateUnreadable)
}
