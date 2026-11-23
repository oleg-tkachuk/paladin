package protonul

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// sharedTable is sdk/testdata/request_nul.json, which both SDKs' fakes are
// held to: the three refuse the same requests, naming the same path.
const sharedTable = "../../../sdk/testdata/request_nul.json"

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
	if got := fmt.Sprintf(RefusalFormat, ""); got != table.RefusalSuffix {
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
			got := Find(msg.ProtoReflect())
			switch {
			case tc.Path == "" && got != nil:
				t.Errorf("Find = %q, want nothing", got.Path)
			case tc.Path != "" && (got == nil || got.Path != tc.Path):
				t.Errorf("Find = %v, want %q", got, tc.Path)
			}
		})
	}
}
