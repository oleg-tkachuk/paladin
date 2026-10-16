package auth_test

import (
	"regexp"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// contractPackage is the proto package prefix of Paladin's own services.
const contractPackage = "paladin."

// readingName is how an RPC that only reads is named in this contract.
var readingName = regexp.MustCompile(`^(Get|List|Count|Lookup|Query|Watch|Describe|Search|Inspect|Simulate|Validate|WhoAmI|Summar)`)

// RequireOAuthWriteScope lets a read-only OAuth grant through exactly the
// RPCs marked NO_SIDE_EFFECTS (ADR-0035). An RPC that reads but lacks the
// mark would refuse such an agent for no reason, so every RPC named as a
// read must carry it.
func TestEveryReadingRPCIsMarkedNoSideEffects(t *testing.T) {
	checked := 0
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), contractPackage) {
			return true
		}
		services := fd.Services()
		for i := range services.Len() {
			methods := services.Get(i).Methods()
			for j := range methods.Len() {
				m := methods.Get(j)
				if !readingName.MatchString(string(m.Name())) {
					continue
				}
				checked++
				opts, _ := m.Options().(*descriptorpb.MethodOptions)
				if opts.GetIdempotencyLevel() != descriptorpb.MethodOptions_NO_SIDE_EFFECTS {
					t.Errorf("%s reads but is not marked NO_SIDE_EFFECTS", m.FullName())
				}
			}
		}
		return true
	})
	if checked == 0 {
		t.Fatal("no reading RPC was found: the contract packages did not register")
	}
}
