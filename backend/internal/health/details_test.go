package health

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

var poolDetails = []Detail{{Name: "connections", Value: "3 of 20 in use"}}

func describe(context.Context) []Detail { return poolDetails }

// Details are reported beside the status, healthy or not: a failing
// component's details are what explain it.
func TestDetailsAreReportedWhetherTheCheckPassesOrNot(t *testing.T) {
	t.Parallel()
	h := &Handler{Ready: []Probe{
		Check{Name: "up", Func: ok, Describe: describe},
		Check{Name: "down", Func: func(context.Context) error { return errors.New("refused") }, Describe: describe},
	}}
	for _, c := range h.Snapshot(context.Background(), "api").Components {
		if !reflect.DeepEqual(c.Details, poolDetails) {
			t.Errorf("%s details = %v, want %v", c.Name, c.Details, poolDetails)
		}
	}
}

// A component that is off reports nothing beside why: its dependency is
// not touched, and neither is anything that describes it.
func TestAnOffComponentReportsNoDetails(t *testing.T) {
	t.Parallel()
	h := &Handler{Ready: []Probe{Check{
		Name:   "replica",
		Switch: Fixed(ByConfig(false, "datastores.postgres.replica.enabled")),
		Describe: func(context.Context) []Detail {
			t.Error("an off component was described")
			return nil
		},
	}}}
	if got := h.Snapshot(context.Background(), "api").Components[0].Details; got != nil {
		t.Errorf("details = %v, want none", got)
	}
}
