// Field-mapping coverage across every Create*/Update* shim.
//
// Failure mode this catches: a new field lands on `CreateXxxRequest`
// in the .proto, buf gen runs, every package compiles — and the
// shim silently drops the field because the human writing the
// shim forgot to add `args.NewField = m.GetNewField()`. The
// existing field_mapping_test.go (admin/field_mapping_test.go)
// caught this for CreateTenant after the Phase 0 slug regression,
// but only for that one RPC. This test generalises the check.
//
// Mechanism (static, no handler instantiation required):
//
//  1. Walk every shim file under internal/api/connectshim/{admin,
//     iam,data} with go/ast.
//  2. For each method whose name starts with `Create` or `Update`,
//     extract the proto Request type from the second parameter
//     (`*connect.Request[pb.CreateXxxRequest]`).
//  3. Look the type up in the global protoregistry, enumerate its
//     fields plus one level of nested-message fields (the common
//     `CreateXxxRequest{ Xxx <inner> }` shape).
//  4. For each leaf field, compute the Go getter (`Get<Field>()`)
//     and assert the substring appears somewhere in the shim file's
//     source.
//
// What it does NOT catch:
//   - Fields that are read via direct field access (`req.Msg.Slug`
//     instead of `req.Msg.GetSlug()`). Project convention is to use
//     the getter everywhere; the existing coverage_test.go would
//     not catch this either.
//   - Field values mishandled after extraction (typo'd assignment,
//     wrong type coercion). Per-RPC tests like the existing
//     field_mapping_test.go cover that.
//
// Skip list at the bottom of the file documents intentional drops.

package connectshim_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	// Side-effect imports: registers every proto descriptor in
	// the global registry so FindMessageByName below succeeds.
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
)

// shimPlanes lists every (filesystem dir, proto package) pair we
// audit. The proto package is what we prepend to the bare type name
// scraped out of the AST to form a FullName.
var shimPlanes = []struct {
	dir     string
	pkgName string // proto FullName prefix
}{
	{"admin", "paladin.admin.v1"},
	{"iam", "paladin.iam.v1"},
	{"data", "paladin.data.v1"},
}

// skipFields enumerates (rpc-method-name, field-path) pairs that the
// shim intentionally does NOT forward. Adding an entry requires a
// real reason — there's no "drift acceptance" path.
var skipFields = map[string]map[string]string{
	"CreateBucket": {
		// Lifecycle is managed via the dedicated SetLifecycleRules
		// RPC (see bucket_server.go) — keeping create lean and
		// avoiding a multi-table tx on the hot path. UI sends an
		// empty array on create then issues a follow-up
		// SetLifecycleRules when the user configures rules.
		"LifecycleRules": "managed via SetLifecycleRules RPC",
		// ObjectLock is part of the bucket's at-rest config and
		// flows through admindomain.Bucket.Constraints rather than
		// a top-level field on CreateBucketInput.
		"ObjectLock": "wired via Bucket.Constraints; no top-level read needed",
		// ProvisionState is a server-computed status field; the
		// client never sets it on create.
		"ProvisionState": "server-computed (provision/active/error); not client-settable",
	},
	"UpdateBucket": {
		"LifecycleRules": "managed via SetLifecycleRules RPC",
		"ObjectLock":     "wired via Bucket.Constraints; no top-level read needed",
		"ProvisionState": "server-computed; not client-settable on update",
	},
	"CreateBackend": {
		// Grace-window state is set only by RotateCredentials (with its own
		// RPC), never on create/update — read-only on the StorageBackend.
		"PreviousCredentialsSecretRef":  "set by RotateCredentials; not client-settable",
		"PreviousCredentialsValidUntil": "set by RotateCredentials; not client-settable",
		// Health is derived from TestBackend probes (migration 048), never
		// client-set — output-only on the StorageBackend resource.
		"HealthStatus":    "derived from TestBackend probe; not client-settable",
		"HealthMessage":   "derived from TestBackend probe; not client-settable",
		"HealthCheckedAt": "derived from TestBackend probe; not client-settable",
		// Maintenance is set via SetBackendMaintenance (its own RPC), never on
		// create/update — operator-managed flag on the StorageBackend.
		"Maintenance": "set via SetBackendMaintenance; not client-settable on create/update",
	},
	"UpdateBackend": {
		"PreviousCredentialsSecretRef":  "set by RotateCredentials; not client-settable",
		"PreviousCredentialsValidUntil": "set by RotateCredentials; not client-settable",
		"HealthStatus":                  "derived from TestBackend probe; not client-settable",
		"HealthMessage":                 "derived from TestBackend probe; not client-settable",
		"HealthCheckedAt":               "derived from TestBackend probe; not client-settable",
		"Maintenance":                   "set via SetBackendMaintenance; not client-settable on create/update",
	},
	"CreateObjectKey": {
		// completion_mode is derived from the bucket → backend
		// events config (see proto comment on the field). Server
		// computes it; clients can't override.
		"CompletionMode": "derived from bucket→backend events; server-computed",
	},
	"UpdateObjectKey": {
		"CompletionMode": "derived from bucket→backend events; server-computed",
	},
}

