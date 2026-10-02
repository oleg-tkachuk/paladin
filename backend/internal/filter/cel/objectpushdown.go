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

	ContentTypeEq     string // content_type == "<literal>"
	ContentTypePrefix string // content_type.startsWith("<literal>")

	// TagsEq and MetadataEq hold `tags["k"] == "v"` (or `tags.k == "v"`)
	// conjuncts. The query turns each map into one jsonb containment
	// (`tags @> {"k":"v"}`), which the GIN index answers. Both maps are
	// string→string on the wire, so containment of a string value is exactly
	// CEL equality: a row whose map lacks the key fails both.
	TagsEq     map[string]string
	MetadataEq map[string]string
}

// ExtractObjectPushdown parses expr against ObjectSchema and walks the
// top-level `&&` chain for predicates the objects query can express
// (state and content-type equality, key and content-type prefix, key
// substring, tag and metadata equality). A parse error is non-fatal —
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
		// CEL keeps operand order, so `"v" == tags["k"]` is its own AST.
		recogniseObjectEq(args[0], args[1], out)
		recogniseObjectEq(args[1], args[0], out)

	case "startsWith":
		lit, ok := methodLiteral(c)
		if !ok {
			return
		}
		switch field, _ := identName(c.Target()); field {
		case "key":
			if out.KeyPrefix == "" {
				out.KeyPrefix = lit
			}
		case "content_type":
			if out.ContentTypePrefix == "" {
				out.ContentTypePrefix = lit
			}
		}

	case "contains":
		lit, ok := methodLiteral(c)
		if !ok {
			return
		}
		if field, _ := identName(c.Target()); field == "key" && out.KeyContains == "" {
			out.KeyContains = lit
		}
	}
}

// recogniseObjectEq records `lhs == rhs` when lhs is a pushable field and rhs
// a string literal. The first equality on a field wins: two different values
// cannot both hold, and keeping one keeps the pushdown a subset of the filter.
func recogniseObjectEq(lhs, rhs celast.Expr, out *ObjectPushdown) {
	lit, ok := stringLiteral(rhs)
	if !ok {
		return
	}
	if field, ok := identName(lhs); ok {
		switch field {
		case "state":
			if out.StateEq == "" {
				out.StateEq = lit
			}
		case "content_type":
			if out.ContentTypeEq == "" {
				out.ContentTypeEq = lit
			}
		}
		return
	}
	mapName, key, ok := mapEntry(lhs)
	if !ok {
		return
	}
	switch mapName {
	case "tags":
		setFirst(&out.TagsEq, key, lit)
	case "metadata":
		setFirst(&out.MetadataEq, key, lit)
	}
}

// mapEntry recognises `m["k"]` and `m.k` over a top-level identifier.
func mapEntry(e celast.Expr) (mapName, key string, ok bool) {
	if e == nil {
		return "", "", false
	}
	switch e.Kind() {
	case celast.CallKind:
		c := e.AsCall()
		if c.FunctionName() != "_[_]" || len(c.Args()) != 2 {
			return "", "", false
		}
		name, ok := identName(c.Args()[0])
		if !ok {
			return "", "", false
		}
		k, ok := stringLiteral(c.Args()[1])
		if !ok {
			return "", "", false
		}
		return name, k, true
	case celast.SelectKind:
		sel := e.AsSelect()
		// `has(m.k)` is a test-only select; it is a predicate on its own and
		// never the operand of an equality, but guard it anyway.
		if sel.IsTestOnly() {
			return "", "", false
		}
		name, ok := identName(sel.Operand())
		if !ok {
			return "", "", false
		}
		return name, sel.FieldName(), true
	default:
		return "", "", false
	}
}

// methodLiteral returns the single string-literal argument of a receiver-style
// call such as `key.startsWith("x")`.
func methodLiteral(c celast.CallExpr) (string, bool) {
	args := c.Args()
	if len(args) != 1 {
		return "", false
	}
	return stringLiteral(args[0])
}

func setFirst(m *map[string]string, k, v string) {
	if *m == nil {
		*m = map[string]string{}
	}
	if _, seen := (*m)[k]; seen {
		return
	}
	(*m)[k] = v
}
