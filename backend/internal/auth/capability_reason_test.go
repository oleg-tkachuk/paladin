package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"strings"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/dpop"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// wireReason is the ErrorInfo reason err carries in Paladin's domain, or ""
// when it carries none.
func wireReason(t *testing.T, err error) string {
	t.Helper()
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("%v is not a connect error", err)
	}
	for _, d := range ce.Details() {
		v, dErr := connectproto.UnmarshalErrorDetail(d)
		if dErr != nil {
			t.Fatalf("detail: %v", dErr)
		}
		if info, ok := v.(*errdetails.ErrorInfo); ok && info.GetDomain() == paladin.ErrorDomain {
			return info.GetReason()
		}
	}
	return ""
}

// A reason the module adds without a proto value would reach clients as no
// reason at all.
func TestEveryCapabilityReasonHasAnErrorReason(t *testing.T) {
	for _, r := range limes.Reasons() {
		if capabilityErrorReason(r) == commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED {
			t.Errorf("capability reason %q has no %s value in ErrorReason", r, capabilityReasonPrefix)
		}
	}
}

func TestCapabilityErrorCarriesTheReason(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		want      string
		limesCode string
	}{
		{"expired", fmt.Errorf("verify: %w", limes.ErrExpired), "ERROR_REASON_CAPABILITY_EXPIRED", string(limes.CodeExpired)},
		{"budget", limes.ErrBudgetExceeded, "ERROR_REASON_CAPABILITY_BUDGET_EXCEEDED", string(limes.CodeBudgetExceeded)},
		{"store failure", errors.New("connection reset"), "", ""},
		{"programming error", limes.ErrEffectConflict, "", string(limes.CodeEffectConflict)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := capabilityError(connect.CodePermissionDenied, tc.err)
			if got := wireReason(t, err); got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
			if got := wireLimesCode(t, err); got != tc.limesCode {
				t.Errorf("limes code = %q, want %q", got, tc.limesCode)
			}
		})
	}
}

// Every code limes and the meter define reaches the wire as itself, under
// the key both SDKs read it by, with or without a reason beside it.
func TestEveryLimesCodeReachesTheWire(t *testing.T) {
	codes := append(limes.Codes(), meter.Codes()...)
	if len(codes) == 0 {
		t.Fatal("limes defines no code")
	}
	for _, code := range codes {
		for _, reason := range []commonv1.ErrorReason{commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED, commonv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT} {
			info := capabilityErrorInfo(reason, code)
			if info.GetDomain() != paladin.ErrorDomain || info.GetMetadata()[paladin.ErrorMetadataLimesCode] != string(code) {
				t.Errorf("code %q with %v: ErrorInfo %v", code, reason, info)
			}
		}
	}
	if info := capabilityErrorInfo(commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED, ""); info != nil {
		t.Errorf("neither a reason nor a code: ErrorInfo %v, want none", info)
	}
}

// wireLimesCode is the limes code an error carries in its ErrorInfo, empty
// when it carries none.
func wireLimesCode(t *testing.T, err error) string {
	t.Helper()
	ce, ok := errors.AsType[*connect.Error](err)
	if !ok {
		t.Fatalf("%v is not a connect error", err)
	}
	for _, d := range ce.Details() {
		v, derr := connectproto.UnmarshalErrorDetail(d)
		if derr != nil {
			continue
		}
		if info, ok := v.(*errdetails.ErrorInfo); ok && info.GetDomain() == paladin.ErrorDomain {
			return info.GetMetadata()[paladin.ErrorMetadataLimesCode]
		}
	}
	return ""
}

// The reason reaches the wire from the paths a handler takes.
func TestCapabilityRefusalsCarryTheirReason(t *testing.T) {
	m, usage := newMeteredMinter(t)
	v, _ := m.Mint(t, limes.Capability{Caveats: limes.Caveats{
		Ops: []limes.Op{limes.OpGet}, Budget: limes.BudgetCeiling{Max: limes.NanosPerUnit, UnitCode: captest.Unit},
	}})
	ctx := WithChargeStore(WithCapability(context.Background(), v), usage)

	err := AssertCapabilityOp(ctx, limes.OpPut, "object://acme/foo")
	if got := wireReason(t, err); got != "ERROR_REASON_CAPABILITY_OP_NOT_ALLOWED" {
		t.Errorf("op refusal reason = %q", got)
	}
	err = ChargeCapability(ctx, 2*limes.NanosPerUnit)
	if got := wireReason(t, err); got != "ERROR_REASON_CAPABILITY_BUDGET_EXCEEDED" {
		t.Errorf("budget refusal reason = %q", got)
	}
}

