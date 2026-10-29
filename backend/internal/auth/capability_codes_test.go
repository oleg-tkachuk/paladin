package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/dpop"
	"github.com/oleg-tkachuk/limes/meter"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// Every code limes and its meter define has a row in capabilityCodes, so a code a
// release adds fails here rather than answering as the store failing; and
// every row names a code they define, so a renamed one cannot linger.
func TestCapabilityCodesCoverEveryLimesCode(t *testing.T) {
	defined := map[limes.Code]bool{}
	for _, c := range append(limes.Codes(), meter.Codes()...) {
		defined[c] = true
		if _, ok := capabilityCodes[c]; !ok {
			t.Errorf("limes code %q has no row in capabilityCodes", c)
		}
	}
	for c := range capabilityCodes {
		if !defined[c] {
			t.Errorf("capabilityCodes names %q, which limes does not define", c)
		}
	}
}

// A metering error answers by its code's row; one that carries no limes
// code, or a code with no row, is the store failing.
func TestCapabilityStatusFallsBackToUnavailable(t *testing.T) {
	if got := capabilityStatus(capabilityCodes, errors.New("store down")); got != capabilityUnknownCode {
		t.Errorf("an error with no limes code = %v, want %v", got, capabilityUnknownCode)
	}
	if got := capabilityStatus(map[limes.Code]connect.Code{}, limes.ErrRevoked); got != capabilityUnknownCode {
		t.Errorf("a code with no row = %v, want %v", got, capabilityUnknownCode)
	}
	if got := capabilityStatus(capabilityCodes, fmt.Errorf("wrapped: %w", limes.ErrRevoked)); got != connect.CodePermissionDenied {
		t.Errorf("a wrapped revocation = %v, want its row's %v", got, connect.CodePermissionDenied)
	}
}

// Unavailable is kept for what a retry can mend: a refusal of the
// capability is PermissionDenied as on the verify path, the server's own
// fault is Internal; those, a failing dependency and the deployment's fault
// are logged as errors, and a refusal is not.
func TestMeterErrorAnswersWhatARetryCanMend(t *testing.T) {
	cases := map[string]struct {
		err    error
		code   connect.Code
		logged bool
	}{
		"an operation the capability does not allow": {limes.ErrOpNotAllowed, connect.CodePermissionDenied, false},
		"a replayed proof":                           {dpop.ErrReplayed, connect.CodePermissionDenied, false},
		"a caveat violated":                          {limes.ErrCaveatViolation, connect.CodePermissionDenied, false},
		"a key-bound capability with no proof check": {limes.ErrProofUnsupported, connect.CodeFailedPrecondition, true},
		"a record already there":                     {limes.ErrAlreadyExists, connect.CodeAlreadyExists, false},
		"a refund beyond its charge":                 {meter.ErrRefundExceedsCharge, connect.CodeAborted, false},
		"a deleted tenant":                           {limes.ErrTenantDeleted, connect.CodeFailedPrecondition, false},
		"a misconfigured server":                     {limes.ErrInvalidConfig, connect.CodeInternal, true},
		"a call scoped to another tenant":            {limes.ErrScopeMismatch, connect.CodeInternal, true},
		"a store that repeats a cursor":              {limes.ErrCursorRepeated, connect.CodeUnavailable, true},
		"a store down":                               {errors.New("store down"), connect.CodeUnavailable, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			core, logs := observer.New(zapcore.InfoLevel)
			ctx := logger.WithContext(context.Background(), zap.New(core))
			if got := connect.CodeOf(meterError(ctx, tc.err)); got != tc.code {
				t.Errorf("code = %v, want %v", got, tc.code)
			}
			if got := logs.FilterLevelExact(zapcore.ErrorLevel).Len(); tc.logged && got != 1 || !tc.logged && got != 0 {
				t.Errorf("%d error lines logged, want logged = %v", got, tc.logged)
			}
		})
	}
}

// Verifying a capability and metering it answer one caller about one
// capability, so they answer every code alike: a spent copy limit is
// ResourceExhausted on both, a misconfigured server Internal on both, and a
// server-side failure keeps its internals from the caller on both.
func TestVerifyAndMeterAnswerAlike(t *testing.T) {
	const internals = "dial tcp 10.0.0.7:5432: connection refused"
	for _, err := range []error{
		meter.ErrCopyLimitExceeded,
		limes.ErrRequestLimitExceeded,
		limes.ErrRevoked,
		limes.ErrInvalidConfig,
		fmt.Errorf("%w: %s", limes.ErrInternal, internals),
		fmt.Errorf("store: %s", internals),
	} {
		ctx := logger.WithContext(context.Background(), zap.NewNop())
		verified, metered := verifyError(ctx, err), meterError(ctx, err)
		if connect.CodeOf(verified) != connect.CodeOf(metered) {
			t.Errorf("%v: verified %v, metered %v", err, connect.CodeOf(verified), connect.CodeOf(metered))
		}
		for name, answer := range map[string]error{"verified": verified, "metered": metered} {
			var ce *connect.Error
			if errors.As(answer, &ce) && strings.Contains(ce.Message(), internals) {
				t.Errorf("%v %s: the caller is told the server's internals: %q", err, name, ce.Message())
			}
		}
	}
	if got := connect.CodeOf(verifyError(context.Background(), meter.ErrCopyLimitExceeded)); got != connect.CodeResourceExhausted {
		t.Errorf("a spent copy limit at verification = %v, want %v", got, connect.CodeResourceExhausted)
	}
}
