// Package contract holds static gates over the API surface: checks that read
// the proto descriptors and the shim source rather than talking to a running
// server. They exist because the failures they catch are silent — the build
// passes, the tests pass, and the wrong thing happens at runtime.
package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	// Register every descriptor the gate walks.
	_ "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/admin/v1"
	_ "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/data/v1"
	_ "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/iam/v1"
)

// TestDeclaredRequestFieldIsRead catches the failure mode this gate was built
// for: a request field that exists in the schema, that clients dutifully
// populate, and that no handler ever reads.
//
// RestoreObjectVersionRequest.resource_version was exactly that. The field was
// declared, the console sent it on every call, generated clients typed it, and
// the shim called RestoreVersion(ctx, name) — dropping it on the floor. Nothing
// failed. The API simply promised a concurrency guard it did not apply, and
// the only way to notice was to read that one line.
//
// The check: for every RPC, find the shim method that serves it and require
// that each field of its request message is read there — either directly as
// GetXxx(), or by handing the whole message to something else.
func TestDeclaredRequestFieldIsRead(t *testing.T) {
	t.Parallel()

	handlers := loadHandlers(t)

	var missing []string
	forEachRPC(t, func(svc protoreflect.ServiceDescriptor, rpc protoreflect.MethodDescriptor) {
		body, ok := handlers.methodBody(string(svc.Name()), string(rpc.Name()))
		if !ok {
			// The service has no Connect assertion in this repo, or the
			// method is inherited from the generated Unimplemented embed. The
			// rpc-surface test already proves every method answers; this gate
			// is about field plumbing, so an unlocatable body is skipped.
			return
		}
		// A body that forwards the whole message reads every field by
		// construction — there is nothing to drop.
		if forwardsWholeMessage.MatchString(body) {
			return
		}
		msg := rpc.Input()
		for i := 0; i < msg.Fields().Len(); i++ {
			f := msg.Fields().Get(i)
			name := string(f.Name())
			k := fieldKey{string(msg.Name()), name}
			if exemptField[k] != "" || knownUnread[k] != "" {
				continue
			}
			if strings.Contains(body, "Get"+goCamel(name)+"()") {
				continue
			}
			missing = append(missing, fmt.Sprintf(
				"%s.%s: field %q is declared but never read in %s.%s",
				svc.FullName(), rpc.Name(), name, svc.Name(), rpc.Name()))
		}
	})

	sort.Strings(missing)
	for _, m := range missing {
		t.Error(m)
	}
	if len(missing) > 0 {
		t.Logf("\n%d declared-but-unread field(s). Read the field, delete it from "+
			"the proto, or — if it is genuinely correct not to read it — add it to "+
			"exemptField. If it is a gap, add it to knownUnread AND to BACKLOG.",
			len(missing))
	}
}

type fieldKey struct{ msg, field string }

// exemptField lists fields a handler legitimately does not read. An entry here
// is a claim that the absence is CORRECT.
var exemptField = map[fieldKey]string{
	{"InitiateMultipartUploadRequest", "idempotency_key"}: "read by the idempotency interceptor, not the handler — see middleware.idempotencyKey",
	{"UploadObjectRequest", "idempotency_key"}:            "same as InitiateMultipartUpload",
}

// knownUnread is the debt list: fields the server accepts and ignores today.
// These are NOT correct — each is an API promising something it does not do —
// and each is tracked in BACKLOG. The distinction from exemptField matters:
// this list should shrink, and a reviewer adding to it is recording a defect,
// not resolving one.
//
// It is currently empty. It started at eleven entries the day this gate was
// written; keeping it empty is the point.
//
// New entries need a BACKLOG item. The gate fails on anything not in either
// map, which is the point: the next field to go unread gets caught the day it
// lands, not two years later.
var knownUnread = map[fieldKey]string{}

// forwardsWholeMessage matches a body that hands the whole message to
// something else, which reads every field transitively.
//
// It deliberately does NOT match `m := req.Msg`. That binds a local and reads
// fields through it one at a time — exactly the shape where a field gets
// forgotten. An earlier version of this pattern treated the assignment as a
// forward, which silently exempted ListParts — at the time one of three RPCs
// ignoring object_name, and invisible to the gate because of that one regex.
var forwardsWholeMessage = regexp.MustCompile(
	`\((?:req\.Msg|m)[,)]|,\s*(?:req\.Msg|m)[,)]|\bfromProto\(|\btoDomain\(`)

