package tenanth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
)

// An absent default binding must answer NotFound.
//
// The sentinel has been registered to CodeNotFound since the error table was
// written, but GetDefaultBinding returned the repo's error without calling the
// mapper, so it left as Unknown — connect's default for an error it does not
// recognise. The console reads exactly this code to choose between "no default
// route set" and a real failure, so every tenant without a binding was met
// with an error toast instead of the empty state the page already had. Found
// 2026-08-27 by the first test ever pointed at that page.
func TestGetDefaultBindingAbsentBindingIsNotFound(t *testing.T) {
	tid := uuid.New()
	h := NewHandler(&fakeRepo{getDefBindingFn: func(context.Context, uuid.UUID) (DefaultBinding, error) {
		return DefaultBinding{}, ErrNotFound
	}}, allow())

	_, err := h.GetDefaultBinding(principalCtx(tid), tid)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound — Unknown is indistinguishable from a server fault", connect.CodeOf(err))
	}
	// The in-process callers branch on the sentinel rather than the code:
	// CreateCollection turns it into the bare-name refusal, and the
	// collection-route lister into "no bare aliases". Wrapping must not break
	// either.
	if !errors.Is(err, ErrNotFound) {
		t.Error("the sentinel no longer matches through the wrap — bare-name creation would stop refusing")
	}
	// And it must say what is actually missing. The registered sentinel reads
	// "tenant not found", which is the wrong noun here: the tenant exists.
	if !strings.Contains(err.Error(), "no default binding") {
		t.Errorf("message %q does not say the BINDING is missing", err.Error())
	}
}

// A genuine repo failure stays distinguishable from an absent binding.
func TestGetDefaultBindingRealFailureIsNotNotFound(t *testing.T) {
	tid := uuid.New()
	h := NewHandler(&fakeRepo{getDefBindingFn: func(context.Context, uuid.UUID) (DefaultBinding, error) {
		return DefaultBinding{}, errors.New("connection refused")
	}}, allow())

	_, err := h.GetDefaultBinding(principalCtx(tid), tid)
	if connect.CodeOf(err) == connect.CodeNotFound {
		t.Fatal("a database failure was reported as an absent binding — the console would render an empty state over an outage")
	}
}
