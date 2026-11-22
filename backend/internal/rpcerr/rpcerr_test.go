package rpcerr_test

import (
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
)

var errSentinel = errors.New("sentinel")

func TestNewKeepsTheCauseAndTheText(t *testing.T) {
	wrapped := fmt.Errorf("%w: detail", errSentinel)
	err := rpcerr.New(connect.CodeInvalidArgument, wrapped)
	if !errors.Is(err, errSentinel) {
		t.Error("errors.Is lost the sentinel")
	}
	if err.Code() != connect.CodeInvalidArgument || err.Message() != wrapped.Error() {
		t.Errorf("code %v message %q", err.Code(), err.Message())
	}
}

// refusal is a Refusal, as a deciding package defines one.
type refusal struct{}

func (refusal) Error() string { return "refused" }
func (refusal) Refuses()      {}

// A refusal is found through the Connect error that answers it and through
// any wrap; an error that is none is not one.
func TestRefusedFindsTheMarkerThroughTheCause(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"the refusal", refusal{}, true},
		{"wrapped", fmt.Errorf("resolve: %w", refusal{}), true},
		{"a connect error's cause", rpcerr.New(connect.CodeUnavailable, refusal{}), true},
		{"a plain error", errSentinel, false},
		{"a connect error without a cause", connect.NewError(connect.CodeUnavailable, "refused"), false},
		{"nil", nil, false},
	} {
		if got := rpcerr.Refused(tc.err); got != tc.want {
			t.Errorf("%s: Refused = %v, want %v", tc.name, got, tc.want)
		}
	}
}
