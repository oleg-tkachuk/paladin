package delegation

import (
	"fmt"
	"log/slog"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/exchange"
)

// NewExchanger is the exchange of ADR-0041 over the delegation issuer:
// subjects reads callers' tokens, users finds their users, and each
// capability's unlimited budget is in unit. exchanges records the one
// capability each token is exchanged for, so exchanging it again, from any
// replica, hands that one back; it lives as long as its token. Every grant
// and refusal is logged to log.
func NewExchanger(issuer *limes.Issuer, subjects exchange.SubjectVerifier, exchanges limes.ExchangeStore, users UserDirectory, unit string, log *slog.Logger) (*exchange.Exchanger, error) {
	ex, err := exchange.NewExchanger(exchange.Config{
		Issuer:    issuer,
		Subjects:  subjects,
		Policy:    Policy(users, unit),
		Exchanges: exchanges,
		Logger:    log,
	})
	if err != nil {
		return nil, fmt.Errorf("delegation: exchange: %w", err)
	}
	return ex, nil
}
