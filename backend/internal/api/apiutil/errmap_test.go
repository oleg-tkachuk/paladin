package apiutil

import (
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"
)

func TestMapError(t *testing.T) {
	registered := errors.New("registered sentinel")
	RegisterError(registered, connect.CodeResourceExhausted)

	cases := []struct {
		name string
		err  error
		want connect.Code // 0 means "expect nil error"
	}{
		{"nil", nil, 0},
		{"canonical not-found", ErrNotFound, connect.CodeNotFound},
		{"wrapped conflict", fmt.Errorf("update: %w", ErrConflict), connect.CodeAborted},
		{"failed precondition", ErrFailedPrecondition, connect.CodeFailedPrecondition},
		{"registered sentinel wrapped", fmt.Errorf("x: %w", registered), connect.CodeResourceExhausted},
		{"unknown → internal", errors.New("boom"), connect.CodeInternal},
		{
			"already a connect error passes through",
			connect.NewError(connect.CodeUnavailable, errors.New("down")),
			connect.CodeUnavailable,
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
		})
	}
}