// A failed verification is the caller's only when it refuses the capability.
// Keys the verifier cannot fetch, and a failure that refuses nothing, are the
// server's: Unavailable, so the caller retries, with the cause logged and
// kept from the caller.
func TestVerifyErrorTellsARefusalFromAnOutage(t *testing.T) {
	const internals = "dial tcp 10.0.0.7:5432: connection refused"
	cases := map[string]struct {
		err      error
		code     connect.Code
		logged   bool
		withheld bool
	}{
		"a revoked capability": {err: limes.ErrRevoked, code: connect.CodePermissionDenied},
		"a bad proof":          {err: dpop.ErrReplayed, code: connect.CodePermissionDenied},
		"keys not fetched":     {err: fmt.Errorf("%w: %s", jwks.ErrUnavailable, internals), code: connect.CodeUnavailable, logged: true},
		"a revocation store down": {
			err: fmt.Errorf("limes: revocation lookup: %s", internals), code: connect.CodeUnavailable, logged: true, withheld: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			core, logs := observer.New(zapcore.InfoLevel)
			ctx := logger.WithContext(context.Background(), zap.New(core))
			err := verifyError(ctx, tc.err)
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("code = %v, want %v", connect.CodeOf(err), tc.code)
			}
			if !errors.Is(err, tc.err) {
				t.Error("the cause is not kept")
			}
			var ce *connect.Error
			if errors.As(err, &ce) && tc.withheld && strings.Contains(ce.Message(), internals) {
				t.Errorf("the caller is told the server's internals: %q", ce.Message())
			}
			got := logs.FilterLevelExact(zapcore.ErrorLevel).Len()
			if tc.logged && got != 1 || !tc.logged && got != 0 {
				t.Errorf("%d error lines logged, want logged = %v", got, tc.logged)
			}
		})
	}
}

// Every metering refusal is answered by its limes family, so one no retry
// mends never reads as one to retry; the ones that refuse the capability
// carry their reason. A store failure alone is Unavailable.
func TestMeterErrorAnswersEachFamily(t *testing.T) {
	cases := map[error]connect.Code{
		limes.ErrRequestLimitExceeded:               connect.CodeResourceExhausted,
		meter.ErrCopyLimitExceeded:                  connect.CodeResourceExhausted,
		limes.ErrBudgetExceeded:                     connect.CodeResourceExhausted,
		limes.ErrTenantBudgetExceeded:               connect.CodeResourceExhausted,
		limes.ErrRevoked:                            connect.CodePermissionDenied,
		limes.ErrTenantErased:                       connect.CodePermissionDenied,
		limes.ErrExpired:                            connect.CodePermissionDenied,
		limes.ErrInvalidAmount:                      connect.CodeInvalidArgument,
		limes.ErrUnitCodeMismatch:                   connect.CodeFailedPrecondition,
		meter.ErrCounterOverflow:                    connect.CodeFailedPrecondition,
		limes.ErrNotFound:                           connect.CodeFailedPrecondition,
		meter.ErrSettleConflict:                     connect.CodeAborted,
		meter.ErrExternalRefConflict:                connect.CodeAborted,
		errors.New("store down"):                    connect.CodeUnavailable,
		fmt.Errorf("wrapped: %w", limes.ErrRevoked): connect.CodePermissionDenied,
	}
	for err, want := range cases {
		if got := connect.CodeOf(meterError(context.Background(), err)); got != want {
			t.Errorf("meterError(%v) = %v, want %v", err, got, want)
		}
	}
	// A revoked capability is told so, by reason, as a verifier tells it.
	var ce *connect.Error
	if !errors.As(meterError(context.Background(), limes.ErrTenantErased), &ce) || len(ce.Details()) == 0 {
		t.Error("an erased tenant's refusal carries no reason")
	}
}
