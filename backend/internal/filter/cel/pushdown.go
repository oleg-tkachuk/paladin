package cel

import (
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	celast "github.com/google/cel-go/common/ast"
)

// Pushdown is the SQL-expressible subset of a CEL filter over any Schema.
//
// ObjectPushdown and AuditPushdown came first and stayed: their schemas have
// shapes worth special-casing (object state is an enum column, audit has
// timestamp ranges). Everything else — buckets, backends, collections,
// tenants, users, operations — wants the same three predicates over plain text
// and boolean columns, and writing six near-identical walkers is how the two
// that exist came to disagree about `_==_` argument order.
//
// The contract matches the other two: this only NARROWS the candidate set.
// The full CEL program still runs over the fetched page, so a predicate that
// does not survive the walk (a disjunction, an unrecognised function, a field
// the query does not carry) costs a wider scan and never a wrong answer.
type Pushdown struct {
	// Eq holds `field == "literal"` for string-typed fields.
	Eq map[string]string
	// BoolEq holds `field == true` / `!field` for bool-typed fields.
	BoolEq map[string]bool
	// Prefix and Contains hold field.startsWith / field.contains literals.
	Prefix   map[string]string
	Contains map[string]string

	// Recognised counts the conjuncts that produced a predicate. Zero means
	// the caller may skip the pushdown entirely.
	Recognised int
}

// ExtractPushdown parses expr against schema and walks the top-level `&&`
// chain for predicates a SQL query can express.
//
// A parse error is non-fatal to the caller: the same expression is compiled
// and evaluated authoritatively elsewhere, so a zero Pushdown means "push
// nothing down", not "reject the request".
func ExtractPushdown(schema *Schema, expr string) (Pushdown, error) {
	out := Pushdown{}
	if expr == "" || schema == nil {
		return out, nil
	}
	env, err := buildEnv(schema)
	if err != nil {
		return out, fmt.Errorf("cel: build env: %w", err)
	}
	ast, iss := env.Parse(expr)
	if iss != nil && iss.Err() != nil {
		return out, fmt.Errorf("cel: parse %q: %w", expr, iss.Err())
	}
	out.walk(ast.NativeRep().Expr(), schema)
	return out, nil
}

func (p *Pushdown) walk(e celast.Expr, schema *Schema) {
	if e == nil {
		return
	}
	if e.Kind() == celast.IdentKind {
		if p.recogniseIdent(e, schema) {
			p.Recognised++
		}
		return
	}
	if e.Kind() != celast.CallKind {
		return
	}
	c := e.AsCall()
	if c.FunctionName() == "_&&_" {
		for _, a := range c.Args() {
			p.walk(a, schema)
		}
		return
	}
	if p.recognise(c, schema) {
		p.Recognised++
	}
}

func (p *Pushdown) recognise(c celast.CallExpr, schema *Schema) bool {
	switch c.FunctionName() {
	case "_==_":
		// CEL does not normalise operand order, so `state == "X"` and
		// `"X" == state` are different ASTs for the same predicate.
		args := c.Args()
		if len(args) != 2 {
			return false
		}
		field, lit, ok := identAndLiteral(args[0], args[1])
		if !ok {
			return false
		}
		switch fieldType(schema, field) {
		case typeString:
			s, ok := lit.(string)
			if !ok {
				return false
			}
			return p.setEq(field, s)
		case typeBool:
			b, ok := lit.(bool)
			if !ok {
				return false
			}
			return p.setBool(field, b)
		default:
			return false
		}

	case "!_":
		// `!disabled` is how a filter author writes `disabled == false`.
		args := c.Args()
		if len(args) != 1 {
			return false
		}
		field, ok := identName(args[0])
		if !ok || fieldType(schema, field) != typeBool {
			return false
		}
		return p.setBool(field, false)

	case "startsWith", "contains":
		field, ok := identName(c.Target())
		if !ok || fieldType(schema, field) != typeString {
			return false
		}
		args := c.Args()
		if len(args) != 1 {
			return false
		}
		lit, ok := stringLiteral(args[0])
		if !ok {
			return false
		}
		if c.FunctionName() == "startsWith" {
			return p.setIn(&p.Prefix, field, lit)
		}
		return p.setIn(&p.Contains, field, lit)
	}
	return false
}

// A bare identifier is a predicate too: `enabled` means `enabled == true`.
// Only reachable through the `&&` walk, where the operand is a leaf.
func (p *Pushdown) recogniseIdent(e celast.Expr, schema *Schema) bool {
	field, ok := identName(e)
	if !ok || fieldType(schema, field) != typeBool {
		return false
	}
	return p.setBool(field, true)
}

func (p *Pushdown) setEq(field, lit string) bool {
	if p.Eq == nil {
		p.Eq = map[string]string{}
	}
	// Two equalities on one field cannot both hold; leaving the first in
	// place keeps the pushdown a subset of the predicate, which is all the
	// contract requires.
	if _, seen := p.Eq[field]; seen {
		return false
	}
	p.Eq[field] = lit
	return true
}

func (p *Pushdown) setBool(field string, v bool) bool {
	if p.BoolEq == nil {
		p.BoolEq = map[string]bool{}
	}
	if _, seen := p.BoolEq[field]; seen {
		return false
	}
	p.BoolEq[field] = v
	return true
}

func (p *Pushdown) setIn(m *map[string]string, field, lit string) bool {
	if *m == nil {
		*m = map[string]string{}
	}
	if _, seen := (*m)[field]; seen {
		return false
	}
	(*m)[field] = lit
	return true
}

// StringHint returns the SQL narrowing hints for one text column: an exact
// value and a LIKE pattern, either of which may be nil.
//
// A literal carrying LIKE metacharacters (%, _, \) is dropped rather than
// escaped: the authoritative CEL pass still runs, so a dropped hint only
// widens the scan, and an unescaped one would silently widen it anyway while
// looking precise.
func (p Pushdown) StringHint(field string) (eq *string, like *string) {
	if v, ok := p.Eq[field]; ok {
		eq = &v
	}
	if v, ok := p.Prefix[field]; ok && likeSafe(v) {
		pat := v + "%"
		like = &pat
	} else if v, ok := p.Contains[field]; ok && likeSafe(v) {
		pat := "%" + v + "%"
		like = &pat
	}
	return eq, like
}

// BoolHint returns the SQL narrowing hint for one boolean column.
func (p Pushdown) BoolHint(field string) *bool {
	if v, ok := p.BoolEq[field]; ok {
		return &v
	}
	return nil
}

func likeSafe(s string) bool {
	return s != "" && !strings.ContainsAny(s, `%_\`)
}

type celFieldType int

const (
	typeOther celFieldType = iota
	typeString
	typeBool
)

func fieldType(schema *Schema, field string) celFieldType {
	t, ok := schema.vars[field]
	if !ok {
		return typeOther
	}
	switch t {
	case cel.StringType:
		return typeString
	case cel.BoolType:
		return typeBool
	default:
		return typeOther
	}
}

// identAndLiteral accepts an equality's operands in either order and returns
// the field name with the literal's Go value.
func identAndLiteral(a, b celast.Expr) (string, any, bool) {
	if name, ok := identName(a); ok {
		if v, ok := literalValue(b); ok {
			return name, v, true
		}
	}
	if name, ok := identName(b); ok {
		if v, ok := literalValue(a); ok {
			return name, v, true
		}
	}
	return "", nil, false
}

func literalValue(e celast.Expr) (any, bool) {
	if e == nil || e.Kind() != celast.LiteralKind {
		return nil, false
	}
	return e.AsLiteral().Value(), true
}
