package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

// When a subsystem is off by config the service is still mounted, so a caller
// reaches a real handler instead of the mux's 404. What makes that worth doing
// is the metadata: X-Paladin-Reason lets the console say "your platform admin
// turned this off" rather than "this build does not implement that call".
//
// Both stubs embed the generated Unimplemented* struct so a newly added RPC
// keeps compiling. That is also the hazard, and the file's own compile-time
// assertion cannot see it: `var _ Handler = stub{}` is satisfied by the
// embedded struct whether or not the method was overridden. A method added to
// the proto and forgotten here returns a bare Unimplemented with no headers,
// and the console shows the wrong message about a feature the operator can
// actually enable.
//
// So this walks the generated interface rather than a list written by hand.
// A new RPC is covered the moment it exists.
func TestDisabledSubsystemHandlers(t *testing.T) {
	cases := []struct {
		name      string
		iface     reflect.Type
		stub      any
		subsystem string
		flag      string
	}{
		{
			name:      "capability",
			iface:     reflect.TypeOf((*paladinadminv1connect.CapabilityServiceHandler)(nil)).Elem(),
			stub:      disabledCapabilityServiceHandler{},
			subsystem: "capability",
			flag:      "config.capability.enabled",
		},
		{
			name:      "api_token",
			iface:     reflect.TypeOf((*paladinadminv1connect.APITokenServiceHandler)(nil)).Elem(),
			stub:      disabledAPITokenServiceHandler{},
			subsystem: "api_token",
			flag:      "config.api_token.enabled",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := reflect.ValueOf(tc.stub)
			if tc.iface.NumMethod() == 0 {
				t.Fatal("the generated handler interface has no methods — this test would assert nothing")
			}
			for i := range tc.iface.NumMethod() {
				m := tc.iface.Method(i)
				t.Run(m.Name, func(t *testing.T) {
					fn := stub.MethodByName(m.Name)
					if !fn.IsValid() {
						t.Fatalf("stub does not implement %s", m.Name)
					}
					// (ctx, *connect.Request[T]) — the stubs ignore both, so
					// a zero request is enough to reach the return.
					args := make([]reflect.Value, fn.Type().NumIn())
					args[0] = reflect.ValueOf(context.Background())
					for j := 1; j < len(args); j++ {
						args[j] = reflect.New(fn.Type().In(j).Elem())
					}

					out := fn.Call(args)
					errVal := out[len(out)-1]
					if errVal.IsNil() {
						t.Fatalf("%s returned no error — a disabled subsystem must refuse", m.Name)
					}
					err, _ := errVal.Interface().(error)

					var ce *connect.Error
					if !errors.As(err, &ce) {
						t.Fatalf("%s returned %v, want a *connect.Error", m.Name, err)
					}
					if ce.Code() != connect.CodeUnimplemented {
						t.Errorf("%s code = %v, want Unimplemented", m.Name, ce.Code())
					}
					// The headers are the whole reason for mounting a stub
					// instead of leaving the route unregistered. Without them
					// this is indistinguishable from the embedded default.
					if got := ce.Meta().Get(HeaderReason); got != ReasonDisabled {
						t.Errorf("%s %s = %q, want %q — the console keys on this to tell a disabled feature from a missing one",
							m.Name, HeaderReason, got, ReasonDisabled)
					}
					if got := ce.Meta().Get(HeaderSubsystem); got != tc.subsystem {
						t.Errorf("%s %s = %q, want %q", m.Name, HeaderSubsystem, got, tc.subsystem)
					}
					// The message names the flag an operator flips. A
					// subsystem named without its flag sends them looking.
					// The message is the operator's instruction: which
					// subsystem, which flag, and which way to set it.
					msg := ce.Message()
					if !strings.Contains(msg, tc.subsystem) || !strings.Contains(msg, tc.flag) {
						t.Errorf("%s message = %q, want it to name both %q and %q",
							m.Name, msg, tc.subsystem, tc.flag)
					}
					if !strings.Contains(msg, tc.flag+"=true") {
						t.Errorf("%s message = %q, want it to say to set %s=true", m.Name, msg, tc.flag)
					}
				})
			}
		})
	}
}
