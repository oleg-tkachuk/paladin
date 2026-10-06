package platformstats

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func serveSignal(t *testing.T, pool *pgxpool.Pool, query string) (int, []error) {
	t.Helper()
	var errs []error
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, SignalTenantsPath+"?"+query, nil)
	SignalTenantsHandler(pool, func(err error) { errs = append(errs, err) })(rec, req)
	return rec.Code, errs
}

// Without the BYPASSRLS pool every count would read as zero; the endpoint
// says it cannot answer instead, and says why.
func TestSignalTenantsHandler_NoPoolIsUnavailable(t *testing.T) {
	code, errs := serveSignal(t, nil, SignalQuery(SignalQuotaAtLimit, TenantPage{}).Encode())
	if code != http.StatusServiceUnavailable || len(errs) != 1 {
		t.Errorf("status %d, %d errors reported; want 503 and one", code, len(errs))
	}
}

// An unknown signal is the caller's mistake and is refused before any query.
func TestSignalTenantsHandler_UnknownSignalIsBadRequest(t *testing.T) {
	// A pool that never connects: the handler must not reach it.
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none")
	if err != nil {
		t.Fatalf("lazy pool: %v", err)
	}
	defer pool.Close()

	code, errs := serveSignal(t, pool, "signal=objects")
	if code != http.StatusBadRequest || len(errs) != 0 {
		t.Errorf("status %d, %d errors reported; want 400 and none", code, len(errs))
	}
}
