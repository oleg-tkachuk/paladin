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
