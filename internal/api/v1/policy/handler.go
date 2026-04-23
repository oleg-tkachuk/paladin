// Package policy exposes policy-text helpers (validation) to the API layer.
// Persistence still lives on TenantService / BucketService via their
// inherited_cedar_policy and BucketPolicy.cedar_policy fields.
package policy

import (
	"context"

	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

// ValidatePolicy parses the Cedar policy text. Returns (valid, parser-error-text).
func (h *Handler) ValidatePolicy(_ context.Context, text string) (bool, string) {
	if err := cedar.Validate(text); err != nil {
		return false, err.Error()
	}
	return true, ""
}
