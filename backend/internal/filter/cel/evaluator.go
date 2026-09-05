// Package cel compiles and evaluates AIP-160-style CEL filter expressions
// used by ListObjects, ListCollections, ListTenants, ListOperations.
//
// Separation from Cedar (policy): CEL answers "does this row match?" for
// database queries; Cedar answers "may the caller perform this action?"
// for authorization. Don't conflate them.
package cel

import (
	"fmt"
	"sort"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
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

// Fields returns the variable names this schema declares, sorted.
//
// A filter compiles against the SCHEMA and evaluates against a projection —
// the map a handler builds per row. Nothing connected the two: a schema field
// with no matching key in the projection produces an expression that compiles
// happily and then matches nothing, for every row, silently. That is the worst
// available failure for a filter, because an empty result set is a legitimate
// answer and the caller cannot tell the difference.
//
// Exported so each handler package can assert its own projection covers what
// its schema promises.
func (s *Schema) Fields() []string {
	out := make([]string, 0, len(s.vars))
	for k := range s.vars {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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

// CollectionSchema is exposed to filters against Collection rows.
//
// Named BucketSchema until the resources were renamed; the wire name was
// already "Collection". It had no callers at all — ListCollections accepted a
// filter and dropped it — so this is its first use.
var CollectionSchema = &Schema{
	Name: "Collection",
	vars: map[string]*cel.Type{
		"collection":      cel.StringType,
		"storage_backend": cel.StringType,
		"display_name":    cel.StringType,
		"created_at":      cel.TimestampType,
		// Derived, not a column — see SearchText. One conjunct for a console
		// search box, because a disjunction over the two name columns pushes
		// nothing down and would report matches past the first page as absent.
		"search": cel.StringType,
	},
}

// BucketSchema is the deprecated alias for CollectionSchema.
//
// Deprecated: use CollectionSchema.
var BucketSchema = CollectionSchema

// TenantSchema is exposed to filters against Tenant rows. deleted_at is
// present so an operator can filter the trash view (`deleted_at != null` is
// not expressible — use the include_trashed / only_trashed flags for that —
// but `slug.startsWith("acme")` narrows either view).
var TenantSchema = &Schema{
	Name: "Tenant",
	vars: map[string]*cel.Type{
		"tenant_id":      cel.StringType,
		"slug":           cel.StringType,
		"display_name":   cel.StringType,
		"storage_layout": cel.StringType,
		"labels":         cel.MapType(cel.StringType, cel.StringType),
		"created_at":     cel.TimestampType,
		"updated_at":     cel.TimestampType,
		// Derived, not a column — see SearchText. One conjunct for a search
		// box, because a disjunction over slug and display_name pushes nothing
		// down: the command palette sent exactly that shape and read a single
		// page, so a tenant sorting past the page ceiling could not be found
		// by typing its name.
		"search": cel.StringType,
	},
}

// StorageBackendSchema is exposed to filters against StorageBackend rows.
var StorageBackendSchema = &Schema{
	Name: "StorageBackend",
	vars: map[string]*cel.Type{
		"backend_id":   cel.StringType,
		"display_name": cel.StringType,
		"provider":     cel.StringType,
		"endpoint":     cel.StringType,
		"region":       cel.StringType,
		"enabled":      cel.BoolType,
		"read_only":    cel.BoolType,
		"maintenance":  cel.BoolType,
		"created_at":   cel.TimestampType,
		// Derived, not a column — see SearchText. One conjunct for a console
		// search box, because a disjunction over the two name columns pushes
		// nothing down and would report matches past the first page as absent.
		"search": cel.StringType,
	},
}

// PhysicalBucketSchema is exposed to filters against Bucket rows — the
// physical bucket on a backend, not the Collection namespace above.
var PhysicalBucketSchema = &Schema{
	Name: "Bucket",
	vars: map[string]*cel.Type{
		"bucket_id":           cel.StringType,
		"backend_id":          cel.StringType,
		"display_name":        cel.StringType,
		"versioning_enabled":  cel.BoolType,
		"object_lock_enabled": cel.BoolType,
		"replication_enabled": cel.BoolType,
		"owner_tenant_id":     cel.StringType,
		"created_at":          cel.TimestampType,
		// A derived field, not a column: lower(bucket_id) + "\n" +
		// lower(display_name). It exists so a console search box has ONE
		// conjunct to send.
		//
		// The alternative it replaces is `bucket_id.contains(q) ||
		// display_name.contains(q)`, and that shape is a trap here. The
		// pushdown walker descends `&&` only, so a disjunction pushes nothing;
		// the server then reads a full page, filters it in memory, and answers
		// with the matches THAT PAGE happened to hold. A caller who trusted
		// that answer would be told a bucket does not exist because it sorted
		// past row 500.
		//
		// One conjunct pushes down, so the narrowing happens in SQL and the
		// page the server reads already holds the matches.
		//
		// Case is folded on both sides because the console's client-side
		// search always did (`toLowerCase().includes(…)`), and moving that
		// behaviour to the server without folding would have quietly made
		// search case-sensitive.
		//
		// The separator is "\n" rather than " ": a query can only match across
		// the boundary by containing the separator, and a newline cannot be
		// typed into a search input. So this matches exactly what the browser
		// matched — id OR display name — and nothing else.
		"search": cel.StringType,
	},
}

// OperationSchema is exposed to filters against long-running Operation rows.
var OperationSchema = &Schema{
	Name: "Operation",
	vars: map[string]*cel.Type{
		"operation_id":  cel.StringType,
		"type":          cel.StringType,
		"state":         cel.StringType,
		"done":          cel.BoolType,
		"tenant_id":     cel.StringType,
		"error_code":    cel.StringType,
		"error_message": cel.StringType,
		"created_at":    cel.TimestampType,
		"updated_at":    cel.TimestampType,
	},
}

// UserSchema is exposed to filters against User rows.
var UserSchema = &Schema{
	Name: "User",
	vars: map[string]*cel.Type{
		"user_id":      cel.StringType,
		"tenant_id":    cel.StringType,
		"subject":      cel.StringType,
		"display_name": cel.StringType,
		"disabled":     cel.BoolType,
		"roles":        cel.ListType(cel.StringType),
		"created_at":   cel.TimestampType,
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
// The derived attributes (kind, severity, severity_level, collection,
// bucket_name, etag, size_bytes) are projected from the event's type / resource
// name / Payload by worker.eventCELVars. Absent values default to zero, so a
// filter referencing a field an event doesn't carry evaluates to false/0 rather
// than erroring. severity is the human label ("info"|"warning"|"critical") and
// severity_level its ordered companion (10/30/50) — filter thresholds on the
// number (`severity_level >= 30`), since lexicographic string order is
// meaningless for severity.
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
		// Payload-derived attributes (projected by worker.eventCELVars);
		// absent → zero value.
		"kind":           cel.StringType,
		"severity":       cel.StringType,
		"severity_level": cel.IntType,
		"collection":     cel.StringType,
		"bucket_name":    cel.StringType,
		"etag":           cel.StringType,
		"size_bytes":     cel.IntType,
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
// context (Object | Collection | AuditLogEntry | EventEnvelope). Returns
// nil for unknown names so callers can map to InvalidArgument.
func SchemaByName(name string) *Schema {
	switch name {
	case ObjectSchema.Name:
		return ObjectSchema
	case CollectionSchema.Name:
		return CollectionSchema
	case TenantSchema.Name:
		return TenantSchema
	case StorageBackendSchema.Name:
		return StorageBackendSchema
	case PhysicalBucketSchema.Name:
		return PhysicalBucketSchema
	case OperationSchema.Name:
		return OperationSchema
	case UserSchema.Name:
		return UserSchema
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

// maxCELCacheEntries bounds the compiled-program cache. Each distinct
// (schema, expr) a tenant submits compiles to a few-KB program; without
// a cap an authenticated caller exhausts memory with ever-varying filter
// strings. A few thousand entries dwarfs any real workload's distinct
// filter set, so eviction effectively never fires in practice — it's a
// safety ceiling, not a hot-path tuning knob.
const maxCELCacheEntries = 4096

// Evaluator compiles and caches CEL programs per schema+expression. The
// cache is size-bounded: at capacity a new compile evicts an arbitrary
// existing entry (Go map range order). Strict LRU isn't worth the
// complexity for a compile cache — the goal is bounding memory, and at
// this cap a re-compile after a rare eviction is cheap.
type Evaluator struct {
	mu    sync.Mutex
	cache map[string]cel.Program // key = schema.Name + "\x00" + expr
}

// NewEvaluator returns an Evaluator with empty cache.
func NewEvaluator() *Evaluator {
	return &Evaluator{cache: make(map[string]cel.Program)}
}

// Compile returns a cached Program or compiles a new one for expr under schema.
// An empty expr returns a program that always evaluates to true.
func (e *Evaluator) Compile(schema *Schema, expr string) (cel.Program, error) {
	if expr == "" {
		return alwaysTrueProgram, nil
	}
	key := schema.Name + "\x00" + expr

	e.mu.Lock()
	if prog, ok := e.cache[key]; ok {
		e.mu.Unlock()
		return prog, nil
	}
	e.mu.Unlock()

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

	e.mu.Lock()
	if len(e.cache) >= maxCELCacheEntries {
		// Evict one arbitrary entry to make room. Range yields a
		// pseudo-random key; deleting during range is safe in Go.
		for k := range e.cache {
			delete(e.cache, k)
			break
		}
	}
	e.cache[key] = prog
	e.mu.Unlock()
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
	b, ok := out.Value().(bool)
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

// ─── Page filtering ─────────────────────────────────────────────────────────

// FilterPage applies a CEL expression to one already-fetched page.
//
// This is the shape every List RPC with a `filter` uses: the SQL query decides
// which rows are candidates (optionally narrowed by a pushdown), and the full
// CEL program then decides which of them the caller actually asked for. The
// program is authoritative — pushdown may only narrow, never replace it — so a
// filter that SQL cannot express still returns exactly the right rows.
//
// The page cursor is NOT adjusted here, and callers must return the repo's
// cursor unchanged. A page where every row fails the predicate is a legitimate
// empty page with a next_page_token: the caller keeps paging. Recomputing the
// cursor from the surviving rows would skip everything that was filtered out.
//
// An empty expression returns the page untouched, without compiling anything.
//
// THE PAGE IS FILTERED IN PLACE. The result aliases the caller's backing array
// and the surviving rows are compacted into its front, so the input slice is
// unusable afterwards. Every production caller passes a page it then discards,
// which is why this has never mattered; it cost an afternoon in a test that
// filtered one fixture twice and got a row reported twice and a match reported
// missing. Copy first if the input is needed again.
func FilterPage[T any](
	e *Evaluator, schema *Schema, expr string, page []T, row func(T) map[string]any,
) ([]T, error) {
	if expr == "" {
		return page, nil
	}
	prog, err := e.Compile(schema, expr)
	if err != nil {
		return nil, err
	}
	out := page[:0]
	for i := range page {
		match, err := Match(prog, row(page[i]))
		if err != nil {
			return nil, err
		}
		if match {
			out = append(out, page[i])
		}
	}
	return out, nil
}
