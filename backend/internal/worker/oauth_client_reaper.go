package worker

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
)

// OAuthClientReapBatch bounds one delete of the reaper, so a backlog is taken
// in short transactions rather than one long one.
const OAuthClientReapBatch = 500

// OAuthClientReaper removes self-registered OAuth clients (RFC 7591) older
// than MaxAge that hold neither a live grant nor a redeemable authorization
// code. Dynamic registration lets any caller add a client and nothing else
// removes one, so an agent that registers on every install leaves a row each
// time. Seeded clients are never reaped. Disabled when Interval <= 0.
//
// Store must run on the reaper pool: refresh_tokens is tenant-scoped by
// row-level security, and a connection scoped to one tenant would see no
// grant of any other, so their clients would be reaped with those grants.
type OAuthClientReaper struct {
	Store    oauth.ClientReaper
	Interval time.Duration
	MaxAge   time.Duration
	Logger   *zap.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

func (r *OAuthClientReaper) Run(ctx context.Context) error {
	if r.Interval <= 0 {
		return nil
	}
	return RunTicker(ctx, "oauth_client_reaper", r.Interval, r.reap)
}

// reap runs batches until one comes back short.
func (r *OAuthClientReaper) reap(ctx context.Context) error {
	now := r.now()
	var total int64
	for {
		n, err := r.Store.ReapIdleClients(ctx, now.Add(-r.MaxAge), now, OAuthClientReapBatch)
		total += n
		if err != nil {
			r.log().Warn("failed to reap idle oauth clients", zap.Int64("reaped", total), zap.Error(err))
			return err
		}
		if n < OAuthClientReapBatch {
			break
		}
	}
	if total > 0 {
		r.log().Info("reaped idle self-registered oauth clients", zap.Int64("clients", total))
	}
	return nil
}

func (r *OAuthClientReaper) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

func (r *OAuthClientReaper) log() *zap.Logger {
	if r.Logger == nil {
		return zap.NewNop()
	}
	return r.Logger
}
