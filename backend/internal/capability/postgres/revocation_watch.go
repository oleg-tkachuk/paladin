package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/notify"
)

// RevokedChannel is the LISTEN/NOTIFY channel migration 033 announces every
// revoking statement on.
const RevokedChannel = "capability_revoked"

// RevocationWatcher turns revocations made anywhere — any replica, an
// operator's psql — into an immediate cache flush on this one. Without it a
// revocation reached other replicas only when their cached answer expired.
type RevocationWatcher = notify.Watcher

// NewRevocationWatcher builds a watcher that calls onRevoked — typically
// CachedRevocationChecker.Clear — whenever a capability is revoked, and after
// every reconnect.
func NewRevocationWatcher(pool *pgxpool.Pool, onRevoked func()) *RevocationWatcher {
	return notify.New(pool, RevokedChannel, onRevoked)
}
