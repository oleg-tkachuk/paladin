package paladin_test

import (
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	// Registered for their descriptors; the tests below walk the registry.
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

const (
	readmePath = "../README.md"
	// contractPackagePrefix selects this contract's files from everything the
	// registry holds (google.*, buf.validate, …).
	contractPackagePrefix = "paladin."
)

// TestReadmeListsEveryMethod keeps the method tables in README.md in step with
// the generated services: a table row is `| \`Service\` | \`A\`, \`B\` |`.
func TestReadmeListsEveryMethod(t *testing.T) {
	raw, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			continue
		}
		name := strings.Trim(strings.TrimSpace(cells[1]), "`")
		rows[name] = cells[2]
	}

	services := 0
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), contractPackagePrefix) {
			return true
		}
		for i := 0; i < fd.Services().Len(); i++ {
			svc := fd.Services().Get(i)
			services++
			row, ok := rows[string(svc.Name())]
			if !ok {
				t.Errorf("README.md has no row for %s", svc.FullName())
				continue
			}
			for j := 0; j < svc.Methods().Len(); j++ {
				m := svc.Methods().Get(j).Name()
				if !strings.Contains(row, "`"+string(m)+"`") {
					t.Errorf("README.md row for %s does not list %s", svc.Name(), m)
				}
			}
		}
		return true
	})
	if services == 0 {
		t.Fatal("no contract services registered — this test is asserting nothing")
	}
}
