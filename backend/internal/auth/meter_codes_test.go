package auth

import (
	"context"
	"errors"
	"fmt"
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

// Every code limes and its meter define has a row in meterCodes, so a code a
// release adds fails here rather than answering as the store failing; and
// every row names a code they define, so a renamed one cannot linger.
func TestMeterCodesCoverEveryLimesCode(t *testing.T) {
	defined := map[limes.Code]bool{}
	for _, c := range append(limes.Codes(), meter.Codes()...) {
		defined[c] = true
		if _, ok := meterCodes[c]; !ok {
			t.Errorf("limes code %q has no row in meterCodes", c)
		}
	}
	for c := range meterCodes {
		if !defined[c] {
			t.Errorf("meterCodes names %q, which limes does not define", c)
		}
	}
}

// A metering error answers by its code's row; one that carries no limes
// code, or a code with no row, is the store failing.
func TestMeterStatusFallsBackToUnavailable(t *testing.T) {
	if got := meterStatus(meterCodes, errors.New("store down")); got != meterUnknownCode {
		t.Errorf("an error with no limes code = %v, want %v", got, meterUnknownCode)
	}
	if got := meterStatus(map[limes.Code]connect.Code{}, limes.ErrRevoked); got != meterUnknownCode {
		t.Errorf("a code with no row = %v, want %v", got, meterUnknownCode)
	}
	if got := meterStatus(meterCodes, fmt.Errorf("wrapped: %w", limes.ErrRevoked)); got != connect.CodePermissionDenied {
		t.Errorf("a wrapped revocation = %v, want its row's %v", got, connect.CodePermissionDenied)
	}
}

// Unavailable is kept for what a retry can mend: a refusal of the
// capability is PermissionDenied as on the verify path, the server's own
// fault is Internal, and only those two answers are logged as errors.
func TestMeterErrorAnswersWhatARetryCanMend(t *testing.T) {
	cases := map[string]struct {
		err    error
		code   connect.Code
		logged bool
	}{
		"an operation the capability does not allow": {limes.ErrOpNotAllowed, connect.CodePermissionDenied, false},
		"a replayed proof":                           {dpop.ErrReplayed, connect.CodePermissionDenied, false},
		"a caveat violated":                          {limes.ErrCaveatViolation, connect.CodePermissionDenied, false},
		"a key-bound capability with no proof check": {limes.ErrProofUnsupported, connect.CodeFailedPrecondition, false},
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
