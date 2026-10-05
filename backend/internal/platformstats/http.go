package platformstats

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SignalTenantsPath is the worker ops endpoint serving CollectSignalTenants.
// The admin pod proxies it behind its platform-admin gate, as it does the
// census itself.
const SignalTenantsPath = "/system/rls-census/tenants.json"

// SignalTenantsHandler serves CollectSignalTenants for the signal and page in
// the query (SignalQuery). onError receives the failures the response only
// summarises.
func SignalTenantsHandler(pool *pgxpool.Pool, onError func(error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Same reason as the census endpoint: without the BYPASSRLS pool
		// every count reads as zero, which is an answer, and a wrong one.
		if pool == nil {
			onError(errors.New("signal drill-down requested but no BYPASSRLS pool is configured"))
			http.Error(w, "no bypassrls pool", http.StatusServiceUnavailable)
			return
		}
		signal, page, ok := SignalFromQuery(r.URL.Query())
		if !ok {
			http.Error(w, "unknown signal", http.StatusBadRequest)
			return
		}
		out, err := CollectSignalTenants(r.Context(), pool, signal, page)
		if err != nil {
			onError(err)
			http.Error(w, "census unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
