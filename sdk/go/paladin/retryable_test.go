package paladin

import (
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/oleg-tkachuk/limes"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// withLimesCode is a Connect error of code carrying limesCode in the
// server's ErrorInfo, as the server answers a refusal of the capability.
func withLimesCode(t *testing.T, code connect.Code, limesCode limes.Code) *connect.Error {
	t.Helper()
	info := &errdetails.ErrorInfo{Domain: ErrorDomain, Metadata: map[string]string{ErrorMetadataLimesCode: string(limesCode)}}
	detail, err := connectproto.NewErrorDetail(info)
	if err != nil {
		t.Fatal(err)
	}
	return connect.NewError(code, "refused").WithDetail(detail)
}

// A limit that something else can lift is retried; one limes holds
// permanent stands for as long as the capability does, and is not.
func TestDefaultRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"unavailable", connect.NewError(connect.CodeUnavailable, "down"), true},
		{"exhausted, no code", connect.NewError(connect.CodeResourceExhausted, "slow down"), true},
		{"a budget, which frees", withLimesCode(t, connect.CodeResourceExhausted, limes.CodeBudgetExceeded), true},
		{"a tenant budget, which frees", withLimesCode(t, connect.CodeResourceExhausted, limes.CodeTenantBudgetExceeded), true},
		{"a request limit, permanent", withLimesCode(t, connect.CodeResourceExhausted, limes.CodeRequestLimitExceeded), false},
		{"a copy limit, permanent", withLimesCode(t, connect.CodeResourceExhausted, limes.CodeCopyLimitExceeded), false},
		{"a code limes does not know", withLimesCode(t, connect.CodeResourceExhausted, "from_a_newer_server"), true},
		{"permission denied", connect.NewError(connect.CodePermissionDenied, "no"), false},
		{"not a connect error", errors.New("x"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DefaultRetryable(tc.err); got != tc.want {
				t.Errorf("DefaultRetryable = %v, want %v", got, tc.want)
			}
		})
	}
}
