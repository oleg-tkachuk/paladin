package session

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	// Registered so every service of each plane's package is in the registry.
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// Every method of every service a plane's package declares routes to that
// plane: a new RPC reaches the console with no change here.
func TestEveryServiceRoutesToItsPlane(t *testing.T) {
	seen := map[Plane]int{}
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		pkg := string(fd.Package()) + "."
		want, ok := planePackages[pkg]
		if !ok {
			return true
		}
		for i := range fd.Services().Len() {
			svc := fd.Services().Get(i)
			for j := range svc.Methods().Len() {
				procedure := "/" + string(svc.FullName()) + "/" + string(svc.Methods().Get(j).Name())
				if got, ok := planeOf(procedure); !ok || got != want {
					t.Errorf("%s routes to %q (%v), want %q", procedure, got, ok, want)
				}
				seen[want]++
			}
		}
		return true
	})
	for _, p := range []Plane{PlaneData, PlaneAdmin, PlaneIAM} {
		if seen[p] == 0 {
			t.Errorf("no procedure of the %s plane found; the registry import is broken", p)
		}
	}
}

func TestPlaneOfRefusesWhatIsNotAProcedure(t *testing.T) {
	for _, path := range []string{
		"", "/", "paladin.data.v1.ObjectService/ListObjects", "/paladin.data.v1.ObjectService",
		"/paladin.data.v1./ListObjects", "/paladin.data.v1.ObjectService/", "/paladin.data.v1.ObjectService/A/B",
		"/paladin.data.v2.ObjectService/ListObjects", "/grpc.health.v1.Health/Check",
	} {
		if p, ok := planeOf(path); ok {
			t.Errorf("planeOf(%q) = %q, want refused", path, p)
		}
	}
}

// Each token procedure is an AuthService method that exists: a renamed one
// would silently start being forwarded.
func TestTokenProceduresExist(t *testing.T) {
	for procedure := range tokenProcedures {
		name := protoreflect.FullName(strings.ReplaceAll(strings.TrimPrefix(procedure, "/"), "/", "."))
		if _, err := protoregistry.GlobalFiles.FindDescriptorByName(name); err != nil {
			t.Errorf("%s: %v", procedure, err)
		}
	}
}

// carriesToken reports whether m, or a message it holds, has a field that
// holds a token: a redacted field named for one, or a TokenPair. A page token
// is not redacted; a password is, and is no token.
func carriesToken(m protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool) bool {
	if seen[m.FullName()] {
		return false
	}
	seen[m.FullName()] = true
	if m.Name() == "TokenPair" {
		return true
	}
	for i := range m.Fields().Len() {
		f := m.Fields().Get(i)
		opts, _ := f.Options().(*descriptorpb.FieldOptions)
		if strings.Contains(string(f.Name()), "token") && opts.GetDebugRedact() {
			return true
		}
		if f.Message() != nil && strings.HasPrefix(string(f.Message().FullName()), "paladin.") && carriesToken(f.Message(), seen) {
			return true
		}
	}
	return false
}

// Every iam procedure whose request or answer carries a token is one the role
// never forwards: forwarded, it would put a token in the browser's hands or
// take one from it.
func TestEveryTokenCarryingIAMProcedureStaysWithTheRole(t *testing.T) {
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if fd.Package() != "paladin.iam.v1" {
			return true
		}
		for i := range fd.Services().Len() {
			svc := fd.Services().Get(i)
			for j := range svc.Methods().Len() {
				m := svc.Methods().Get(j)
				procedure := "/" + string(svc.FullName()) + "/" + string(m.Name())
				carries := carriesToken(m.Input(), map[protoreflect.FullName]bool{}) ||
					carriesToken(m.Output(), map[protoreflect.FullName]bool{})
				if carries != tokenProcedures[procedure] {
					t.Errorf("%s carries a token: %v; held back from forwarding: %v", procedure, carries, tokenProcedures[procedure])
				}
			}
		}
		return true
	})
}
