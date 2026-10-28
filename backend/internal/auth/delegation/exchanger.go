package delegation

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/exchange"
)

// NewExchanger is the exchange of ADR-0041 over the delegation issuer:
// subjects reads callers' tokens, users finds their users, and each
// capability lives at most ttl. Every grant and refusal is logged to log.
func NewExchanger(issuer *limes.Issuer, subjects exchange.SubjectVerifier, users UserDirectory, ttl time.Duration, log *slog.Logger) (*exchange.Exchanger, error) {
	ex, err := exchange.NewExchanger(exchange.Config{
		Issuer:   issuer,
		Subjects: subjects,
		Policy:   Policy(users, ttl),
		Logger:   log,
	})
	if err != nil {
		return nil, fmt.Errorf("delegation: exchange: %w", err)
	}
	return ex, nil
}
