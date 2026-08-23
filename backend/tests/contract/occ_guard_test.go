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

	"buf.build/go/protovalidate"
)

// An OCC guard that can be omitted is not a guard. expected_version=0 disables
// the check in SQL — see objects.sql and collections.sql, which say so — so a
// request that reaches a handler with an empty resource_version performs an
// unconditional write and returns 200. The caller believes it held a lock it
// never had.
//
// Two ways that happens, and this file gates both:
//
//   - The schema does not require the field, and nothing else refuses an empty
//     one. TestOCCGuardIsRequired.
//   - The code parses the field but discards the parse error, so a malformed
//     guard like "abc" satisfies min_len and still lands on 0.
//     TestParseRVErrorIsNotDiscarded.
//
// Both were live defects before these gates existed: DeleteCollection had a
// force flag with no guard behind it, DeleteBucket hung its bypass on
// delete_on_backend (so the most destructive call was the only unguarded one),
// and eleven call sites wrote `rv, _ := parseRV(...)`.

// TestOCCGuardIsRequired requires every request carrying resource_version to
// either mandate it in the schema or offer an explicit, documented bypass.
func TestOCCGuardIsRequired(t *testing.T) {
	t.Parallel()

	var problems []string
	forEachMessage(t, func(msg protoreflect.MessageDescriptor) {
		name := string(msg.Name())
		if !strings.HasSuffix(name, "Request") {
			return
		}
		f := msg.Fields().ByName("resource_version")
		if f == nil {
			return
		}
		if reason := occExempt[name]; reason != "" {
			return
		}
		if hasMinLen(t, f) {
			return
		}
		// No schema requirement — a bypass flag makes that legitimate, because
		// the handler then has something to check the omission against.
		if bypassFlag(msg) != "" {
			return
		}
		problems = append(problems, fmt.Sprintf(
			"%s.resource_version is optional and the message has no bypass flag: "+
				"an omitted guard silently disables the OCC check", name))
	})

	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
	if len(problems) > 0 {
		t.Log("\nAdd `[(buf.validate.field).string.min_len = 1]`, or add an explicit " +
			"bool bypass the handler checks, or record it in occExempt with why.")
	}
}

// bypassFlag reports the name of a bool field that reads as a deliberate
// opt-out of the guard. `force` is the convention (DeleteTenant,
// DeleteBackend, DeleteCollection, DeleteBucket).
func bypassFlag(msg protoreflect.MessageDescriptor) string {
	for i := 0; i < msg.Fields().Len(); i++ {
		f := msg.Fields().Get(i)
		if f.Kind() == protoreflect.BoolKind && string(f.Name()) == "force" {
			return string(f.Name())
		}
	}
	return ""
}

// occExempt records requests where an optional guard is the documented,
// intended behaviour. Each entry is a claim that the omission is safe.
var occExempt = map[string]string{
	"SetQuotaRequest": "documented in the proto as not enforced — SetQuota upserts without consulting the column; requiring it would demand a guard the server ignores",
}

func hasMinLen(t *testing.T, f protoreflect.FieldDescriptor) bool {
	t.Helper()
	// protovalidate resolves the extension; a min_len of 1 or more is a
	// requirement, since an absent string field arrives as "".
	c, err := protovalidate.ResolveFieldRules(f)
	if err != nil || c == nil {
		return false
	}
	s := c.GetString()
	return s != nil && s.GetMinLen() >= 1
}

// TestParseRVErrorIsNotDiscarded forbids dropping the error from the guard's
// parse. `rv, _ := parseRV(x)` turns "abc" — which satisfies min_len — into 0,
// which is exactly the value that disables the check. The schema constraint
// alone does not close this, which is why it needs its own gate.
func TestParseRVErrorIsNotDiscarded(t *testing.T) {
	t.Parallel()

	discard := regexp.MustCompile(`\w+,\s*_\s*:?=\s*parse(RV|Int64)\(`)

	var hits []string
	root := repoPath(t, "internal")
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
		for i, line := range strings.Split(string(src), "\n") {
			if discard.MatchString(line) {
				hits = append(hits, fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}

	sort.Strings(hits)
	for _, h := range hits {
		t.Errorf("discarded resource_version parse error — a malformed guard "+
			"becomes 0, which disables the check:\n    %s", h)
	}
}

func forEachMessage(t *testing.T, fn func(protoreflect.MessageDescriptor)) {
	t.Helper()
	seen := 0
	rangePaladinFiles(func(fd protoreflect.FileDescriptor) {
		for i := 0; i < fd.Messages().Len(); i++ {
			seen++
			fn(fd.Messages().Get(i))
		}
	})
	if seen == 0 {
		t.Fatal("walked no messages — descriptors are not registered")
	}
}
