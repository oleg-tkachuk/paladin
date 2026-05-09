// Package cel compiles and evaluates AIP-160-style CEL filter expressions
// used by ListObjects, ListObjectKeys, ListTenants, ListOperations.
//
// Separation from Cedar (policy): CEL answers "does this row match?" for
// database queries; Cedar answers "may the caller perform this action?"
// for authorization. Don't conflate them.
package cel

import (
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// Program is re-exported so callers outside this package don't need to import
// google/cel-go directly just to hold a compiled handle.
type Program = cel.Program

// Schema describes the variable names and types exposed to filter authors.
//
// Example: for ObjectSchema, a user can write:
//
//	state == "AVAILABLE" && size_bytes > 1048576 && tags["type"] == "invoice"
type Schema struct {
	Name string
	// Declarations are returned by buildEnv — a concrete wiring lives below.
	vars map[string]*cel.Type
}

// ObjectSchema is exposed to filters against Object rows.
var ObjectSchema = &Schema{
	Name: "Object",
	vars: map[string]*cel.Type{
		"state":        cel.StringType,
		"key":          cel.StringType,
		"content_type": cel.StringType,
		"size_bytes":   cel.IntType,
		"tags":         cel.MapType(cel.StringType, cel.StringType),
		"metadata":     cel.MapType(cel.StringType, cel.StringType),
		"created_at":   cel.TimestampType,
		"updated_at":   cel.TimestampType,
		"committed_at": cel.TimestampType,
		"external_ref": cel.StringType,
	},
}

// BucketSchema is exposed to filters against ObjectKey rows.
var BucketSchema = &Schema{
	Name: "ObjectKey",
	vars: map[string]*cel.Type{
		"objectKey":       cel.StringType,
		"storage_backend": cel.StringType,
		"display_name":    cel.StringType,
		"created_at":      cel.TimestampType,
	},
}

// EventEnvelopeSchema is exposed to filters on EventSubscription rows.
//
// The runtime envelope delivered by worker.Dispatcher (see
// internal/worker/event_dispatcher.go `Event` struct) carries a fixed
// JSON shape: type, at, tenant_id, resource_name, actor_subject. The
// CloudEvents passthrough adds id / source / specversion / time /
// datacontenttype / subject when the publisher emits a CE 1.0 envelope
// (see internal/eventingest/source_cloudevents.go).
//
// Only fields actually present at evaluation time are declared. The
// admin UI references richer attributes (`kind`, `severity`,
// `size_bytes`, `etag`, `bucket_name`, `object_key`) in placeholder
// hints — those are aspirational; declaring them here would let users
// write filters that compile but always evaluate to undefined / false.
// Add them to the schema only when the dispatcher actually populates
// them (slice-N follow-up).
var EventEnvelopeSchema = &Schema{
	Name: "EventEnvelope",
	vars: map[string]*cel.Type{
		// Dispatched runtime fields (worker.Event → JSON).
		"type":          cel.StringType, // "paladin.object.uploaded", ...
		"at":            cel.TimestampType,
		"tenant_id":     cel.StringType,
		"resource_name": cel.StringType,
		"actor_subject": cel.StringType,
		// CloudEvents 1.0 envelope attributes — populated when the
		// publisher uses the CE source adapter.
		"id":              cel.StringType,
		"source":          cel.StringType,
		"specversion":     cel.StringType,
		"time":            cel.StringType, // RFC3339 string per CE spec
		"datacontenttype": cel.StringType,
		"subject":         cel.StringType,
	},
}

// AuditLogSchema is exposed to filters against AuditLogEntry rows
// (ListAuditLog, ExportAuditLog). `is_error` is a derived bool — true
// when the entry has a non-empty error_message — exposed because it's
// the most common selector in compliance queries.
var AuditLogSchema = &Schema{
	Name: "AuditLogEntry",
	vars: map[string]*cel.Type{
		"actor_subject":   cel.StringType,
		"actor_tenant_id": cel.StringType,
		"actor_audience":  cel.StringType,
		"action":          cel.StringType,
		"resource_name":   cel.StringType,
		"request_id":      cel.StringType,
		"source_ip":       cel.StringType,
		"at":              cel.TimestampType,
		"is_error":        cel.BoolType,
	},
}

// SchemaByName resolves a registered schema by its public name. Used by
// the admin CEL validation RPC to pick the right schema for the caller's
// context (Object | ObjectKey | AuditLogEntry | EventEnvelope). Returns
// nil for unknown names so callers can map to InvalidArgument.
func SchemaByName(name string) *Schema {
	switch name {
	case ObjectSchema.Name:
		return ObjectSchema
	case BucketSchema.Name:
		return BucketSchema
	case AuditLogSchema.Name:
		return AuditLogSchema
	case EventEnvelopeSchema.Name:
		return EventEnvelopeSchema
	default:
		return nil
	}
}

// CompileError carries the first compile diagnostic with 1-indexed source
// position (line, column). Returned from CompileWithPosition for callers
// that want to surface line / column to the user (admin CEL validation).
//
// cel-go's Issues.Errors() yields []*cel.Error with Location.Line() /
// .Column() — Line is 1-indexed, Column is 0-indexed in cel-go itself
// but display strings add 1. We normalise to 1-indexed for both.
type CompileError struct {
	Message string
	Line    int
	Column  int
}

func (e *CompileError) Error() string { return e.Message }

// CompileFirstError compiles `expr` against `schema` and, on failure,
// returns a *CompileError carrying the first diagnostic's position.
// Used by admin CELService.Validate to populate line/column in the
// response without leaking the cel-go error formatting.
//
// An empty expression is valid (returns nil) — same sentinel as
// Validate.
func CompileFirstError(schema *Schema, expr string) error {
	if expr == "" {
		return nil
	}
	env, err := buildEnv(schema)
	if err != nil {
		return &CompileError{Message: fmt.Sprintf("build env: %v", err)}
	}
	ast, iss := env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		errs := iss.Errors()
		if len(errs) == 0 {
			return &CompileError{Message: iss.Err().Error()}
		}
		first := errs[0]
		return &CompileError{
			Message: first.Message,
			Line:    first.Location.Line(),
			Column:  first.Location.Column() + 1, // 0→1-indexed
		}
	}
	if ast.OutputType() != cel.BoolType {
		return &CompileError{Message: fmt.Sprintf("expression must return bool, got %s", ast.OutputType())}
	}
	return nil
}