// allowedDirectAccess covers shims that pull fields via Msg.Foo
// instead of GetFoo(). We don't expect any today; if one slips in,
// the test fails and the operator either adds the getter or
// updates this list with a justification.
var allowedDirectAccess = map[string]bool{}

func TestShimFieldMappingCoverage(t *testing.T) {
	root := repoBackendRoot(t)

	type missing struct {
		Method    string
		Field     string
		ShimFile  string
		ProtoName string
	}
	var misses []missing

	for _, plane := range shimPlanes {
		dir := filepath.Join(root, "internal", "api", "connectshim", plane.dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		// Build a package-wide source corpus. Nested-message reads
		// in CreateXxx are typically delegated to a converter in
		// conv.go (e.g. `backendFromProto(m.GetBackend())`), so
		// the getter for sub-fields like `Kind`, `Endpoint`, etc.
		// lives in the converter file, not the per-method shim.
		// Searching the whole package source closes that gap
		// without forcing a refactor.
		var corpus strings.Builder
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			if strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			corpus.Write(b)
			corpus.WriteByte('\n')
		}
		src := corpus.String()
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), "_server.go") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			fset := token.NewFileSet()
			af, err := parser.ParseFile(fset, path, body, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, decl := range af.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil {
					continue
				}
				name := fn.Name.Name
				if !strings.HasPrefix(name, "Create") && !strings.HasPrefix(name, "Update") {
					continue
				}
				reqType := extractRequestType(fn)
				if reqType == "" {
					continue
				}
				full := protoreflect.FullName(plane.pkgName + "." + reqType)
				desc, err := protoregistry.GlobalFiles.FindDescriptorByName(full)
				if err != nil {
					t.Errorf("%s: cannot resolve proto %s: %v", e.Name(), full, err)
					continue
				}
				msg, ok := desc.(protoreflect.MessageDescriptor)
				if !ok {
					t.Errorf("%s: %s is not a message descriptor", e.Name(), full)
					continue
				}
				for _, leaf := range enumerateLeafGetters(msg) {
					if skipped, ok := skipFields[name][leaf]; ok {
						t.Logf("%s: skipping %s — %s", name, leaf, skipped)
						continue
					}
					// Match either the canonical getter (Get<Field>(...)
					// where the `(` rules out accidental substring hits
					// like `GetSlugPrefix`) or, if explicitly allowed,
					// direct field access.
					needle := "Get" + leaf + "("
					if !strings.Contains(src, needle) && !allowedDirectAccess[name+"."+leaf] {
						misses = append(misses, missing{
							Method:    name,
							Field:     leaf,
							ShimFile:  e.Name(),
							ProtoName: string(full),
						})
					}
				}
			}
		}
	}

	if len(misses) == 0 {
		return
	}
	sort.Slice(misses, func(i, j int) bool {
		if misses[i].ShimFile != misses[j].ShimFile {
			return misses[i].ShimFile < misses[j].ShimFile
		}
		if misses[i].Method != misses[j].Method {
			return misses[i].Method < misses[j].Method
		}
		return misses[i].Field < misses[j].Field
	})
	var b strings.Builder
	b.WriteString("Connectshim is missing getters for proto fields:\n\n")
	for _, m := range misses {
		b.WriteString("  ")
		b.WriteString(m.ShimFile)
		b.WriteString(" :: ")
		b.WriteString(m.Method)
		b.WriteString("  proto=")
		b.WriteString(m.ProtoName)
		b.WriteString("  missing=Get")
		b.WriteString(m.Field)
		b.WriteString("\n")
	}
	b.WriteString("\nEither thread the field through to the handler args, ")
	b.WriteString("or — if the drop is intentional — add it to ")
	b.WriteString("skipFields in mapping_test.go with a justification.")
	t.Fatal(b.String())
}

