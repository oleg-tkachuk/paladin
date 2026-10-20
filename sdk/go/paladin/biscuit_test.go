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
