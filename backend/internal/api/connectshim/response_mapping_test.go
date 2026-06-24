// Response-direction field-mapping coverage — the mirror of
// TestShimFieldMappingCoverage (which guards Create/Update *inputs*).
//
// Failure mode this catches: a new field lands on a *response* proto
// message (e.g. `Bucket`, `StorageBackend`, `Quota`), buf gen runs,
// everything compiles — and the `xToProto` converter silently leaves it
// at the zero value because the human writing the converter forgot a
// `NewField: domain.NewField` line. The input-side test cannot see this:
// it only checks getters on request messages.
//
// SPIKE NOTE (BACKLOG "connectshim proto ↔ struct converters: generate
// instead of hand-write"): descriptor-driven codegen was rejected for these
// converters. They are domain↔proto, not proto↔proto — the domain side is
// hand-written Go (admindomain.*), and the mappings encode real logic a
// proto descriptor can't know: resource-name synthesis
// (`fmt.Sprintf("storageBackends/%s/buckets/%s", …)`), uuid.UUID↔string,
// time.Time↔timestamppb, enum↔string, custom resource-version encoding,
// nested sub-mappers. A generator could only emit the trivial 1:1 fields
// while the drift-prone hard fields stayed hand-written. So the realistic
// guard against drift is a coverage test, not codegen — and this closes the
// response half that was missing.
//
// Mechanism (static): for each converter function whose single result is a
// pointer to a proto message (`func xToProto(...) *pb.Y`), resolve Y's
// descriptor and assert every field name appears in the function's source —
// as a keyed literal element (`Field: …`) or a later assignment
// (`x.Field = …`). Searching the source text (rather than only the literal
// keys) mirrors the input-side test and tolerates both styles. proto `oneof`
// members are collapsed to the oneof's Go field name (protoc-gen-go sets the
// whole oneof via that one struct field), so we don't demand each member.
//
// What it does NOT catch: wrong *values* (per-RPC tests cover that), and a
// field whose Go name is a substring of unrelated source text (low risk; the
// `Field:`/`Field =` anchors below keep it precise).

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
)

// responseSkipFields lists (proto message, field) pairs a ToProto converter
// intentionally leaves unset, each with a real reason — the response-side
// mirror of skipFields. These are the established baseline this guard found;
// adding a NEW entry requires justifying why the field is not projected
// (privacy gating, server-computed-but-not-stored, feature not yet wired),
// not "the converter forgot it".
var responseSkipFields = map[string]map[string]string{
	"Object": {
		// objectToProto comment: surfaced only for privileged callers; the
		// role check is deferred, so it is left unset for now.
		"Placement": "physical placement is privileged-only; role-gated projection not yet wired",
		// Object-lock state is enforced server-side (SQL triggers + HardDelete
		// guard); it is not projected onto the read Object DTO here.
		"Lock": "lock state enforced server-side; not projected onto the read Object DTO",
	},
	"ObjectKey": {
		// Mirrors the input-side skip: completion_mode is derived from the
		// bucket→backend events config server-side and is not carried on the
		// ObjectKey read projection.
		"CompletionMode": "server-computed from bucket→backend events; not on the ObjectKey read DTO",
		"Constraints":    "per-key constraints not yet projected onto the ObjectKey read DTO",
	},
	"Operation": {
		// operationToProto projects the initiator tenant; the actor subject is
		// not surfaced on the operation read DTO.
		"InitiatorSubject": "operation read DTO projects initiator tenant only, not subject",
	},
}

func TestShimResponseFieldCoverage(t *testing.T) {
	root := repoBackendRoot(t)

	type missing struct {
		Fn, ProtoName, Field, File string
	}
	var misses []missing
	audited := 0

	for _, plane := range shimPlanes {
		dir := filepath.Join(root, "internal", "api", "connectshim", plane.dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
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
				if !ok || fn.Body == nil {
					continue
				}
				typeName := protoResultType(fn)
				if typeName == "" {
					continue
				}
				// Resolve against this plane's proto package; if it's not a
				// proto message (e.g. a domain return type), skip silently.
				full := protoreflect.FullName(plane.pkgName + "." + typeName)
				desc, err := protoregistry.GlobalFiles.FindDescriptorByName(full)
				if err != nil {
					continue
				}
				msg, ok := desc.(protoreflect.MessageDescriptor)
				if !ok {
					continue
				}
				audited++
				src := string(body[fset.Position(fn.Body.Pos()).Offset:fset.Position(fn.Body.End()).Offset])
				for _, name := range responseFieldGoNames(msg) {
					if _, skip := responseSkipFields[typeName][name]; skip {
						continue
					}
					// `Field:` (keyed literal) or `Field =` / `Field=`
					// (assignment) — anchored so we don't match a bare
					// substring inside an unrelated identifier.
					if !strings.Contains(src, name+":") &&
						!strings.Contains(src, name+" =") &&
						!strings.Contains(src, name+"=") {
						misses = append(misses, missing{
							Fn: fn.Name.Name, ProtoName: string(full), Field: name, File: e.Name(),
						})
					}
				}
			}
		}
	}

	if audited == 0 {
		t.Fatal("audited 0 ToProto converters — detection is broken")
	}
	t.Logf("audited %d ToProto converters", audited)

	if len(misses) == 0 {
		return
	}
	sort.Slice(misses, func(i, j int) bool {
		if misses[i].File != misses[j].File {
			return misses[i].File < misses[j].File
		}
		if misses[i].Fn != misses[j].Fn {
			return misses[i].Fn < misses[j].Fn
		}
		return misses[i].Field < misses[j].Field
	})
	var b strings.Builder
	b.WriteString("ToProto converters leave proto response fields unset:\n\n")
	for _, m := range misses {
		b.WriteString("  ")
		b.WriteString(m.File)
		b.WriteString(" :: ")
		b.WriteString(m.Fn)
		b.WriteString("  proto=")
		b.WriteString(m.ProtoName)
		b.WriteString("  unset=")
		b.WriteString(m.Field)
		b.WriteString("\n")
	}
	b.WriteString("\nEither set the field in the converter literal, or — if the drop ")
	b.WriteString("is intentional — add it to responseSkipFields with a justification.")
	t.Fatal(b.String())
}

// protoResultType returns the bare type name when fn's single result is a
// pointer to a selector type (`*pkg.TypeName`), else "". The caller decides
// whether TypeName is actually a proto message via the registry.
func protoResultType(fn *ast.FuncDecl) string {
	if fn.Type == nil || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return ""
	}
	star, ok := fn.Type.Results.List[0].Type.(*ast.StarExpr)
	if !ok {
		return ""
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	return sel.Sel.Name
}

// responseFieldGoNames returns the protoc-gen-go struct field names a ToProto
// converter is expected to set. Each oneof's members collapse to the single Go
// field named after the oneof (protoc-gen-go sets the whole oneof via that one
// field), so we don't demand every member. Synthetic oneofs (proto3 `optional`)
// are left as their plain scalar field.
func responseFieldGoNames(msg protoreflect.MessageDescriptor) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	fields := msg.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if oo := f.ContainingOneof(); oo != nil && !oo.IsSynthetic() {
			add(camelGo(string(oo.Name())))
			continue
		}
		add(goFieldName(f))
	}
	return out
}

// camelGo converts a snake_case proto name to the protoc-gen-go CamelCase Go
// identifier (same rule goFieldName applies to a field name).
func camelGo(snake string) string {
	parts := strings.Split(snake, "_")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
