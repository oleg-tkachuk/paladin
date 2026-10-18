package apiutil

import (
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/limes"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// reasonOf is the ErrorInfo reason err carries, "" for none.
func reasonOf(t *testing.T, err error) string {
	t.Helper()
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return ""
	}
	for _, d := range cerr.Details() {
		v, verr := connectproto.UnmarshalErrorDetail(d)
		if verr != nil {
			t.Fatalf("detail does not decode: %v", verr)
		}
		if info, ok := v.(*errdetails.ErrorInfo); ok {
			if info.GetDomain() != paladin.ErrorDomain {
				t.Errorf("ErrorInfo domain = %q, want %q", info.GetDomain(), paladin.ErrorDomain)
			}
			return info.GetReason()
		}
	}
	return ""
}

func TestMapError(t *testing.T) {
	registered := errors.New("registered sentinel")
	RegisterError(registered, connect.CodeResourceExhausted, commonv1.ErrorReason_ERROR_REASON_CONFLICT)

	cases := []struct {
		name   string
		err    error
		want   connect.Code // 0 means "expect nil error"
		reason commonv1.ErrorReason
	}{
		{"nil", nil, 0, 0},
		{"canonical not-found", ErrNotFound, connect.CodeNotFound, commonv1.ErrorReason_ERROR_REASON_NOT_FOUND},
		{"wrapped conflict", fmt.Errorf("update: %w", ErrConflict), connect.CodeAborted, commonv1.ErrorReason_ERROR_REASON_VERSION_CONFLICT},
		{"failed precondition", ErrFailedPrecondition, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_FAILED_PRECONDITION},
		{"registered sentinel wrapped", fmt.Errorf("x: %w", registered), connect.CodeResourceExhausted, commonv1.ErrorReason_ERROR_REASON_CONFLICT},
		{"unknown → internal, no reason", errors.New("boom"), connect.CodeInternal, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED},
		{
			"already a connect error passes through",
			connect.NewError(connect.CodeUnavailable, "down"),
			connect.CodeUnavailable,
			commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MapError(tc.err)
			if tc.want == 0 {
				if got != nil {
					t.Fatalf("want nil, got %v", got)
				}
				return
			}
			if connect.CodeOf(got) != tc.want {
				t.Errorf("MapError(%v) code = %v, want %v", tc.err, connect.CodeOf(got), tc.want)
			}
			want := ""
			if tc.reason != commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED {
				want = tc.reason.String()
			}
			if r := reasonOf(t, got); r != want {
				t.Errorf("MapError(%v) reason = %q, want %q", tc.err, r, want)
			}
		})
	}
}

func TestRegisterErrorRefusesNoReason(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a sentinel registered with no reason was accepted")
		}
	}()
	RegisterError(errors.New("x"), connect.CodeInternal, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
}

// Every sentinel the server maps carries a reason a client can switch on.
func TestEveryMappedSentinelHasAReason(t *testing.T) {
	check := func(table map[error]mapping) {
		for sentinel, m := range table {
			if m.reason == commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED {
				t.Errorf("%v maps to %v with no reason", sentinel, m.code)
			}
		}
	}
	check(canonical)
	registryMu.RLock()
	defer registryMu.RUnlock()
	check(registry)
}

// A store call scoped to another tenant than its request names is a wiring
// bug in the server, not the client's request: Internal, never NotFound or
// InvalidArgument, which would send the client to fix what is not broken.
func TestMapErrorScopeMismatchIsInternal(t *testing.T) {
	err := MapError(fmt.Errorf("insert: %w", limes.ErrScopeMismatch))
	if got := connect.CodeOf(err); got != connect.CodeInternal {
		t.Errorf("code = %v, want %v", got, connect.CodeInternal)
	}
}

type countedErr struct{ n string }

func (e countedErr) Error() string                    { return "counted: " + e.n }
func (e countedErr) Is(target error) bool             { return target == ErrFailedPrecondition }
func (e countedErr) ErrorMetadata() map[string]string { return map[string]string{"count": e.n} }

// What a MetadataError says beyond its reason reaches the ErrorInfo; an error
// that says nothing more carries no metadata.
func TestMapError_Metadata(t *testing.T) {
	metadataOfMapped := func(err error) map[string]string {
		var cerr *connect.Error
		if !errors.As(MapError(err), &cerr) {
			t.Fatalf("%v did not map to a connect error", err)
		}
		for _, d := range cerr.Details() {
			v, verr := connectproto.UnmarshalErrorDetail(d)
			if verr != nil {
				t.Fatalf("detail does not decode: %v", verr)
			}
			if info, ok := v.(*errdetails.ErrorInfo); ok {
				return info.GetMetadata()
			}
		}
		t.Fatalf("no ErrorInfo on %v", err)
		return nil
	}
	if got := metadataOfMapped(fmt.Errorf("wrapped: %w", countedErr{"3"})); got["count"] != "3" {
		t.Errorf("metadata = %v, want count=3", got)
	}
	if got := metadataOfMapped(ErrFailedPrecondition); len(got) != 0 {
		t.Errorf("metadata = %v, want none", got)
	}
}
