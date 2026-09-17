package cel

import (
	"fmt"

	celast "cel.dev/cel-go/common/ast"
)

// ObjectPushdown is the SQL-expressible subset of a CEL filter over
// ObjectSchema. Empty fields mean "no SQL predicate". Like the audit
// variant, this only NARROWS the candidate set — ListObjects still runs
// the full CEL program in-memory on the fetched page, so disjunctions,
// residual conjuncts, and unrecognised shapes stay authoritative and no
// matching row is ever dropped by pushdown.
type ObjectPushdown struct {
	StateEq     string // state == "<literal>"
	KeyPrefix   string // key.startsWith("<literal>")
	KeyContains string // key.contains("<literal>")

	Recognised int
}

// ExtractObjectPushdown parses expr against ObjectSchema and walks the
// top-level `&&` chain for predicates the objects query can express
// (state equality, key prefix/substring). A parse error is non-fatal —
// the caller falls back to a pure in-memory CEL scan with the same
// expression — so callers may ignore the error and treat a zero
// ObjectPushdown as "push nothing down".
func ExtractObjectPushdown(expr string) (ObjectPushdown, error) {
	if expr == "" {
		return ObjectPushdown{}, nil
	}
	env, err := buildEnv(ObjectSchema)
	if err != nil {
		return ObjectPushdown{}, fmt.Errorf("cel: build env: %w", err)
	}
	ast, iss := env.Parse(expr)
	if iss != nil && iss.Err() != nil {
		return ObjectPushdown{}, fmt.Errorf("cel: parse %q: %w", expr, iss.Err())
	}
	var out ObjectPushdown
	walkObjectConjuncts(ast.NativeRep().Expr(), &out)
	return out, nil
}

func walkObjectConjuncts(e celast.Expr, out *ObjectPushdown) {
	if e == nil || e.Kind() != celast.CallKind {
		return
	}
	c := e.AsCall()
	if c.FunctionName() == "_&&_" {
		for _, a := range c.Args() {
			walkObjectConjuncts(a, out)
		}
		return
	}
	if recogniseObjectLeaf(c, out) {
		out.Recognised++
	}
}

func recogniseObjectLeaf(c celast.CallExpr, out *ObjectPushdown) bool {
	switch c.FunctionName() {
	case "_==_":
		args := c.Args()
		if len(args) != 2 {
			return false
		}
		field, ok := identName(args[0])
		if !ok {
			return false
		}
		lit, ok := stringLiteral(args[1])
		if !ok {
			return false
		}
		if field == "state" && out.StateEq == "" {
			out.StateEq = lit
			return true
		}
		return false

	case "startsWith":
		field, ok := identName(c.Target())
		if !ok || field != "key" {
			return false
		}
		args := c.Args()
		if len(args) != 1 {
			return false
		}
		lit, ok := stringLiteral(args[0])
		if !ok || out.KeyPrefix != "" {
			return false
		}
		out.KeyPrefix = lit
		return true

	case "contains":
		field, ok := identName(c.Target())
		if !ok || field != "key" {
			return false
		}
		args := c.Args()
		if len(args) != 1 {
			return false
		}
		lit, ok := stringLiteral(args[0])
		if !ok || out.KeyContains != "" {
			return false
		}
		out.KeyContains = lit
		return true
	}
	return false
}
