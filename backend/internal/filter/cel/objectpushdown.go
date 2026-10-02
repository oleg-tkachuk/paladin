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

// ExtractObjectPushdown is ExtractObjectBranches for a caller that can run
// only one query: the single branch when there is one, nothing otherwise. A
// parse error is non-fatal — the caller falls back to a pure in-memory CEL
// scan with the same expression — so callers may ignore the error and treat a
// zero ObjectPushdown as "push nothing down".
func ExtractObjectPushdown(expr string) (ObjectPushdown, error) {
	branches, err := ExtractObjectBranches(expr)
	if err != nil {
		return ObjectPushdown{}, err
	}
	if len(branches) != 1 {
		return ObjectPushdown{}, nil
	}
	return branches[0], nil
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

// maxObjectBranches caps how many disjuncts a filter is split into. Each
// branch is one indexed query, so a filter that expands past this is
// cheaper to scan than to fan out; it then pushes nothing down, which is
// always correct.
const maxObjectBranches = 8

// ExtractObjectBranches is ExtractObjectPushdown for filters with `||`.
//
// It rewrites the SQL-expressible part of expr in disjunctive normal form:
// one ObjectPushdown per disjunct, the union of whose matches contains every
// object the full expression accepts. Anything it does not recognise — a
// negation, a comparison on size, a function — is taken as "true", which only
// widens a branch. `x in ["a", "b"]` over a pushable field becomes one branch
// per value.
//
// A single empty branch means nothing can be pushed down: either nothing was
// recognised, or some disjunct is unconstrained (which makes the whole OR
// unconstrained), or the expansion passed maxObjectBranches. Callers query
// once per branch and merge; the compiled program stays authoritative.
func ExtractObjectBranches(expr string) ([]ObjectPushdown, error) {
	if expr == "" {
		return []ObjectPushdown{{}}, nil
	}
	env, err := buildEnv(ObjectSchema)
	if err != nil {
		return nil, fmt.Errorf("cel: build env: %w", err)
	}
	ast, iss := env.Parse(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel: parse %q: %w", expr, iss.Err())
	}
	return objectDNF(ast.NativeRep().Expr()), nil
}

// unconstrained is the DNF of "true": one branch that narrows nothing.
func unconstrained() []ObjectPushdown { return []ObjectPushdown{{}} }

func objectDNF(e celast.Expr) []ObjectPushdown {
	if e == nil || e.Kind() != celast.CallKind {
		return unconstrained()
	}
	c := e.AsCall()
	args := c.Args()
	switch c.FunctionName() {
	case "_&&_":
		out := unconstrained()
		for _, a := range args {
			sub := objectDNF(a)
			next := make([]ObjectPushdown, 0, len(out)*len(sub))
			for _, x := range out {
				for _, y := range sub {
					next = append(next, mergePushdown(x, y))
				}
			}
			if len(next) > maxObjectBranches {
				return unconstrained()
			}
			out = next
		}
		return out

	case "_||_":
		var out []ObjectPushdown
		for _, a := range args {
			for _, b := range objectDNF(a) {
				if b.empty() {
					return unconstrained()
				}
				out = append(out, b)
			}
		}
		if len(out) == 0 || len(out) > maxObjectBranches {
			return unconstrained()
		}
		return out

	case "@in":
		if len(args) != 2 || args[1].Kind() != celast.ListKind {
			return unconstrained()
		}
		elems := args[1].AsList().Elements()
		if len(elems) == 0 || len(elems) > maxObjectBranches {
			return unconstrained()
		}
		out := make([]ObjectPushdown, 0, len(elems))
		for _, el := range elems {
			var pd ObjectPushdown
			recogniseObjectEq(args[0], el, &pd)
			if pd.empty() {
				return unconstrained()
			}
			out = append(out, pd)
		}
		return out
	}

	var pd ObjectPushdown
	recogniseObjectLeaf(c, &pd)
	return []ObjectPushdown{pd}
}

func (p ObjectPushdown) empty() bool {
	return p.StateEq == "" && p.KeyPrefix == "" && p.KeyContains == "" &&
		p.ContentTypeEq == "" && p.ContentTypePrefix == "" &&
		len(p.TagsEq) == 0 && len(p.MetadataEq) == 0
}

// mergePushdown is the conjunction of two branches. A field set on both keeps
// a's value — the same first-wins rule the && walk uses, and a superset
// either way.
func mergePushdown(a, b ObjectPushdown) ObjectPushdown {
	out := a
	pick := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	pick(&out.StateEq, b.StateEq)
	pick(&out.KeyPrefix, b.KeyPrefix)
	pick(&out.KeyContains, b.KeyContains)
	pick(&out.ContentTypeEq, b.ContentTypeEq)
	pick(&out.ContentTypePrefix, b.ContentTypePrefix)
	out.TagsEq = mergeMap(a.TagsEq, b.TagsEq)
	out.MetadataEq = mergeMap(a.MetadataEq, b.MetadataEq)
	return out
}

// mergeMap returns a new map so branches sharing a parent never alias.
func mergeMap(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if _, seen := out[k]; !seen {
			out[k] = v
		}
	}
	return out
}
