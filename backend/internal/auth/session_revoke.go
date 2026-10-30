package auth

import (
	"context"
	"fmt"
	"uuid"

	"github.com/oleg-tkachuk/limes"
)

// RevokeSession revokes every capability exchanged under session — an OAuth
// grant's refresh-token family — in tenant, batch after batch until none is
// left, and reports how many. It repeats safely: a capability already
// revoked is not counted again.
func RevokeSession(ctx context.Context, sessions limes.BulkRevoker, tenant, session uuid.UUID, note, actor string) (int64, error) {
	var revoked int64
	for {
		res, err := sessions.RevokeMatching(WithActingTenant(ctx, tenant), limes.RevokeMatchingRequest{
			TenantID: tenant, Session: session.String(), Note: note, Actor: actor,
		})
		if err != nil {
			return revoked, fmt.Errorf("revoke the capabilities of session %s: %w", session, err)
		}
		revoked += res.Revoked
		if !res.More {
			return revoked, nil
		}
	}
}
