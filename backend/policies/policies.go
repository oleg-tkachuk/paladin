// Package policies carries the Cedar schema and the example policies as
// embedded files, and validates policy text against the schema.
package policies

import (
	"embed"
	"errors"
	"fmt"

	"github.com/cedar-policy/cedar-go"
	xast "github.com/cedar-policy/cedar-go/x/exp/ast"
	"github.com/cedar-policy/cedar-go/x/exp/schema"
	"github.com/cedar-policy/cedar-go/x/exp/schema/resolved"
	"github.com/cedar-policy/cedar-go/x/exp/schema/validate"
)

// Schema is policies/schema.cedarschema in Cedar's human-readable format.
//
//go:embed schema.cedarschema
var Schema []byte

// Examples holds policies/examples/*.cedar.
//
//go:embed examples/*.cedar
var Examples embed.FS

// ExamplesDir is the directory inside Examples that holds the policies.
const ExamplesDir = "examples"

// DefaultTenantPolicy is the policy a tenant gets at creation when the caller
// supplies none, with TenantPlaceholder standing for the tenant's Cedar UID.
//
//go:embed examples/default.cedar
var DefaultTenantPolicy string

// TenantPlaceholder is the Tenant UID in DefaultTenantPolicy that is
// replaced with the tenant's slug, or its UUID when it has none.
const TenantPlaceholder = "placeholder"

// Resolve parses Schema and resolves its type references.
func Resolve() (*resolved.Schema, error) {
	var s schema.Schema
	if err := s.UnmarshalCedar(Schema); err != nil {
		return nil, fmt.Errorf("parse schema: %w", err)
	}
	r, err := s.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve schema: %w", err)
	}
	return r, nil
}

// Validate type-checks every policy in text against Schema in strict mode,
// the mode that reports an attribute read that can raise an evaluation
// error. name labels the errors.
func Validate(name string, text []byte) error {
	r, err := Resolve()
	if err != nil {
		return err
	}
	ps, err := cedar.NewPolicySetFromBytes(name, text)
	if err != nil {
		return fmt.Errorf("%s: parse: %w", name, err)
	}
	v := validate.New(r, validate.WithStrict())
	var errs []error
	for id, p := range ps.All() {
		if err := v.Policy(string(id), (*xast.Policy)(p.AST())); err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", name, id, err))
		}
	}
	return errors.Join(errs...)
}
