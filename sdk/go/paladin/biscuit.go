package paladin

import (
	"errors"
	"fmt"
	"log/slog"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// ErrNoBiscuit is an Issue or Delegate answer that carries no Biscuit. The
// error BiscuitOf returns wraps it and names why.
var ErrNoBiscuit = errors.New("paladin: the capability was issued without a Biscuit")

// BiscuitOf is the Biscuit an Issue or Delegate answer carries, or an error
// wrapping ErrNoBiscuit that names its BiscuitStatus — so a caller that needs
// one fails where it asks, rather than passing an empty token on. To be
// refused before anything is issued instead, set require_biscuit. A caller
// that only presents the capability wants CredentialOf.
func BiscuitOf(resp *adminv1.CapabilityServiceIssueResponse) (string, error) {
	if resp.GetBiscuitStatus() == adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED && resp.GetBiscuit() != "" {
		return resp.GetBiscuit(), nil
	}
	return "", fmt.Errorf("%w: %s", ErrNoBiscuit, resp.GetBiscuitStatus())
}

// CredentialForm is which form of a capability a Credential is. Both are
// presented the same way, with WithCapability.
type CredentialForm int

const (
	// FormToken is the capability's JWT.
	FormToken CredentialForm = iota + 1
	// FormBiscuit is its Biscuit, which its holder can also narrow offline
	// with Attenuate.
	FormBiscuit
)

// String names the form.
func (f CredentialForm) String() string {
	switch f {
	case FormToken:
		return "token"
	case FormBiscuit:
		return "biscuit"
	}
	return fmt.Sprintf("CredentialForm(%d)", int(f))
}

// MarshalText is the form's name, so encoding/json and slog's JSON handler
// write "token" rather than its number.
func (f CredentialForm) MarshalText() ([]byte, error) { return []byte(f.String()), nil }

// LogValue is the form's name.
func (f CredentialForm) LogValue() slog.Value { return slog.StringValue(f.String()) }

// Credential is what to present for a capability an Issue or Delegate answer
// carries.
type Credential struct {
	// Value is presented with WithCapability.
	Value string
	// Form is which form Value is.
	Form CredentialForm
	// BiscuitStatus is the answer's: ISSUED for a Biscuit, otherwise why the
	// token is presented instead.
	BiscuitStatus adminv1.BiscuitStatus
}

// The attributes a Credential logs as.
const (
	logKeyForm          = "form"
	logKeyBiscuitStatus = "biscuit_status"
)

// LogValue is the credential's form and BiscuitStatus, by name. Never its
// Value: that is the capability itself, which a log must not hold.
func (c Credential) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String(logKeyForm, c.Form.String()),
		slog.String(logKeyBiscuitStatus, c.BiscuitStatus.String()),
	)
}

// ErrNoCredential is an Issue or Delegate answer that carries neither a
// token nor a Biscuit.
var ErrNoCredential = errors.New("paladin: the answer carries no capability to present")

// CredentialOf is what to present for the capability an Issue or Delegate
// answer carries: its Biscuit when the status is ISSUED, its token otherwise,
// with the status that says why. A caller that only presents the capability
// needs no more; one that must narrow it offline asks BiscuitOf.
func CredentialOf(resp *adminv1.CapabilityServiceIssueResponse) (Credential, error) {
	status := resp.GetBiscuitStatus()
	if b := resp.GetBiscuit(); b != "" && status == adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED {
		return Credential{Value: b, Form: FormBiscuit, BiscuitStatus: status}, nil
	}
	if t := resp.GetToken(); t != "" {
		return Credential{Value: t, Form: FormToken, BiscuitStatus: status}, nil
	}
	return Credential{}, fmt.Errorf("%w: biscuit status %s", ErrNoCredential, status)
}

// TokenOf is the token an Issue or Delegate answer carries, whatever Biscuit
// comes beside it: for a caller that presents the capability as issued and
// never narrows it offline. An answer without one wraps ErrNoCredential.
func TokenOf(resp *adminv1.CapabilityServiceIssueResponse) (string, error) {
	if t := resp.GetToken(); t != "" {
		return t, nil
	}
	return "", fmt.Errorf("%w: no token", ErrNoCredential)
}
