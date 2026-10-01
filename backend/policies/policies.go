// Package policies carries the Cedar schema and the example policies as
// embedded files, and validates policy text against the schema.
package policies

import (
	"embed"
	"errors"
	"fmt"
	"sync"

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

// Resolve parses Schema and resolves its type references. The result is
// computed once; Schema is embedded and does not change.
func Resolve() (*resolved.Schema, error) { return resolveOnce() }

var resolveOnce = sync.OnceValues(resolve)

func resolve() (*resolved.Schema, error) {
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

// ErrUnparseable marks policy text Cedar cannot parse, as distinct from text
// that parses and does not type-check.
var ErrUnparseable = errors.New("policy does not parse")

// Check type-checks every policy in text against Schema in strict mode, the
// mode that reports an attribute read that can raise an evaluation error. It
// returns one finding per policy that does not type-check, and an error
// wrapping ErrUnparseable when the text does not parse. name labels both.
func Check(name string, text []byte) ([]string, error) {
	r, err := Resolve()
	if err != nil {
		return nil, err
	}
	ps, err := cedar.NewPolicySetFromBytes(name, text)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnparseable, name, err)
	}
	v := validate.New(r, validate.WithStrict())
	var findings []string
	for id, p := range ps.All() {
		if err := v.Policy(string(id), (*xast.Policy)(p.AST())); err != nil {
			findings = append(findings, fmt.Sprintf("%s %s: %v", name, id, err))
		}
	}
	return findings, nil
}

// Validate is Check with every finding returned as one error.
func Validate(name string, text []byte) error {
	findings, err := Check(name, text)
	if err != nil {
		return err
	}
	errs := make([]error, len(findings))
	for i, f := range findings {
		errs[i] = errors.New(f)
	}
	return errors.Join(errs...)
}
