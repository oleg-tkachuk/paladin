package auth

import (
	"context"
	"fmt"
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
	logKeyCapabilityID        = "capability_id"
	logKeyRunID               = "run_id"
)

// msgCapabilityHoldsReleased is the line ReleaseRevoked logs.
const msgCapabilityHoldsReleased = "holds of a revoked capability released"

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
// until none is left, then releases the holds of the revoked ones by the
// same selectors, in req's tenant or, for a revocation across tenants, in
// every tenant, and reports how many it revoked. It repeats safely: a
// capability already revoked is not counted again, nor a hold already
// released, and a release that failed is retried whole though the repeated
// revocation revokes nothing.
func (b *BulkRevocation) RevokeMatching(ctx context.Context, req limes.RevokeMatchingRequest) (int64, error) {
	if b == nil {
		return 0, nil
	}
	scoped := WithActingTenant(ctx, req.TenantID)
	if req.AllTenants {
		scoped = WithAllTenantsWrite(ctx)
	}
	var revoked int64
	for {
		res, err := b.Revoker.RevokeMatching(scoped, req)
		if err != nil {
			return revoked, fmt.Errorf("revoke matching capabilities: %w", err)
		}
		revoked += res.Revoked
		if !res.More {
			break
		}
	}
	var released int64
	if b.Holds != nil {
		n, err := b.releaseHolds(scoped, meter.ReleaseMatchingRequest{
			TenantID: req.TenantID, AllTenants: req.AllTenants, Subject: req.Subject, OnBehalfOf: req.OnBehalfOf,
			ClientID: req.ClientID, Session: req.Session, RunID: req.RunID,
		})
		released = n
		if err != nil {
			return revoked, err
		}
	}
	logRevoked(ctx, req, revoked, released)
	return revoked, nil
}

// ReleaseRevoked releases at once the holds of one revoked capability, and
// of those delegated from it, in tenant, and reports how many it released.
// It repeats safely: a hold already released is not counted again, and a
// live capability's holds are left, as their cost may still come.
func (b *BulkRevocation) ReleaseRevoked(ctx context.Context, tenant, capability uuid.UUID) (int64, error) {
	if b == nil || b.Holds == nil {
		return 0, nil
	}
	released, err := b.releaseHolds(WithActingTenant(ctx, tenant),
		meter.ReleaseMatchingRequest{TenantID: tenant, CapabilityID: capability})
	if err != nil {
		return released, err
	}
	level := zapcore.DebugLevel
	if released > 0 {
		level = zapcore.InfoLevel
	}
	logger.FromContext(ctx).Log(level, msgCapabilityHoldsReleased,
		zap.String(logKeyCapabilityID, capability.String()), zap.Int64(logKeyHoldsReleased, released))
	return released, nil
}

// releaseHolds releases, batch after batch, the holds of the revoked
// capabilities rel selects, and reports how many it released.
func (b *BulkRevocation) releaseHolds(ctx context.Context, rel meter.ReleaseMatchingRequest) (int64, error) {
	var released int64
	for {
		res, err := b.Holds.ReleaseMatching(ctx, rel)
		released += res.Released
		if err != nil {
			return released, fmt.Errorf("release the holds of revoked capabilities: %w", err)
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
		zap.String(logKeyRunID, runOf(req)),
		zap.Int64(LogKeyCapabilitiesRevoked, revoked), zap.Int64(logKeyHoldsReleased, released))
}

// RevokeSession revokes every capability exchanged under session — an OAuth
// grant's refresh-token family — in tenant, and releases their holds.
func (b *BulkRevocation) RevokeSession(ctx context.Context, tenant, session uuid.UUID, note, actor string) (int64, error) {
	return b.RevokeMatching(ctx, limes.RevokeMatchingRequest{
		TenantID: tenant, Session: session.String(), Note: note, Actor: actor,
	})
}

// runOf is the run a revocation selects by, empty for none.
func runOf(req limes.RevokeMatchingRequest) string {
	if req.RunID == uuid.Nil() {
		return ""
	}
	return req.RunID.String()
}

// RevokeRun revokes every capability of an agent run in tenant — the run's
// own and, through them, those delegated from them — and releases their
// holds, with the note and actor its revocation entries record.
func (b *BulkRevocation) RevokeRun(ctx context.Context, tenant, run uuid.UUID, note, actor string) (int64, error) {
	return b.RevokeMatching(ctx, limes.RevokeMatchingRequest{
		TenantID: tenant, RunID: run, Note: note, Actor: actor,
	})
}
