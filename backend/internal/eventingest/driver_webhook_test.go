package eventingest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Webhook driver-level tests focus on the HTTP front-door:
// HMAC verification, content-type rejection, body cap.
// Source-parsing happens in source_seaweedfs_test.go.

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// stubSource always succeeds; the driver test doesn't care about
// payload semantics.
type stubSource struct{ called bool }

func (s *stubSource) Name() string { return "stub" }
func (s *stubSource) Parse(_ []byte, _ string) (CloudEvent, error) {
	s.called = true
	return CloudEvent{
		SpecVersion: "1.0",
		Type:        EventTypeUploaded,
		Source:      "stub://t",
		ID:          "stub-evt-1",
	}, nil
}

func TestVerifyHMAC_Match(t *testing.T) {
	body := []byte(`{"key":"x"}`)
	if !verifyHMAC(body, "secret", sign(body, "secret")) {
		t.Error("HMAC must match")
	}
}

func TestVerifyHMAC_PrefixedHeader(t *testing.T) {
	body := []byte(`{"key":"x"}`)
	header := "sha256=" + sign(body, "secret")
	if !verifyHMAC(body, "secret", header) {
		t.Error("sha256= prefixed header must verify")
	}
}

func TestVerifyHMAC_MismatchRejected(t *testing.T) {
	body := []byte(`{"key":"x"}`)
	if verifyHMAC(body, "secret", sign(body, "wrong-secret")) {
		t.Error("HMAC mismatch must be rejected")
	}
}

func TestVerifyHMAC_EmptyHeaderRejected(t *testing.T) {
	if verifyHMAC([]byte("body"), "secret", "") {
		t.Error("empty signature header must be rejected")
	}
}

func TestWebhookHandler_HMACGate(t *testing.T) {
	src := &stubSource{}
	d := &WebhookDriver{
		Sources:      map[string]Source{"/webhook/stub": src},
		SharedSecret: "secret",
		SignatureHdr: "X-PALADIN-Signature",
		MaxBodyBytes: 1024,
	}
	delivered := false
	h := d.handler(src, func(_ context.Context, _ CloudEvent) error {
		delivered = true
		return nil
	})

	body := []byte(`{"k":"v"}`)

	// Wrong signature → 401 + handler not called.
	r := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/webhook/stub", bytes.NewReader(body))
	req.Header.Set("X-PALADIN-Signature", "deadbeef")
	h(r, req)
	if r.Code != http.StatusUnauthorized {
		t.Errorf("bad sig got %d, want 401", r.Code)
	}
	if delivered {
		t.Error("handler must not run when HMAC fails")
	}

	// Correct signature → 200 + handler called.
	r = httptest.NewRecorder()
	req = httptest.NewRequestWithContext(t.Context(), "POST", "/webhook/stub", bytes.NewReader(body))
	req.Header.Set("X-PALADIN-Signature", sign(body, "secret"))
	h(r, req)
	if r.Code != http.StatusOK {
		t.Errorf("good sig got %d, want 200", r.Code)
	}
	if !delivered {
		t.Error("handler must run after HMAC pass")
	}
}

func TestWebhookHandler_MethodNotAllowed(t *testing.T) {
	src := &stubSource{}
	d := &WebhookDriver{
		Sources:      map[string]Source{"/webhook/stub": src},
		MaxBodyBytes: 1024,
	}
	h := d.handler(src, func(_ context.Context, _ CloudEvent) error { return nil })

	r := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/webhook/stub", nil)
	h(r, req)
	if r.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET got %d, want 405", r.Code)
	}
}
