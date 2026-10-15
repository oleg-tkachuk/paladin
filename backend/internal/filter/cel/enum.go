package cel

import (
	"fmt"
	"sort"
	"strings"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// Values of the SQL enums behind enum-backed filter fields. A filter literal
// for such a field is checked against these at compile time.
//
// Object states come from statemachine, which owns the object_state mirror.
// Operation states and storage layouts are spelled here because their Go
// mirrors live in handler packages that import this one. The adapters tests
// pin every list against the sqlc constants generated from the migration.
var (
	objectStates = []string{
		string(statemachine.StatePending),
		string(statemachine.StateAvailable),
		string(statemachine.StateFailed),
		string(statemachine.StateDeleted),
	}
	operationStates = []string{
		OperationStatePending,
		OperationStateRunning,
		OperationStateSucceeded,
		OperationStateFailed,
		OperationStateCancelled,
	}
	storageLayouts = []string{
		StorageLayoutShared,
		StorageLayoutDedicated,
	}
)

// operation_state values.
const (
	OperationStatePending   = "PENDING"
	OperationStateRunning   = "RUNNING"
	OperationStateSucceeded = "SUCCEEDED"
	OperationStateFailed    = "FAILED"
	OperationStateCancelled = "CANCELLED"
)

// tenant_storage_layout values.
const (
	StorageLayoutShared    = "shared"
	StorageLayoutDedicated = "dedicated"
)

// Schema field names backed by a SQL enum.
const (
	fieldState         = "state"
	fieldStorageLayout = "storage_layout"
)

// CEL function names the enum check inspects.
const (
	fnEquals    = "_==_"
	fnNotEquals = "_!=_"
	fnIn        = "@in"
)

// EnumValues returns the values an enum-backed field accepts, sorted, and
// false when field is not enum-backed in this schema.
func (s *Schema) EnumValues(field string) ([]string, bool) {
	vals, ok := s.enums[field]
	if !ok {
		return nil, false
	}
	out := append([]string(nil), vals...)
	sort.Strings(out)
	return out, true
}

// enumViolation is a string literal compared with an enum-backed field that
// the enum does not hold.
type enumViolation struct {
	exprID   int64
	field    string
	value    string
	accepted []string
}

func (v *enumViolation) message() string {
	return fmt.Sprintf("%s: unknown value %q; accepted values: %s",
		v.field, v.value, strings.Join(v.accepted, ", "))
}

// checkEnums reports the first literal in ast that an enum-backed field of
// schema could never equal.
//
// Such a literal is never a meaningful filter: `==` and `in` match nothing,
// `!=` matches everything. For objects it is worse — the pushdown hands the
// literal to Postgres as an object_state parameter, which fails the whole
// query with 22P02 and reaches the caller as Internal. Rejecting it here
// turns both into InvalidArgument naming the values that exist.
func checkEnums(schema *Schema, ast *cel.Ast) *enumViolation {
	if len(schema.enums) == 0 {
		return nil
	}
	// Every node, not only the top-level conjuncts: a literal under `||`, `!`,
	// a ternary or a macro body is as wrong there as anywhere else.
	var first *enumViolation
	celast.PreOrderVisit(ast.NativeRep().Expr(), celast.NewExprVisitor(func(e celast.Expr) {
		if first == nil && e.Kind() == celast.CallKind {
			first = enumCall(schema, e.AsCall())
		}
	}))
	return first
}

// enumCall checks one call node for `field == lit`, `lit == field`,
// `field != lit` (either order) and `field in [lit, ...]`.
func enumCall(schema *Schema, c celast.CallExpr) *enumViolation {
	args := c.Args()
	if len(args) != 2 {
		return nil
	}
	switch c.FunctionName() {
	case fnEquals, fnNotEquals:
		if v := enumOperand(schema, args[0], args[1]); v != nil {
			return v
		}
		return enumOperand(schema, args[1], args[0])
	case fnIn:
		if args[1].Kind() != celast.ListKind {
			return nil
		}
		for _, el := range args[1].AsList().Elements() {
			if v := enumOperand(schema, args[0], el); v != nil {
				return v
			}
		}
	}
	return nil
}

// enumOperand checks `field <op> lit` with field an enum-backed identifier
// and lit a string literal.
func enumOperand(schema *Schema, fieldExpr, litExpr celast.Expr) *enumViolation {
	field, ok := identName(fieldExpr)
	if !ok {
		return nil
	}
	accepted, ok := schema.EnumValues(field)
	if !ok {
		return nil
	}
	lit, ok := stringLiteral(litExpr)
	if !ok {
		return nil
	}
	for _, a := range accepted {
		if lit == a {
			return nil
		}
	}
	return &enumViolation{exprID: litExpr.ID(), field: field, value: lit, accepted: accepted}
}
