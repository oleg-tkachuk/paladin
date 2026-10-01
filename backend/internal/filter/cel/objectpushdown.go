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
	recogniseObjectLeaf(c, out)
}

func recogniseObjectLeaf(c celast.CallExpr, out *ObjectPushdown) {
	switch c.FunctionName() {
	case "_==_":
		args := c.Args()
		if len(args) != 2 {
			return
		}
		field, ok := identName(args[0])
		if !ok {
			return
		}
		lit, ok := stringLiteral(args[1])
		if !ok {
			return
		}
		if field == "state" && out.StateEq == "" {
			out.StateEq = lit
		}

	case "startsWith":
		field, ok := identName(c.Target())
		if !ok || field != "key" {
			return
		}
		args := c.Args()
		if len(args) != 1 {
			return
		}
		lit, ok := stringLiteral(args[0])
		if !ok || out.KeyPrefix != "" {
			return
		}
		out.KeyPrefix = lit

	case "contains":
		field, ok := identName(c.Target())
		if !ok || field != "key" {
			return
		}
		args := c.Args()
		if len(args) != 1 {
			return
		}
		lit, ok := stringLiteral(args[0])
		if !ok || out.KeyContains != "" {
			return
		}
		out.KeyContains = lit
	}
}
