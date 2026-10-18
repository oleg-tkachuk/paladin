package iam

import (
	"testing"

	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// Every grant state has a wire name, and every wire name but UNSPECIFIED is
// some state's: a state left out would reach the console as UNSPECIFIED and
// show as neither live nor ended.
func TestOAuthGrantStateToProto_CoversEveryState(t *testing.T) {
	states := []authstore.OAuthGrantState{
		authstore.OAuthGrantActive,
		authstore.OAuthGrantRevoked,
		authstore.OAuthGrantExpired,
	}
	seen := map[pb.OAuthGrant_State]bool{}
	for _, s := range states {
		got := oauthGrantToProto(authstore.UserOAuthGrant{State: s}).GetState()
		if got == pb.OAuthGrant_STATE_UNSPECIFIED {
			t.Errorf("state %v reaches the wire as UNSPECIFIED", s)
		}
		seen[got] = true
	}
	for v := range pb.OAuthGrant_State_name {
		w := pb.OAuthGrant_State(v)
		if w != pb.OAuthGrant_STATE_UNSPECIFIED && !seen[w] {
			t.Errorf("%v names no grant state", w)
		}
	}
}
