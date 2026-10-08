package middleware

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
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
// TENANT_ALREADY_DELETED — platform admins included. The trash is a tenant frozen
// until someone decides: restoring returns exactly what was trashed, so
// nothing new may enter meanwhile. Reads, withdrawals (revoking a credential,
// cancelling work) and the tenant's own lifecycle stay open: an admin
// inspects, exports, takes access away, restores or purges.
//
// It concerns the platform roles, the only principals that act on a tenant by
// naming it; any other principal is confined to its own tenant, which
// TenantGate has already checked. The tenants a call acts on are the ones it
// names, in a resource name or a tenant field. A tenant that does not exist is
// left to the handler's NotFound. A state that cannot be read refuses the
// change as Unavailable.
//
// Install it after TenantGate. Unary only: no change is a streaming RPC
// (TestNoChangeIsStreaming).
func TenantFreeze(states auth.TenantStateReader, slugs TenantSlugs) connect.ServerInterceptor {
	return unary.Interceptor(func(next unary.Func) unary.Func {
		return func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
			if err := checkFrozen(ctx, states, slugs, spec.Procedure, req); err != nil {
				return nil, err
			}
			return next(ctx, spec, req)
		}
	}, nil)
}

func checkFrozen(ctx context.Context, states auth.TenantStateReader, slugs TenantSlugs, procedure string, msg any) error {
	// Only a platform role acts on a tenant it names. Anyone else is confined
	// to its own tenant, which TenantGate has already checked; answering about
	// another tenant here would tell them its state before the handler refuses
	// to act on it.
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil || p == nil || !apiutil.HoldsPlatformRole(p) {
		return nil
	}
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
	return connect.NewError(connect.CodeUnavailable, errTenantStateUnreadable.Error()).WithCause(errTenantStateUnreadable)
}
