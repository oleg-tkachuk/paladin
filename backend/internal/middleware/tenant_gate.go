package middleware

import (
	"context"
	"errors"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// DefaultTenantStateTTL is how long a cached state is trusted without a
// notification: the fallback for one lost while the watcher reconnected,
// which itself clears the cache.
const DefaultTenantStateTTL = 30 * time.Second

// ErrTenantTrashed is a credential of a tenant in the trash.
var ErrTenantTrashed = errors.New("tenant is in the trash; restore it to use its credentials")

// ErrTenantGone is a credential of a tenant that no longer exists.
var ErrTenantGone = errors.New("the credential's tenant does not exist")

// errTenantStateUnreadable is what a caller is told when the gate could not
// read its tenant's state.
var errTenantStateUnreadable = errors.New("the tenant's state could not be read; retry")

func init() {
	apiutil.RegisterError(ErrTenantTrashed, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_TENANT_ALREADY_DELETED)
	apiutil.RegisterError(ErrTenantGone, connect.CodeUnauthenticated, commonv1.ErrorReason_ERROR_REASON_UNAUTHENTICATED)
}

// CachedTenantStates keeps each tenant's state for ttl, or until Clear — which
// the tenant_state watcher calls on every change. A failed read is not cached.
type CachedTenantStates struct {
	reader auth.TenantStateReader
	ttl    time.Duration
	now    func() time.Time

	mu      sync.RWMutex
	entries map[uuid.UUID]cachedTenantState
}

type cachedTenantState struct {
	state   auth.TenantState
	expires time.Time
}

// NewCachedTenantStates caches reader's answers for ttl.
func NewCachedTenantStates(reader auth.TenantStateReader, ttl time.Duration) *CachedTenantStates {
	return &CachedTenantStates{reader: reader, ttl: ttl, now: time.Now, entries: map[uuid.UUID]cachedTenantState{}}
}

// TenantState is the cached state, read through on a miss.
func (c *CachedTenantStates) TenantState(ctx context.Context, tenantID uuid.UUID) (auth.TenantState, error) {
	now := c.now()
	c.mu.RLock()
	e, ok := c.entries[tenantID]
	c.mu.RUnlock()
	if ok && now.Before(e.expires) {
		return e.state, nil
	}
	state, err := c.reader.TenantState(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	c.entries[tenantID] = cachedTenantState{state: state, expires: now.Add(c.ttl)}
	c.mu.Unlock()
	return state, nil
}

// Clear drops every cached state.
func (c *CachedTenantStates) Clear() {
	c.mu.Lock()
	c.entries = map[uuid.UUID]cachedTenantState{}
	c.mu.Unlock()
}

// TenantGate refuses a call whose principal belongs to a tenant in the trash
// (FailedPrecondition, TENANT_ALREADY_DELETED) or to one that no longer
// exists (Unauthenticated). Moving a tenant to the trash revokes nothing — a
// restored tenant's credentials work again — so this is what stops them
// working meanwhile.
//
// A call with no principal (Login, RefreshToken), a principal with no tenant
// and one holding a platform role pass: a platform role's authority is not
// its tenant's, and gating it would let a trashed platform tenant lock out
// every admin who could restore it. A state that cannot be read refuses the call
// as Unavailable: the gate fails closed, and the caller retries.
//
// Install it after every interceptor that establishes a principal, and before
// validation and idempotency, so a refused call is neither validated against
// nor answered from a memoised response.
func TenantGate(states auth.TenantStateReader) connect.ServerInterceptor {
	return tenantGate{states: states}.intercept
}

type tenantGate struct{ states auth.TenantStateReader }

func (g tenantGate) check(ctx context.Context) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil || p == nil || p.TenantID == uuid.Nil || apiutil.HoldsPlatformRole(p) {
		return nil
	}
	state, err := g.states.TenantState(ctx, p.TenantID)
	if err != nil {
		// Unavailable is not scrubbed: the cause stays in the log.
		logger.FromContext(ctx).Warn("tenant state unreadable; refusing the call", zap.Error(err))
		return connect.NewError(connect.CodeUnavailable, errTenantStateUnreadable.Error()).WithCause(errTenantStateUnreadable)
	}
	switch state {
	case auth.TenantTrashed:
		return apiutil.MapError(ErrTenantTrashed)
	case auth.TenantMissing:
		return apiutil.MapError(ErrTenantGone)
	}
	return nil
}

func (g tenantGate) intercept(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		if err := g.check(ctx); err != nil {
			return err
		}
		return next(ctx, spec, stream)
	}
}
