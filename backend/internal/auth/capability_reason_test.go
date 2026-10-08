package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/paladin/capability"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
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
	for _, r := range capability.Reasons() {
		if capabilityErrorReason(r) == commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED {
			t.Errorf("capability reason %q has no %s value in ErrorReason", r, capabilityReasonPrefix)
		}
	}
}

func TestCapabilityErrorCarriesTheReason(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"expired", fmt.Errorf("verify: %w", capability.ErrExpired), "ERROR_REASON_CAPABILITY_EXPIRED"},
		{"budget", capability.ErrBudgetExceeded, "ERROR_REASON_CAPABILITY_BUDGET_EXCEEDED"},
		{"store failure", errors.New("connection reset"), ""},
		{"programming error", capability.ErrEffectConflict, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wireReason(t, capabilityError(connect.CodePermissionDenied, tc.err)); got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

// The reason reaches the wire from the paths a handler takes.
func TestCapabilityRefusalsCarryTheirReason(t *testing.T) {
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{
		Ops: []capability.Op{capability.OpGet}, MaxBudgetAmount: capability.MustParseAmount("1"),
	}}
	ctx := WithChargeStore(WithCapability(context.Background(), cap), newFakeUsage())

	err := AssertCapabilityOp(ctx, capability.OpPut, "object://acme/foo")
	if got := wireReason(t, err); got != "ERROR_REASON_CAPABILITY_OP_NOT_ALLOWED" {
		t.Errorf("op refusal reason = %q", got)
	}
	err = ChargeCapability(ctx, 2*capability.NanosPerUnit, "")
	if got := wireReason(t, err); got != "ERROR_REASON_CAPABILITY_BUDGET_EXCEEDED" {
		t.Errorf("budget refusal reason = %q", got)
	}
}
