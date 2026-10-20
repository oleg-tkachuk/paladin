package paladin_test

import (
	"errors"
	"strings"
	"testing"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// sealed stands in for a Biscuit the server sealed.
const sealed = "biscuit-token"

func TestBiscuitOf(t *testing.T) {
	got, err := paladin.BiscuitOf(&adminv1.CapabilityServiceIssueResponse{
		Biscuit: sealed, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED,
	})
	if err != nil || got != sealed {
		t.Fatalf("an issued Biscuit = %q, %v", got, err)
	}
	for _, status := range []adminv1.BiscuitStatus{
		adminv1.BiscuitStatus_BISCUIT_STATUS_KEY_BOUND,
		adminv1.BiscuitStatus_BISCUIT_STATUS_NOT_YET_VALID,
		// A server too old to say: no Biscuit is no Biscuit.
		adminv1.BiscuitStatus_BISCUIT_STATUS_UNSPECIFIED,
	} {
		got, err := paladin.BiscuitOf(&adminv1.CapabilityServiceIssueResponse{BiscuitStatus: status})
		if got != "" || !errors.Is(err, paladin.ErrNoBiscuit) || !strings.Contains(err.Error(), status.String()) {
			t.Errorf("%v: %q, %v; want ErrNoBiscuit naming the status", status, got, err)
		}
	}
}

// The Biscuit when the status says it is issued, the token otherwise, each
// with the status; neither is an error.
func TestCredentialOf(t *testing.T) {
	const token = "jwt-token"
	cases := map[string]struct {
		resp *adminv1.CapabilityServiceIssueResponse
		want paladin.Credential
	}{
		"issued": {
			&adminv1.CapabilityServiceIssueResponse{Token: token, Biscuit: sealed, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED},
			paladin.Credential{Value: sealed, Form: paladin.FormBiscuit, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED},
		},
		"key-bound": {
			&adminv1.CapabilityServiceIssueResponse{Token: token, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_KEY_BOUND},
			paladin.Credential{Value: token, Form: paladin.FormToken, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_KEY_BOUND},
		},
		"not yet valid": {
			&adminv1.CapabilityServiceIssueResponse{Token: token, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_NOT_YET_VALID},
			paladin.Credential{Value: token, Form: paladin.FormToken, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_NOT_YET_VALID},
		},
		// A server too old to name a status: a Biscuit beside it is not
		// trusted to be one, and the token is presented.
		"unspecified": {
			&adminv1.CapabilityServiceIssueResponse{Token: token, Biscuit: sealed},
			paladin.Credential{Value: token, Form: paladin.FormToken},
		},
	}
	for name, tc := range cases {
		got, err := paladin.CredentialOf(tc.resp)
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v, %v; want %+v", name, got, err, tc.want)
		}
	}
	if _, err := paladin.CredentialOf(&adminv1.CapabilityServiceIssueResponse{}); !errors.Is(err, paladin.ErrNoCredential) {
		t.Errorf("an empty answer: %v, want ErrNoCredential", err)
	}
	if paladin.FormToken.String() != "token" || paladin.FormBiscuit.String() != "biscuit" {
		t.Errorf("forms are named %q and %q", paladin.FormToken, paladin.FormBiscuit)
	}
}
