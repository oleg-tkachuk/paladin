package health

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSnapshotGate covers the /system/health.json auth gate: open when no
// token is configured (dev), 401 without/with-wrong token when set, 200 with
// the correct token via either accepted header.
func TestSnapshotGate(t *testing.T) {
	t.Parallel()

	call := func(h *Handler, set func(*http.Request)) int {
		req := httptest.NewRequest(http.MethodGet, "/system/health.json", nil)
		if set != nil {
			set(req)
		}
		rec := httptest.NewRecorder()
		h.serveSnapshot(rec, req)
		return rec.Code
	}

	t.Run("open when unset", func(t *testing.T) {
		t.Parallel()
		if code := call(&Handler{}, nil); code != http.StatusOK {
			t.Fatalf("want 200 (open), got %d", code)
		}
	})

	t.Run("401 without token when set", func(t *testing.T) {
		t.Parallel()
		if code := call(&Handler{SnapshotToken: "s3cret"}, nil); code != http.StatusUnauthorized {
			t.Fatalf("want 401, got %d", code)
		}
	})

	t.Run("401 with wrong token", func(t *testing.T) {
		t.Parallel()
		code := call(&Handler{SnapshotToken: "s3cret"}, func(r *http.Request) {
			r.Header.Set("X-Health-Token", "nope")
		})
		if code != http.StatusUnauthorized {
			t.Fatalf("want 401, got %d", code)
		}
	})

	t.Run("200 with X-Health-Token", func(t *testing.T) {
		t.Parallel()
		code := call(&Handler{SnapshotToken: "s3cret"}, func(r *http.Request) {
			r.Header.Set("X-Health-Token", "s3cret")
		})
		if code != http.StatusOK {
			t.Fatalf("want 200, got %d", code)
		}
	})

	t.Run("200 with Bearer Authorization", func(t *testing.T) {
		t.Parallel()
		code := call(&Handler{SnapshotToken: "s3cret"}, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer s3cret")
		})
		if code != http.StatusOK {
			t.Fatalf("want 200, got %d", code)
		}
	})
}