// extractRequestType pulls the proto type name out of a shim method
// signature. The second parameter is always
// `*connect.Request[pb.<TypeName>]`. Returns the bare type name
// (e.g. "CreateTenantRequest") or "" if the signature doesn't match.
func extractRequestType(fn *ast.FuncDecl) string {
	if fn.Type == nil || fn.Type.Params == nil || len(fn.Type.Params.List) < 2 {
		return ""
	}
	// fn.Type.Params.List[1] is the connect.Request param.
	param := fn.Type.Params.List[1]
	star, ok := param.Type.(*ast.StarExpr)
	if !ok {
		return ""
	}
	// `connect.Request[pb.XxxRequest]` is an IndexExpr (Go generics).
	idx, ok := star.X.(*ast.IndexExpr)
	if !ok {
		return ""
	}
	sel, ok := idx.Index.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return sel.Sel.Name
}

// outputOnlyFields are proto field names that, by project convention,
// the server fills on the response side and never expects to receive
// from the client. They appear on Create/Update requests because the
// nested resource message (e.g. `Tenant`, `Bucket`, `StorageBackend`)
// is reused for both directions; the shim deliberately ignores them
// on input. Listing them here keeps the per-RPC skipFields map short.
var outputOnlyFields = map[string]bool{
	"CreatedAt":       true,
	"UpdatedAt":       true,
	"DeletedAt":       true,
	"ResourceVersion": true,
	// `Name` is the canonical resource-name field on most messages
	// (`tenants/<id>`, `storageBackends/<id>/buckets/<name>`). On
	// Update/Delete it comes via the *Request.Name field (which IS
	// checked); on Create the nested resource's `name` is
	// server-derived from other inputs and never read from the
	// request payload.
	"Name": true,
}

// enumerateLeafGetters walks the message descriptor and returns the
// Go getter base names (`Slug`, `DisplayName`, `Tenant.Slug`, …) for
// every field that needs to be forwarded by the shim.
//
// Rules:
//   - Top-level scalar / repeated / map fields: getter is the
//     CamelCase Go name. We use the descriptor's TextName converted
//     via protoreflect.Name -> Go camel naming.
//   - Top-level message fields: we still require the getter for the
//     wrapper (the shim must at least know the sub-message exists);
//     additionally we descend one level and require getters for each
//     sub-field. That's where the original `Slug` regression lived
//     (inside the nested Tenant message).
//   - We don't descend deeper than one level — sub-sub-messages are
//     usually domain-internal types whose own field-by-field shim
//     handling is rare. If you have a Create RPC with deep nesting,
//     extend this function explicitly.
func enumerateLeafGetters(msg protoreflect.MessageDescriptor) []string {
	var out []string
	fields := msg.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		topName := goFieldName(f)
		if !outputOnlyFields[topName] {
			out = append(out, topName)
		}
		if f.Kind() == protoreflect.MessageKind && f.Cardinality() != protoreflect.Repeated && !f.IsMap() {
			sub := f.Message()
			// Don't descend into well-known types or
			// cross-package referents — those are handled by
			// dedicated converters (timestampToProto, etc.).
			if strings.HasPrefix(string(sub.FullName()), "google.protobuf.") {
				continue
			}
			subFields := sub.Fields()
			for j := 0; j < subFields.Len(); j++ {
				name := goFieldName(subFields.Get(j))
				if outputOnlyFields[name] {
					continue
				}
				out = append(out, name)
			}
		}
	}
	return out
}

// goFieldName converts a proto field descriptor's snake_case name
// (`display_name`) to the protoc-gen-go CamelCase getter base
// (`DisplayName`). Matches the camelCasing protoc-gen-go applies:
// underscores delimit words, each becomes a leading-capital.
func goFieldName(f protoreflect.FieldDescriptor) string {
	parts := strings.Split(string(f.Name()), "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}

// repoBackendRoot resolves the backend module root from the test's
// working directory. `go test` runs with cwd = package dir, so the
// connectshim_test cwd is .../backend/internal/api/connectshim;
// climb three levels.
func repoBackendRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	// .../backend/internal/api/connectshim → .../backend
	return filepath.Clean(filepath.Join(cwd, "..", "..", ".."))
}
