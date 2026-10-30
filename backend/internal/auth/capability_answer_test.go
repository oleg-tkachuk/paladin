package auth

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// An operation answers a limes error by its own row first, then by
// capabilityCodes, then as a dependency failing; a row with a reason names
// it, a row without keeps the refusal's own.
func TestAnswerCapabilityErrorTakesTheOperationsRowFirst(t *testing.T) {
	errStore := errors.New(`pgstore: insert: ERROR: violates foreign key constraint "capability_records_fkey" (SQLSTATE 23503)`)
	except := Exceptions{
		limes.CodeUnknownTenant: {Code: connect.CodeNotFound, Reason: commonv1.ErrorReason_ERROR_REASON_TENANT_NOT_FOUND},
		limes.CodeRevoked:       {Code: connect.CodeFailedPrecondition},
	}
	cases := []struct {
		name   string
		err    error
		except Exceptions
		code   connect.Code
		reason commonv1.ErrorReason
	}{
		{"a row with its reason", fmt.Errorf("%w: t1", limes.ErrUnknownTenant), except, connect.CodeNotFound, commonv1.ErrorReason_ERROR_REASON_TENANT_NOT_FOUND},
		{"a row keeping the refusal's reason", limes.ErrRevoked, except, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_CAPABILITY_REVOKED},
		{"no row: the table's", fmt.Errorf("%w: t1", limes.ErrUnknownTenant), nil, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED},
		{"no row: a refusal as verified", limes.ErrRevoked, nil, connect.CodePermissionDenied, commonv1.ErrorReason_ERROR_REASON_CAPABILITY_REVOKED},
		{"no code: the store failing", errStore, except, connect.CodeUnavailable, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := AnswerCapabilityError(tc.err, tc.except)
			if got := connect.CodeOf(err); got != tc.code {
				t.Fatalf("code = %v, want %v (%v)", got, tc.code, err)
			}
			want := ""
			if tc.reason != commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED {
				want = tc.reason.String()
			}
			if got := wireReason(t, err); got != want {
				t.Errorf("reason = %q, want %q", got, want)
			}
			if !errors.Is(err, tc.err) {
				t.Errorf("%v lost its cause %v", err, tc.err)
			}
		})
	}
}

// A dependency failing is told only to retry: its text is the server's, and
// Unavailable is not scrubbed on the way out. It stays the cause, for the log.
func TestAnswerCapabilityErrorWithholdsAStoresText(t *testing.T) {
	errStore := errors.New("pgstore: dial tcp 10.0.0.7:5432: connection refused")
	err := AnswerCapabilityError(errStore, nil)
	ce, ok := errors.AsType[*connect.Error](err)
	if !ok || ce.Message() != capabilityUnanswered {
		t.Fatalf("err = %v, want Unavailable telling %q", err, capabilityUnanswered)
	}
	if strings.Contains(err.Error(), "10.0.0.7") || !errors.Is(err, errStore) {
		t.Errorf("err = %v: the store's text is told, or its cause lost", err)
	}
}

// A handler's own answer, and no error, pass through unchanged.
func TestAnswerCapabilityErrorKeepsAnAnswer(t *testing.T) {
	answered := connect.NewError(connect.CodeNotFound, "no such capability")
	if got, ok := errors.AsType[*connect.Error](AnswerCapabilityError(answered, nil)); !ok || got != answered {
		t.Errorf("answer = %v, want %v unchanged", got, answered)
	}
	if got := AnswerCapabilityError(nil, nil); got != nil {
		t.Errorf("answer to no error = %v, want nil", got)
	}
}

// An operation's rows name codes limes defines, so a code it renames is
// caught by the operation's test rather than silently answered by the table.
func TestExceptionsUndefinedNamesWhatLimesDoesNot(t *testing.T) {
	const madeUp limes.Code = "made_up"
	got := Exceptions{limes.CodeRevoked: {}, madeUp: {}}.Undefined()
	if len(got) != 1 || got[0] != madeUp {
		t.Errorf("undefined = %v, want [%s]", got, madeUp)
	}
}
