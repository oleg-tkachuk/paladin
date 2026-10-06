package platformstats

import (
	"context"
	"fmt"
	"net/url"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Signal names one of the census counts the console flags for an operator's
// attention. CollectSignalTenants answers "whose": the tenants behind it.
//
// Only the counts that are a warning have a drill-down. The rest of the
// census is inventory, and a per-tenant table of every dimension is what the
// page deliberately does not carry (see the BACKLOG history of this file).
type Signal string

const (
	SignalQuotaAtLimit         Signal = "quota_at_limit"
	SignalQuotaNearLimit       Signal = "quota_near_limit"
	SignalCapabilitiesExpiring Signal = "capabilities_expiring"
	SignalAPITokensExpiring    Signal = "api_tokens_expiring" // #nosec G101 -- a signal name, not a credential
)

// signalQueries is each signal's per-tenant GROUP BY, built from the same
// predicate fragments its census count uses, so the drill-down's counts sum
// to the number on the card. Each returns (tenant_id text, count); a NULL
// tenant_id is a row no tenant owns.
var signalQueries = map[Signal]string{
	// A bucket quota belongs to the tenant owning the bucket; a shared
	// bucket has no owner, and its quota is counted as unattributed rather
	// than dropped.
	SignalQuotaAtLimit: `
		SELECT owner_tenant_id::text, count(*)
		FROM ` + allQuotaRowsSQL + `
		WHERE ` + quotaAtLimitSQL + `
		GROUP BY 1`,
	SignalQuotaNearLimit: `
		SELECT owner_tenant_id::text, count(*)
		FROM ` + allQuotaRowsSQL + `
		WHERE ` + quotaNearLimitSQL + ` AND NOT ` + quotaAtLimitSQL + `
		GROUP BY 1`,
	SignalCapabilitiesExpiring: `
		SELECT c.tenant_id::text, count(*)
		FROM capability_records c
		WHERE ` + capabilityExpiringSQL + `
		GROUP BY 1`,
	SignalAPITokensExpiring: `
		SELECT tenant_id::text, count(*)
		FROM api_tokens
		WHERE ` + apiTokenExpiringSQL + `
		GROUP BY 1`,
}

// signalArgs are the positional arguments a signal's query takes.
var signalArgs = map[Signal][]any{
	SignalQuotaNearLimit: {nearLimitRatio},
}

// ParseSignal reports whether s names a signal with a drill-down.
func ParseSignal(s string) (Signal, bool) {
	sig := Signal(s)
	_, ok := signalQueries[sig]
	return sig, ok
}

// TenantCount is one tenant's share of a signal.
type TenantCount struct {
	TenantID string `json:"tenant_id"`
	Count    int64  `json:"count"`
}

// SignalTenants is one page of the tenants behind a signal, biggest share
// first, ordered and paged exactly like ObjectCensus.Tenants.
type SignalTenants struct {
	Signal      Signal        `json:"signal"`
	Tenants     []TenantCount `json:"tenants"`
	TenantsCut  int64         `json:"tenants_truncated"`
	TenantsNext string        `json:"tenants_next_page_token,omitempty"`
	// Unattributed counts the rows behind the signal that belong to no
	// tenant: quotas on a shared bucket. Together with every page's counts it
	// adds up to the census number.
	Unattributed int64 `json:"unattributed"`
}

// CollectSignalTenants groups one signal's rows by tenant. It runs on the
// worker's BYPASSRLS pool for the same reason CollectRLS does.
func CollectSignalTenants(ctx context.Context, pool *pgxpool.Pool, signal Signal, page TenantPage) (*SignalTenants, error) {
	query, ok := signalQueries[signal]
	if !ok {
		return nil, fmt.Errorf("census: unknown signal %q", signal)
	}
	rows, err := pool.Query(ctx, query, signalArgs[signal]...)
	if err != nil {
		return nil, fmt.Errorf("census: %s: %w", signal, err)
	}
	defer rows.Close()

	out := &SignalTenants{Signal: signal}
	var tenants []TenantCount
	for rows.Next() {
		var id *string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("census: %s: scan: %w", signal, err)
		}
		if id == nil {
			out.Unattributed += n
			continue
		}
		tenants = append(tenants, TenantCount{TenantID: *id, Count: n})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("census: %s: %w", signal, err)
	}

	sort.Slice(tenants, func(i, j int) bool {
		return tenantBefore(tenants[i].Count, tenants[i].TenantID, tenants[j].Count, tenants[j].TenantID)
	})
	out.Tenants, out.TenantsNext, out.TenantsCut = pageRanked(tenants,
		func(t TenantCount) (int64, string) { return t.Count, t.TenantID }, page)
	if out.Tenants == nil {
		out.Tenants = []TenantCount{}
	}
	return out, nil
}

// signalParam carries the signal on the worker's drill-down endpoint, beside
// the TenantPage parameters.
const signalParam = "signal"

// SignalQuery encodes a signal and a page for the worker's drill-down
// endpoint.
func SignalQuery(signal Signal, page TenantPage) url.Values {
	q := page.Query()
	q.Set(signalParam, string(signal))
	return q
}

// SignalFromQuery reads what SignalQuery wrote. ok is false when the signal
// is missing or unknown.
func SignalFromQuery(q url.Values) (Signal, TenantPage, bool) {
	sig, ok := ParseSignal(q.Get(signalParam))
	return sig, TenantPageFromQuery(q), ok
}