// Evaluator compiles and caches CEL programs per schema+expression.
type Evaluator struct {
	cache sync.Map // key = schema.Name + "\x00" + expr; val = cel.Program
}

// NewEvaluator returns an Evaluator with empty cache.
func NewEvaluator() *Evaluator {
	return &Evaluator{}
}

// Compile returns a cached Program or compiles a new one for expr under schema.
// An empty expr returns a program that always evaluates to true.
func (e *Evaluator) Compile(schema *Schema, expr string) (cel.Program, error) {
	if expr == "" {
		return alwaysTrueProgram, nil
	}
	key := schema.Name + "\x00" + expr
	if v, ok := e.cache.Load(key); ok {
		return v.(cel.Program), nil
	}

	env, err := buildEnv(schema)
	if err != nil {
		return nil, fmt.Errorf("cel: build env: %w", err)
	}
	ast, iss := env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("cel: compile %q: %w", expr, iss.Err())
	}
	if ast.OutputType() != cel.BoolType {
		return nil, fmt.Errorf("cel: expression %q must return bool, got %s", expr, ast.OutputType())
	}
	prog, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("cel: program: %w", err)
	}
	e.cache.Store(key, prog)
	return prog, nil
}

// Validate compiles `expr` against `schema` and discards the resulting
// program. Returns nil iff the expression parses, type-checks against the
// schema vars, and yields a bool. Used by write paths that want to reject
// malformed CEL synchronously rather than discovering the failure from a
// background worker hours later (see worker.lifecycle compile-on-tick).
//
// An empty expression is valid — it's the "match everything" sentinel.
func Validate(schema *Schema, expr string) error {
	if expr == "" {
		return nil
	}
	env, err := buildEnv(schema)
	if err != nil {
		return fmt.Errorf("cel: build env: %w", err)
	}
	ast, iss := env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return fmt.Errorf("cel: compile %q: %w", expr, iss.Err())
	}
	if ast.OutputType() != cel.BoolType {
		return fmt.Errorf("cel: expression %q must return bool, got %s", expr, ast.OutputType())
	}
	return nil
}

// Match runs a compiled program against a row represented as a map.
func Match(prog cel.Program, row map[string]any) (bool, error) {
	out, _, err := prog.Eval(row)
	if err != nil {
		return false, fmt.Errorf("cel: eval: %w", err)
	}
	b, ok := out.(ref.Val).Value().(bool)
	if !ok {
		return false, fmt.Errorf("cel: expression did not yield bool")
	}
	return b, nil
}

func buildEnv(schema *Schema) (*cel.Env, error) {
	opts := make([]cel.EnvOption, 0, len(schema.vars))
	for name, typ := range schema.vars {
		opts = append(opts, cel.Variable(name, typ))
	}
	return cel.NewEnv(opts...)
}

// alwaysTrueProgram is lazily initialized and shared.
var alwaysTrueProgram cel.Program

func init() {
	env, err := cel.NewEnv()
	if err != nil {
		panic(err)
	}
	ast, iss := env.Compile("true")
	if iss != nil && iss.Err() != nil {
		panic(iss.Err())
	}
	prog, err := env.Program(ast)
	if err != nil {
		panic(err)
	}
	alwaysTrueProgram = prog
	_ = types.Bool(true) // keep types pulled in
}
