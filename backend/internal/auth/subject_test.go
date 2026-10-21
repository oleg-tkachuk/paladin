package auth

import (
	"encoding/json"
	"os"
	"testing"
)

// sharedSubjects is the table the console reads the same prefixes from.
const sharedSubjects = "../../../sdk/testdata/actor_subjects.json"

func TestSubjectPrefixesMatchTheSharedTable(t *testing.T) {
	raw, err := os.ReadFile(sharedSubjects)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		APIToken   string `json:"api_token"`
		Capability string `json:"capability"`
		System     string `json:"system"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	for name, pair := range map[string][2]string{
		"api_token":  {SubjectPrefixAPIToken, table.APIToken},
		"capability": {SubjectPrefixCapability, table.Capability},
		"system":     {SubjectPrefixSystem, table.System},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: %q in the server, %q in the table", name, pair[0], pair[1])
		}
	}
}
