package cel

import (
	"fmt"
	"maps"
	"slices"

	celast "cel.dev/cel-go/common/ast"
)

// maxPushdownBranches caps how many disjuncts a filter is split into, for
// objects and for every other list. Each branch is one indexed query, so a
// filter that expands past this is cheaper to scan than to fan out; it then
// pushes nothing down, which is always correct.
const maxPushdownBranches = 8

// ExtractPushdownBranches is ExtractPushdown for filters with `||`, over any
// Schema: the SQL-expressible part of expr in disjunctive normal form, one
// Pushdown per disjunct, the union of whose matches holds every row the full
// expression accepts. Callers run the list query once per branch and merge;
// the compiled program stays authoritative.
//
// A disjunction of equalities on one field stays one branch, as the `&&` walk
// already pushes it as a value set (`= ANY`); so does `field in [...]`. Only a
// disjunction across fields, or over prefixes and the like, fans out.
//
// A single empty branch means nothing can be pushed down: nothing was
// recognised, a disjunct is unconstrained (which makes the whole OR so), or
// the expansion passed maxPushdownBranches. A parse error is the caller's to
// ignore, as for ExtractPushdown.
func ExtractPushdownBranches(schema *Schema, expr string) ([]Pushdown, error) {
	if expr == "" || schema == nil {
		return unconstrainedBranches(), nil
	}
	env, err := buildEnv(schema)
	if err != nil {
		return nil, fmt.Errorf("cel: build env: %w", err)
	}
	ast, iss := env.Parse(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel: parse %q: %w", expr, iss.Err())
	}
	return pushdownDNF(ast.NativeRep().Expr(), schema), nil
}

// unconstrainedBranches is the DNF of "true": one branch narrowing nothing.
func unconstrainedBranches() []Pushdown { return []Pushdown{{}} }

func pushdownDNF(e celast.Expr, schema *Schema) []Pushdown {
	if e != nil && e.Kind() == celast.CallKind {
		switch e.AsCall().FunctionName() {
		case "_&&_":
			return conjunctionDNF(e.AsCall().Args(), schema)
		case "_||_":
			return disjunctionDNF(e, schema)
		}
	}
	var p Pushdown
	p.walk(e, schema)
	return []Pushdown{p}
}

// conjunctionDNF distributes a conjunction over its operands' branches.
func conjunctionDNF(args []celast.Expr, schema *Schema) []Pushdown {
	out := unconstrainedBranches()
	for _, a := range args {
		sub := pushdownDNF(a, schema)
		next := make([]Pushdown, 0, len(out)*len(sub))
		for _, x := range out {
			for _, y := range sub {
				next = append(next, x.and(y))
			}
		}
		if len(next) > maxPushdownBranches {
			return unconstrainedBranches()
		}
		out = next
	}
	return out
}

// disjunctionDNF is the union of a disjunction's branches, or one value set
// when every arm is an equality on the same field.
func disjunctionDNF(e celast.Expr, schema *Schema) []Pushdown {
	var sameField Pushdown
	sameField.recogniseDisjunction(e, schema)
	if !sameField.empty() {
		return []Pushdown{sameField}
	}
	var out []Pushdown
	for _, a := range e.AsCall().Args() {
		for _, b := range pushdownDNF(a, schema) {
			if b.empty() {
				return unconstrainedBranches()
			}
			out = append(out, b)
		}
	}
	if len(out) == 0 || len(out) > maxPushdownBranches {
		return unconstrainedBranches()
	}
	return out
}

// empty reports whether p narrows nothing.
func (p Pushdown) empty() bool {
	return len(p.In) == 0 && len(p.Neq) == 0 && len(p.BoolEq) == 0 &&
		len(p.Prefix) == 0 && len(p.Contains) == 0 &&
		len(p.TimeGTE) == 0 && len(p.TimeLTE) == 0 && len(p.MapEq) == 0
}

// and is the conjunction of two branches, in new maps so that branches
// sharing a parent never alias. A field set on both keeps p's predicate: the
// first-wins rule of the `&&` walk, and a superset of the rows either way.
func (p Pushdown) and(q Pushdown) Pushdown {
	out := Pushdown{
		In:       cloneMerged(p.In, q.In, slices.Clone[[]string]),
		Neq:      cloneMerged(p.Neq, q.Neq, same[string]),
		BoolEq:   cloneMerged(p.BoolEq, q.BoolEq, same[bool]),
		Prefix:   cloneMerged(p.Prefix, q.Prefix, same[string]),
		Contains: cloneMerged(p.Contains, q.Contains, same[string]),
		TimeGTE:  cloneMerged(p.TimeGTE, q.TimeGTE, same),
		TimeLTE:  cloneMerged(p.TimeLTE, q.TimeLTE, same),
	}
	out.MapEq = cloneMerged(p.MapEq, nil, maps.Clone[map[string]string])
	for field, entries := range q.MapEq {
		if out.MapEq == nil {
			out.MapEq = map[string]map[string]string{}
		}
		out.MapEq[field] = cloneMerged(out.MapEq[field], entries, same[string])
	}
	return out
}

func same[V any](v V) V { return v }

// cloneMerged returns a new map holding a's entries, then b's for keys a
// lacks, each value passed through clone; nil when both are empty.
func cloneMerged[V any](a, b map[string]V, clone func(V) V) map[string]V {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]V, len(a)+len(b))
	for k, v := range a {
		out[k] = clone(v)
	}
	for k, v := range b {
		if _, seen := out[k]; !seen {
			out[k] = clone(v)
		}
	}
	return out
}
