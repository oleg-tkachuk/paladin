package auth

import (
	"net/http"
	"testing"
)

// A capability-only request has no Authorization header at all, so the JWT gate
// has to step aside for it or it never reaches the interceptor that can
// authenticate it.
func TestInterceptorSkipTokensAndCapabilities_DefersACapability(t *testing.T) {
	a := &authInterceptor{skipAPITokens: true, skipCapabilities: true}

	h := http.Header{}
	h.Set(HeaderCapability, "eyJ0eXAiOiJKV1QifQ.e30.sig")
	if !a.hasCapability(h) {
		t.Error("X-PALADIN-Capability must be recognised")
	}

	h2 := http.Header{}
	h2.Set("Authorization", "Capability eyJ0eXAiOiJKV1QifQ.e30.sig")
	if !a.hasCapability(h2) {
		t.Error("Authorization: Capability must be recognised")
	}

	if a.hasCapability(http.Header{}) {
		t.Error("a request with neither must NOT be deferred — auth stays mandatory")
	}
}

// The plain gate keeps refusing capability-only requests, so enabling this is a
// per-plane decision rather than a global loosening.
func TestInterceptor_WithoutTheFlagDoesNotDeferCapabilities(t *testing.T) {
	a := &authInterceptor{skipAPITokens: true}

	h := http.Header{}
	h.Set(HeaderCapability, "eyJ0eXAiOiJKV1QifQ.e30.sig")
	if a.hasCapability(h) {
		t.Error("a plane that did not opt in must not defer")
	}
}
