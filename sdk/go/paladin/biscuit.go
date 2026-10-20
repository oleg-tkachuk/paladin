package paladin

import (
	"errors"
	"fmt"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// ErrNoBiscuit is an Issue or Delegate answer that carries no Biscuit. The
// error BiscuitOf returns wraps it and names why.
var ErrNoBiscuit = errors.New("paladin: the capability was issued without a Biscuit")

// BiscuitOf is the Biscuit an Issue or Delegate answer carries, or an error
// wrapping ErrNoBiscuit that names its BiscuitStatus — so a caller that needs
// one fails where it asks, rather than passing an empty token on. To be
// refused before anything is issued instead, set require_biscuit.
func BiscuitOf(resp *adminv1.CapabilityServiceIssueResponse) (string, error) {
	if resp.GetBiscuitStatus() == adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED && resp.GetBiscuit() != "" {
		return resp.GetBiscuit(), nil
	}
	return "", fmt.Errorf("%w: %s", ErrNoBiscuit, resp.GetBiscuitStatus())
}
