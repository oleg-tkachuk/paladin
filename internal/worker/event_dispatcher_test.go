package worker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

type fakeStore struct {
	subs []admindomain.EventSubscription
}

func (f *fakeStore) List(_ context.Context, _ admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	return f.subs, "", nil
}

func TestDispatchHTTP_Success(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())

	var (
		mu       sync.Mutex
		bodies   [][]byte
		sigs     []string
		typeHdrs []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		sigs = append(sigs, r.Header.Get("X-PALADIN-Signature"))
		typeHdrs = append(typeHdrs, r.Header.Get("X-PALADIN-Event-Type"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg, _ := json.Marshal(map[string]any{
		"url":                srv.URL,
		"signing_secret_ref": "test-secret",
	})
	d := &Dispatcher{
		Store: &fakeStore{
			subs: []admindomain.EventSubscription{{
				SubscriptionID: uuid.Must(uuid.NewV7()),
				TenantID:       tenantID,
				SinkKind:       "http",
				SinkConfig:     cfg,
			}},
		},
		HTTPClient:  srv.Client(),
		MaxAttempts: 1,
	}
	n, err := d.Dispatch(context.Background(), tenantID.String(), Event{
		Type:     "object.created",
		At:       time.Now().UTC(),
		TenantID: tenantID.String(),
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if n != 1 {
		t.Errorf("delivered: got %d want 1", n)
	}
	if len(bodies) != 1 {
		t.Fatalf("bodies: got %d want 1", len(bodies))
	}
	if len(sigs) != 1 || sigs[0] == "" {
		t.Errorf("missing signature header: %+v", sigs)
	}
	if typeHdrs[0] != "object.created" {
		t.Errorf("X-PALADIN-Event-Type: got %q", typeHdrs[0])
	}
}

func TestDispatchHTTP_RetryThenFail(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	cfg, _ := json.Marshal(map[string]any{"url": srv.URL})
	d := &Dispatcher{
		Store: &fakeStore{
			subs: []admindomain.EventSubscription{{
				SubscriptionID: uuid.Must(uuid.NewV7()),
				TenantID:       tenantID,
				SinkKind:       "http",
				SinkConfig:     cfg,
			}},
		},
		HTTPClient:  srv.Client(),
		MaxAttempts: 2,
		BaseBackoff: 1 * time.Millisecond,
	}
	n, err := d.Dispatch(context.Background(), tenantID.String(), Event{Type: "object.created"})
	if err != nil {
		t.Fatalf("Dispatch returned error %v", err)
	}
	if n != 0 {
		t.Errorf("delivered: got %d want 0 (all attempts 5xx)", n)
	}
}

func TestDispatchSkipsDisabled(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	cfg, _ := json.Marshal(map[string]any{"url": "http://unused"})
	d := &Dispatcher{
		Store: &fakeStore{
			subs: []admindomain.EventSubscription{{
				SubscriptionID: uuid.Must(uuid.NewV7()),
				TenantID:       tenantID,
				SinkKind:       "http",
				SinkConfig:     cfg,
				Disabled:       true,
			}},
		},
	}
	n, err := d.Dispatch(context.Background(), tenantID.String(), Event{Type: "object.created"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("delivered: got %d want 0 (disabled)", n)
	}
}

func TestDispatchFilterMatch(t *testing.T) {
	tenantID := uuid.Must(uuid.NewV7())
	cfg, _ := json.Marshal(map[string]any{"url": "http://unused"})
	d := &Dispatcher{
		Store: &fakeStore{
			subs: []admindomain.EventSubscription{{
				SubscriptionID: uuid.Must(uuid.NewV7()),
				TenantID:       tenantID,
				SinkKind:       "http",
				SinkConfig:     cfg,
				CELFilter:      "object.deleted", // exact-match in v2
			}},
		},
	}
	n, _ := d.Dispatch(context.Background(), tenantID.String(), Event{Type: "object.created"})
	if n != 0 {
		t.Errorf("delivered: got %d want 0 (filter mismatch)", n)
	}
}
