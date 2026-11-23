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
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
)

// Program is re-exported so callers outside this package don't need to import
// cel-go directly just to hold a compiled handle.
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
	// enums holds, per string field backed by a SQL enum, the values that
	// enum accepts. See checkEnums.
	enums map[string][]string
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
	enums: map[string][]string{fieldState: objectStates},
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
		// Derived: trashed, and admin.tenants.trash_retention lapsed — the
		// console's "Past retention". `purge_eligible` alone, with
		// only_trashed or include_trashed, lists the tenants an operator
		// should decide on.
		"purge_eligible": cel.BoolType,
	},
	enums: map[string][]string{fieldStorageLayout: storageLayouts},
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
		// How many objects a batch could not act on; one that ran to its
		// end succeeded whatever it left behind.
		"failed_count": cel.IntType,
		// has_failures is an operation that failed, or a batch that could
		// not act on some objects: what a list of failures shows.
		"has_failures": cel.BoolType,
		"created_at":   cel.TimestampType,
		"updated_at":   cel.TimestampType,
	},
	enums: map[string][]string{fieldState: operationStates},
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
// the most common selector in compliance queries. `search` is the entry's
// text fields folded by SearchText, so a console search box is one
// case-insensitive conjunct rather than a disjunction over columns.
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
		"relayed_by":      cel.StringType,
		"user_agent":      cel.StringType,
		"at":              cel.TimestampType,
		"is_error":        cel.BoolType,
		"search":          cel.StringType,
	},
}

// registered is every schema SchemaByName resolves, in the order
// SchemaNames lists them.
var registered = []*Schema{
	ObjectSchema, CollectionSchema, TenantSchema, StorageBackendSchema,
	PhysicalBucketSchema, OperationSchema, UserSchema, AuditLogSchema,
	EventEnvelopeSchema,
}

// SchemaByName resolves a registered schema by its public name. Used by
// the admin CEL validation RPC to pick the right schema for the caller's
// context. Returns nil for unknown names so callers can map to
// InvalidArgument.
func SchemaByName(name string) *Schema {
	for _, s := range registered {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// SchemaNames lists the name of every schema SchemaByName resolves.
func SchemaNames() []string {
	names := make([]string, 0, len(registered))
	for _, s := range registered {
		names = append(names, s.Name)
	}
	return names
}

// CompileError carries the first compile diagnostic with 1-indexed source
// position (line, column). Returned from CompileWithPosition for callers
// that want to surface line / column to the user (admin CEL validation).
//
// cel-go's Issues.Errors() yields []*cel.Error with Location.Line() /
// .Column() — Line is 1-indexed, Column is 0-indexed in cel-go itself
// but display strings add 1. We normalise to 1-indexed for both.
//
// Message is text a client reads — the validation RPC answers it, the list
// RPCs refuse a filter with it — so it is one printable line: cel-go names the
// token it could not read as the expression holds it, and a control character
// there reached the client raw. Error adds the position, not cel-go's report
// rendered against the source, which echoes the expression line by line.
type CompileError struct {
	Message string
	Line    int
	Column  int
}

func (e *CompileError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%d:%d: %s", e.Line, e.Column, e.Message)
	}
	return e.Message
}

// printable is s with each character that does not print, and each byte that
// is not UTF-8, escaped as a Go string literal escapes it.
func printable(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == ' ' || unicode.IsPrint(r):
			b.WriteRune(r)
		default:
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		}
		i += size
	}
	return b.String()
}

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
	_, _, err := compileChecked(schema, expr)
	return err
}

// compileChecked is the one compile path behind Compile, Validate and
// CompileFirstError, so the admin validation RPC accepts exactly the filters
// the list RPCs accept. Failures are *CompileError with a 1-indexed position
// where one is known.
func compileChecked(schema *Schema, expr string) (*cel.Env, *cel.Ast, error) {
	env, err := buildEnv(schema)
	if err != nil {
		return nil, nil, &CompileError{Message: fmt.Sprintf("build env: %v", err)}
	}
	ast, iss := env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		errs := iss.Errors()
		if len(errs) == 0 {
			return nil, nil, &CompileError{Message: printable(iss.Err().Error())}
		}
		first := errs[0]
		return nil, nil, &CompileError{
			Message: printable(first.Message),
			Line:    first.Location.Line(),
			Column:  first.Location.Column() + 1, // 0→1-indexed
		}
	}
	if ast.OutputType() != cel.BoolType {
		return nil, nil, &CompileError{Message: fmt.Sprintf("expression must return bool, got %s", ast.OutputType())}
	}
	if v := checkEnums(schema, ast); v != nil {
		loc := ast.NativeRep().SourceInfo().GetStartLocation(v.exprID)
		return nil, nil, &CompileError{
			Message: v.message(),
			Line:    loc.Line(),
			Column:  loc.Column() + 1, // 0→1-indexed
		}
	}
	return env, ast, nil
}

// maxCELCacheEntries bounds the compiled-program cache. Each distinct
// (schema, expr) a tenant submits compiles to a few-KB program; without
// a cap an authenticated caller exhausts memory with ever-varying filter
// strings. A few thousand entries dwarfs any real workload's distinct
// filter set, so eviction effectively never fires in practice — it's a
// safety ceiling, not a hot-path tuning knob.
const maxCELCacheEntries = 4096

// maxEvalCost bounds what evaluating one compiled filter against one row may
// cost, in cel-go's cost units (roughly one per operation). A filter is the
// caller's text and is evaluated on every row of a page, and a few hundred
// bytes of nested comprehensions over literal lists cost billions — seconds
// to minutes per row with no bound. A real filter is a handful of
// comparisons and costs tens; the ceiling leaves orders of magnitude for
// map and list macros over a row's tags and holds one row to milliseconds.
// Evaluation past it fails, which every list RPC answers as InvalidArgument
// and every background matcher as a skip.
const maxEvalCost = 100_000

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

	env, ast, err := compileChecked(schema, expr)
	if err != nil {
		return nil, fmt.Errorf("cel: compile %q: %w", expr, err)
	}
	prog, err := env.Program(ast, cel.CostLimit(maxEvalCost))
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
	if _, _, err := compileChecked(schema, expr); err != nil {
		return fmt.Errorf("cel: compile %q: %w", expr, err)
	}
	return nil
}

// Match runs a compiled program against a row represented as a map.
func Match(prog cel.Program, row map[string]any) (bool, error) {
	out, _, err := prog.Eval(rowActivation(row))
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