// ─── handler index ──────────────────────────────────────────────────────────

// The index is built from the Connect assertions each handler package writes:
//
//	var _ paladinadminv1connect.CELServiceHandler = (*Handler)(nil)
//
// That line names the service AND the concrete type serving it, which is the
// only reliable way to pair them — handler types are called "Handler" in half
// a dozen packages, so matching on the type name alone pairs the wrong bodies
// and produces confident nonsense.
type handlerIndex struct {
	// service name → method name → body
	bodies map[string]map[string]string
}

var (
	assertRe = regexp.MustCompile(`var _ \w+connect\.(\w+)Handler = \(\*(\w+)\)\(nil\)`)
	methodRe = regexp.MustCompile(`(?m)^func \(\w+ \*(\w+)\) (\w+)\(`)
)

func loadHandlers(t *testing.T) *handlerIndex {
	t.Helper()
	idx := &handlerIndex{bodies: map[string]map[string]string{}}

	// Package dir → (type → method → body), plus the assertions found in it.
	perDir := map[string]map[string]map[string]string{}
	type assertion struct{ service, typ string }
	asserts := map[string][]assertion{}

	root := repoPath(t, "internal", "api")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path) // #nosec G304 — repo-local path
		if err != nil {
			return err
		}
		text := string(src)
		dir := filepath.Dir(path)

		for _, m := range assertRe.FindAllStringSubmatch(text, -1) {
			asserts[dir] = append(asserts[dir], assertion{service: m[1], typ: m[2]})
		}
		locs := methodRe.FindAllStringSubmatchIndex(text, -1)
		for i, loc := range locs {
			typ := text[loc[2]:loc[3]]
			method := text[loc[4]:loc[5]]
			end := len(text)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			if perDir[dir] == nil {
				perDir[dir] = map[string]map[string]string{}
			}
			if perDir[dir][typ] == nil {
				perDir[dir][typ] = map[string]string{}
			}
			perDir[dir][typ][method] = text[loc[0]:end]
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/api: %v", err)
	}

	for dir, as := range asserts {
		for _, a := range as {
			if m := perDir[dir][a.typ]; m != nil {
				idx.bodies[a.service] = m
			}
		}
	}
	if len(idx.bodies) == 0 {
		t.Fatal("indexed no handlers — the gate would pass vacuously")
	}
	return idx
}

// methodBody returns the body serving service.method, if this repo serves it.
func (h *handlerIndex) methodBody(service, method string) (string, bool) {
	m, ok := h.bodies[service]
	if !ok {
		return "", false
	}
	b, ok := m[method]
	return b, ok
}

// goCamel converts a proto field name to the Go getter suffix protoc-gen-go
// produces: object_id → ObjectId, http_url → HttpUrl.
func goCamel(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}

// ─── descriptor walk ────────────────────────────────────────────────────────

// rangePaladinFiles visits every registered descriptor in the paladin.* packages.
func rangePaladinFiles(fn func(protoreflect.FileDescriptor)) {
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if strings.HasPrefix(string(fd.Package()), "paladin.") {
			fn(fd)
		}
		return true
	})
}

func forEachRPC(t *testing.T, fn func(protoreflect.ServiceDescriptor, protoreflect.MethodDescriptor)) {
	t.Helper()
	seen := 0
	rangePaladinFiles(func(fd protoreflect.FileDescriptor) {
		for i := 0; i < fd.Services().Len(); i++ {
			svc := fd.Services().Get(i)
			for j := 0; j < svc.Methods().Len(); j++ {
				seen++
				fn(svc, svc.Methods().Get(j))
			}
		}
	})
	if seen == 0 {
		t.Fatal("walked no RPCs — descriptors are not registered, so the gate proves nothing")
	}
}

func repoPath(t *testing.T, parts ...string) string {
	t.Helper()
	// tests/contract → backend
	root := filepath.Join("..", "..")
	return filepath.Join(append([]string{root}, parts...)...)
}
