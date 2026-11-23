package capabilityh

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// errPolicyStore is the engine failing to load a policy.
var errPolicyStore = errors.New("policy store unreachable")

// failingAuthorizer cannot decide: its policy store is down.
type failingAuthorizer struct{}

func (failingAuthorizer) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionDeny, errPolicyStore
}

// A delegation the caveats or Cedar refuse is answered before anything is
// minted, and logged by what it is: a refusal at warn, an engine that could
// not decide at error, each naming the capability.
func TestDelegate_RefusalsAreLoggedAndMintNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ops    []limes.Op
		policy cedar.Authorizer
		code   connect.Code
		level  zapcore.Level
		msg    string
	}{
		{"denied by policy", []limes.Op{limes.OpShare}, &denyAuthorizer{}, connect.CodePermissionDenied, zapcore.WarnLevel, msgDelegationDenied},
		{"no share", []limes.Op{limes.OpGet}, &allowAuthorizer{}, connect.CodePermissionDenied, zapcore.WarnLevel, msgDelegationWithoutShare},
		{"engine failure", []limes.Op{limes.OpShare}, failingAuthorizer{}, connect.CodeOf(apiutil.MapError(errPolicyStore)), zapcore.ErrorLevel, msgDelegationUnchecked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &insertCtxStore{}
			m := newMinter(t, store)
			parent := mintParent(t, m, tc.ops...)
			store.insertCtx = nil // the parent's own record
			core, logs := observer.New(zapcore.DebugLevel)
			h := NewHandler(m.Issuer, m.Verifier, store, nil, tc.policy).WithLogger(zap.New(core))

			_, err := h.Delegate(auth.WithCapability(context.Background(), parent), delegateReq(parent))
			if got := connect.CodeOf(err); got != tc.code {
				t.Fatalf("code = %v, want %v (%v)", got, tc.code, err)
			}
			if store.insertCtx != nil {
				t.Error("a child was recorded although the delegation was refused")
			}
			lines := logs.FilterMessage(tc.msg).FilterLevelExact(tc.level).All()
			if len(lines) != 1 || lines[0].ContextMap()["capability_id"] != parent.Capability().ID.String() {
				t.Fatalf("want one %v line %q naming the capability, got %v", tc.level, tc.msg, logs.All())
			}
		})
	}
}
