package auth

import (
	"context"
	"fmt"
	"slices"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// The log keys of a bulk revocation's outcome. A handler that reports the
// count it was answered logs it under the same key.
const (
	LogKeyCapabilitiesRevoked = "capabilities_revoked"
	logKeyHoldsReleased       = "holds_released"
)

// BulkRevocation revokes capabilities in bulk and releases at once the holds
// of those it revoked, which would otherwise count against their ceilings
// until they expire. A nil *BulkRevocation — the capability subsystem off —
// revokes nothing.
type BulkRevocation struct {
	Revoker limes.BulkRevoker
	// Holds releases the holds of the capabilities revoked; nil while
	// metering is off.
	Holds meter.MatchingReleaser
}

// RevokeMatching revokes every capability req selects, batch after batch
// until none is left, then releases the holds of the revoked ones in req's
// tenant and in each tenant a revocation across tenants wrote in, and
// reports how many it revoked. It repeats safely: a capability already
// revoked is not counted again, nor a hold already released.
func (b *BulkRevocation) RevokeMatching(ctx context.Context, req limes.RevokeMatchingRequest) (int64, error) {
	if b == nil {
		return 0, nil
	}
	var revoked int64
	tenants := []uuid.UUID{}
	if !req.AllTenants {
		tenants = append(tenants, req.TenantID)
	}
	revokeCtx := WithActingTenant(ctx, req.TenantID)
	if req.AllTenants {
		revokeCtx = WithAllTenantsWrite(ctx)
	}
	for {
		res, err := b.Revoker.RevokeMatching(revokeCtx, req)
		if err != nil {
			return revoked, fmt.Errorf("revoke matching capabilities: %w", err)
		}
		revoked += res.Revoked
		for _, t := range res.Tenants {
			if !slices.Contains(tenants, t) {
				tenants = append(tenants, t)
			}
		}
		if !res.More {
			break
		}
	}
	var released int64
	if b.Holds != nil {
		for _, t := range tenants {
			n, err := b.releaseHolds(WithActingTenant(ctx, t), t, req)
			released += n
			if err != nil {
				return revoked, err
			}
		}
	}
	logRevoked(ctx, req, revoked, released)
	return revoked, nil
}

// releaseHolds releases, batch after batch, the holds of tenant's revoked
// capabilities that req selects, and reports how many it released.
func (b *BulkRevocation) releaseHolds(ctx context.Context, tenant uuid.UUID, req limes.RevokeMatchingRequest) (int64, error) {
	var released int64
	for {
		res, err := b.Holds.ReleaseMatching(ctx, meter.ReleaseMatchingRequest{
			TenantID: tenant, Subject: req.Subject, OnBehalfOf: req.OnBehalfOf,
			ClientID: req.ClientID, Session: req.Session, RunID: req.RunID,
		})
		released += res.Released
		if err != nil {
			return released, fmt.Errorf("release the holds of revoked capabilities in tenant %s: %w", tenant, err)
		}
		if !res.More {
			return released, nil
		}
	}
}

// logRevoked logs what a revocation selected and what it stopped: at info
// when it revoked or released something, at debug when a repeat found
// nothing left. A failure is the caller's to log, with what it answers.
func logRevoked(ctx context.Context, req limes.RevokeMatchingRequest, revoked, released int64) {
	level := zapcore.DebugLevel
	if revoked > 0 || released > 0 {
		level = zapcore.InfoLevel
	}
	logger.FromContext(ctx).Log(level, "capabilities revoked in bulk",
		zap.Bool("all_tenants", req.AllTenants), zap.String("client_id", req.ClientID), zap.String("session", req.Session),
		zap.Int64(LogKeyCapabilitiesRevoked, revoked), zap.Int64(logKeyHoldsReleased, released))
}

// RevokeSession revokes every capability exchanged under session — an OAuth
// grant's refresh-token family — in tenant, and releases their holds.
func (b *BulkRevocation) RevokeSession(ctx context.Context, tenant, session uuid.UUID, note, actor string) (int64, error) {
	return b.RevokeMatching(ctx, limes.RevokeMatchingRequest{
		TenantID: tenant, Session: session.String(), Note: note, Actor: actor,
	})
}
