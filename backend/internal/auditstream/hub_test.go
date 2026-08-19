package auditstream

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
)

func payload(tenantID, action string) string {
	b, _ := json.Marshal(Entry{
		EntryID:       uuid.NewString(),
		At:            "2026-07-01T00:00:00.000000Z",
		ActorSubject:  "op-1",
		ActorTenantID: tenantID,
		ActorAudience: "paladin-admin",
		Action:        action,
		ResourceName:  "tenants/" + tenantID,
	})
	return string(b)
}

func TestHub_DispatchRoutesPerTenant(t *testing.T) {
	h := NewHub(nil)
	tA, tB := uuid.NewString(), uuid.NewString()
	chA, cancelA := h.Subscribe(tA)
	defer cancelA()
	chB, cancelB := h.Subscribe(tB)
	defer cancelB()

	h.Dispatch(payload(tA, "admin.CreateBucket"))

	select {
	case e := <-chA:
		if e.Action != "admin.CreateBucket" || e.ActorTenantID != tA {
			t.Errorf("subscriber A got %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber A never received the event")
	}
	select {
	case e := <-chB:
		t.Errorf("tenant-B subscriber must not see tenant-A events, got %+v", e)
	default:
	}
}

func TestHub_DispatchDropsUndecodableAndTenantless(t *testing.T) {
	h := NewHub(nil)
	ch, cancel := h.Subscribe(uuid.NewString())
	defer cancel()
	h.Dispatch("{not json")
	h.Dispatch(payload("", "iam.Login")) // NULL actor_tenant_id → routed nowhere
	select {
	case e := <-ch:
		t.Errorf("unexpected delivery: %+v", e)
	default:
	}
}

func TestHub_SlowConsumerDropsInsteadOfBlocking(t *testing.T) {
	h := NewHub(nil)
	tid := uuid.NewString()
	_, cancel := h.Subscribe(tid) // never drained — fills the 64-slot buffer
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ { // > buffer size
			h.Dispatch(payload(tid, "spam"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch blocked on a slow consumer")
	}
}

func TestHub_CancelUnsubscribesAndCloses(t *testing.T) {
	h := NewHub(nil)
	tid := uuid.NewString()
	ch, cancel := h.Subscribe(tid)
	cancel()
	cancel() // idempotent
	if _, open := <-ch; open {
		t.Error("channel must be closed after cancel")
	}
	h.Dispatch(payload(tid, "post-cancel")) // must not panic on the closed ch
}

// ─── SSE handler ─────────────────────────────────────────────────────────────

type stubVerifier struct {
	p   *auth.Principal
	err error
}

func (s stubVerifier) Verify(context.Context, string) (*auth.Principal, error) {
	return s.p, s.err
}

func TestSSEHandler_AuthGates(t *testing.T) {
	h := NewHub(nil)
	tid := uuid.New()
	srv := httptest.NewServer(h.SSEHandler(stubVerifier{p: &auth.Principal{TenantID: tid}}))
	defer srv.Close()

	// No bearer → 401.
	noBearerReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	res, err := http.DefaultClient.Do(noBearerReq)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no-bearer status = %d, want 401", res.StatusCode)
	}

	// Bad token → 401.
	badSrv := httptest.NewServer(h.SSEHandler(stubVerifier{err: errors.New("bad")}))
	defer badSrv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, badSrv.URL, nil)
	req.Header.Set("Authorization", "Bearer nope")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad-token status = %d, want 401", res.StatusCode)
	}

	// Tenant-less principal → 403.
	nilSrv := httptest.NewServer(h.SSEHandler(stubVerifier{p: &auth.Principal{}}))
	defer nilSrv.Close()
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, nilSrv.URL, nil)
	req.Header.Set("Authorization", "Bearer x")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("tenant-less status = %d, want 403", res.StatusCode)
	}

	// POST → 405.
	postReq, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, nil)
	res, err = http.DefaultClient.Do(postReq)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", res.StatusCode)
	}
}

func TestSSEHandler_StreamsTenantEvents(t *testing.T) {
	h := NewHub(nil)
	tid := uuid.New()
	srv := httptest.NewServer(h.SSEHandler(stubVerifier{p: &auth.Principal{TenantID: tid}}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer ok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	scanner := bufio.NewScanner(res.Body)
	// First frame is the ": connected" liveness comment.
	if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), ": connected") {
		t.Fatalf("first line = %q, want ': connected'", scanner.Text())
	}

	// The subscription is registered before the connected comment is written,
	// so dispatching now is race-free.
	h.Dispatch(payload(tid.String(), "admin.UpdateTenant"))
	h.Dispatch(payload(uuid.NewString(), "other.Tenant")) // must NOT arrive

	var dataLine string
	deadline := time.After(3 * time.Second)
	lineCh := make(chan string, 8)
	go func() {
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
	}()
scan:
	for {
		select {
		case l := <-lineCh:
			if strings.HasPrefix(l, "data: ") {
				dataLine = strings.TrimPrefix(l, "data: ")
				break scan
			}
		case <-deadline:
			t.Fatal("no data frame within deadline")
		}
	}
	var e Entry
	if err := json.Unmarshal([]byte(dataLine), &e); err != nil {
		t.Fatalf("data frame not JSON: %v (%q)", err, dataLine)
	}
	if e.Action != "admin.UpdateTenant" || e.ActorTenantID != tid.String() {
		t.Errorf("streamed entry = %+v, want the tenant's own event", e)
	}
	// Ensure the other tenant's event did not sneak into the stream buffer.
	select {
	case l := <-lineCh:
		if strings.Contains(l, "other.Tenant") {
			t.Errorf("cross-tenant event leaked into the stream: %q", l)
		}
	case <-time.After(200 * time.Millisecond):
	}
}
