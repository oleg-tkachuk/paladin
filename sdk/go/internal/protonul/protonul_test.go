package protonul_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/wrapperspb"

	// Registers the request messages the shared table names.
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"

	"github.com/oleg-tkachuk/paladin/sdk/go/internal/protonul"
)

// sharedTable is sdk/testdata/request_nul.json, which the server's walker
// and the Python SDK's fake are held to as well.
const sharedTable = "../../../testdata/request_nul.json"

// nulTable is the shared table's shape.
type nulTable struct {
	RefusalSuffix string `json:"refusal_suffix"`
	Cases         []struct {
		Name    string          `json:"name"`
		Message string          `json:"message"`
		Request json.RawMessage `json:"request"`
		Path    string          `json:"path"`
	} `json:"cases"`
}

func TestFindAgreesWithTheSharedTable(t *testing.T) {
	raw, err := os.ReadFile(sharedTable)
	if err != nil {
		t.Fatal(err)
	}
	var table nulTable
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Cases) == 0 {
		t.Fatal("the shared table holds no case")
	}
	if got := fmt.Sprintf(protonul.RefusalFormat, ""); got != table.RefusalSuffix {
		t.Errorf("refusal suffix %q, the table says %q", got, table.RefusalSuffix)
	}
	for _, tc := range table.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			typ, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(tc.Message))
			if err != nil {
				t.Fatal(err)
			}
			msg := typ.New().Interface()
			if err := protojson.Unmarshal(tc.Request, msg); err != nil {
				t.Fatal(err)
			}
			path, found := protonul.Find(msg.ProtoReflect())
			if path != tc.Path || found != (tc.Path != "") {
				t.Errorf("Find = %q, %v; want %q", path, found, tc.Path)
			}
		})
	}
}

// What the shared table cannot carry in protobuf JSON: a wrapper at the top,
// and a bytes field, which is not text.
func TestFindWrappers(t *testing.T) {
	const withNUL = "a\x00b"
	for _, tc := range []struct {
		name string
		msg  proto.Message
		path string
	}{
		{"a string wrapper", wrapperspb.String(withNUL), "value"},
		{"bytes are skipped", wrapperspb.Bytes([]byte(withNUL)), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, found := protonul.Find(tc.msg.ProtoReflect())
			if path != tc.path || found != (tc.path != "") {
				t.Errorf("Find = %q, %v; want %q", path, found, tc.path)
			}
		})
	}
}

func TestRefusal(t *testing.T) {
	if got, want := protonul.Refusal("key"), "key: holds a NUL character"; got != want {
		t.Errorf("Refusal = %q, want %q", got, want)
	}
}
