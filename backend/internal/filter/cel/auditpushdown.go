package cel

import (
	"fmt"
	"time"

	celgo "cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/types/ref"
)

// AuditPushdown carries the SQL-expressible subset of a CEL filter
// extracted from an AuditLogEntry CEL expression. Empty / zero fields
// mean "no SQL predicate"; the audit handler still runs the full CEL
// program in-memory after the SQL fetch, so pushdown only narrows the
// candidate set — it never changes which rows match.
type AuditPushdown struct {
	ActionEq       string
	ActionPrefix   string
	ActorSubjectEq string
	AtGTE          time.Time
	AtLTE          time.Time
}

// ExtractAuditPushdown parses expr against AuditLogSchema and walks the
// top-level `&&` chain looking for predicates the SQL layer can express.
// The returned AuditPushdown is purely a narrowing hint; the caller
// MUST still evaluate the full CEL program in-memory on the fetched
// page so disjunctions, residual conjuncts, and unrecognised shapes
// remain authoritative.
//
// A parse error here is non-fatal: the audit handler retries the
// in-memory CEL path with the same expression and surfaces the parse
// error there. Callers that want to surface the error early can pass
// the expression through Validate first.
//
// Recognised shapes (operators on top-level `&&` conjuncts):
//
//	action == "<literal>"
//	action.startsWith("<literal>")
//	actor_subject == "<literal>"
//	at >= timestamp("<rfc3339>")           // also `>` (treated as `>=`)
//	at <= timestamp("<rfc3339>")           // also `<` (treated as `<=`)
//
// Conservative on the strict-vs-non-strict comparison: a row that
// would be filtered by `at > X` but kept by `at >= X` is still
// rejected by the in-memory CEL eval, so widening the SQL bound is
// safe. The reverse — narrowing past what CEL would accept — would
// drop matches and is forbidden.
func ExtractAuditPushdown(expr string) (AuditPushdown, error) {
	if expr == "" {
		return AuditPushdown{}, nil
	}
	env, err := buildEnv(AuditLogSchema)
	if err != nil {
		return AuditPushdown{}, fmt.Errorf("cel: build env: %w", err)
	}
	ast, iss := env.Parse(expr)
	if iss != nil && iss.Err() != nil {
		return AuditPushdown{}, fmt.Errorf("cel: parse %q: %w", expr, iss.Err())
	}
	return walkAuditAST(env, ast.NativeRep().Expr()), nil
}

// walkAuditAST descends through top-level `_&&_` conjunctions and tries
// to recognise each leaf. Unrecognised leaves are silently skipped
// (the in-memory CEL pass still enforces them).
func walkAuditAST(_ *celgo.Env, e celast.Expr) AuditPushdown {
	var out AuditPushdown
	walkAuditConjuncts(e, &out)
	return out
}

func walkAuditConjuncts(e celast.Expr, out *AuditPushdown) {
	if e == nil {
		return
	}
	if e.Kind() == celast.CallKind {
		c := e.AsCall()
		if c.FunctionName() == "_&&_" {
			for _, a := range c.Args() {
				walkAuditConjuncts(a, out)
			}
			return
		}
		recogniseAuditLeaf(c, out)
	}
}

// recogniseAuditLeaf attempts to match a single CEL call expression
// against one of the supported shapes and, on success, populates the
// matching AuditPushdown field.
//
// First-match wins: each AuditPushdown field can only carry one
// value; a second `action == "x"` after the first is discarded (the
// in-memory CEL eval would reject the row anyway, since two distinct
// equality predicates on the same field can't both be true).
func recogniseAuditLeaf(c celast.CallExpr, out *AuditPushdown) {
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
		switch field {
		case "action":
			if out.ActionEq == "" {
				out.ActionEq = lit
				return
			}
		case "actor_subject":
			if out.ActorSubjectEq == "" {
				out.ActorSubjectEq = lit
				return
			}
		}

	case "startsWith":
		// Receiver-call: target is the receiver. `action.startsWith("X")`
		// → target = action, args = [literal "X"].
		field, ok := identName(c.Target())
		if !ok || field != "action" {
			return
		}
		args := c.Args()
		if len(args) != 1 {
			return
		}
		lit, ok := stringLiteral(args[0])
		if !ok {
			return
		}
		if out.ActionPrefix == "" {
			out.ActionPrefix = lit
		}

	case "_>=_", "_>_":
		args := c.Args()
		if len(args) != 2 {
			return
		}
		field, ok := identName(args[0])
		if !ok || field != "at" {
			return
		}
		ts, ok := timestampLiteral(args[1])
		if !ok {
			return
		}
		if out.AtGTE.IsZero() {
			out.AtGTE = ts
		}

	case "_<=_", "_<_":
		args := c.Args()
		if len(args) != 2 {
			return
		}
		field, ok := identName(args[0])
		if !ok || field != "at" {
			return
		}
		ts, ok := timestampLiteral(args[1])
		if !ok {
			return
		}
		if out.AtLTE.IsZero() {
			out.AtLTE = ts
		}
	}
}

func identName(e celast.Expr) (string, bool) {
	if e == nil || e.Kind() != celast.IdentKind {
		return "", false
	}
	return e.AsIdent(), true
}

func stringLiteral(e celast.Expr) (string, bool) {
	if e == nil || e.Kind() != celast.LiteralKind {
		return "", false
	}
	v, ok := e.AsLiteral().Value().(string)
	return v, ok
}

// timestampLiteral handles the canonical `timestamp("RFC3339")` call.
// CEL also accepts implicit-conversion timestamp literals in some
// dialects, but the AuditLogSchema doesn't enable any extension that
// would change that — the call form is the only stable shape.
func timestampLiteral(e celast.Expr) (time.Time, bool) {
	if e == nil || e.Kind() != celast.CallKind {
		return time.Time{}, false
	}
	c := e.AsCall()
	if c.FunctionName() != "timestamp" {
		return time.Time{}, false
	}
	args := c.Args()
	if len(args) != 1 {
		return time.Time{}, false
	}
	s, ok := stringLiteral(args[0])
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// RFC3339Nano accepts everything RFC3339 does plus fractional
		// seconds; a parse failure here means a malformed literal —
		// skip the predicate and let in-memory CEL surface the error
		// when it tries to evaluate the call.
		return time.Time{}, false
	}
	return t, true
}

// _ keeps the ref import live; ref.Val is used transitively via
// celast.Expr.AsLiteral().
var _ ref.Val = nil
