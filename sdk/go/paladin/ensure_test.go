package paladin_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// ensureCalls scripts what get and create answer, in order, and counts them.
type ensureCalls struct {
	gets, creates []error
	got, made     int
}

func (e *ensureCalls) get(context.Context) (string, error) {
	err := e.gets[e.got]
	e.got++
	return "existing", err
}

func (e *ensureCalls) create(context.Context) (string, error) {
	err := e.creates[e.made]
	e.made++
	return "created", err
}

func TestEnsure(t *testing.T) {
	notFound := connect.NewError(connect.CodeNotFound, "no such collection")
	exists := connect.NewError(connect.CodeAlreadyExists, "taken")
	denied := connect.NewError(connect.CodePermissionDenied, "no")
	cases := []struct {
		name        string
		calls       ensureCalls
		want        string
		wantCreated bool
		wantErr     error
	}{
		{"there already", ensureCalls{gets: []error{nil}}, "existing", false, nil},
		{"created", ensureCalls{gets: []error{notFound}, creates: []error{nil}}, "created", true, nil},
		{"another process won the create", ensureCalls{gets: []error{notFound, nil}, creates: []error{exists}}, "existing", false, nil},
		{"get refused", ensureCalls{gets: []error{denied}}, "", false, denied},
		{"create refused", ensureCalls{gets: []error{notFound}, creates: []error{denied}}, "", false, denied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, created, err := paladin.Ensure(context.Background(), tc.calls.get, tc.calls.create)
			if !errors.Is(err, tc.wantErr) || got != tc.want || created != tc.wantCreated {
				t.Errorf("Ensure = (%q, %v, %v), want (%q, %v, %v)", got, created, err, tc.want, tc.wantCreated, tc.wantErr)
			}
		})
	}
}
