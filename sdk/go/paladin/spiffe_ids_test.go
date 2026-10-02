package paladin_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// spiffeIDsFile is the table both SDKs judge SPIFFE IDs against.
const spiffeIDsFile = "../../testdata/spiffe_ids.json"

// TestSpiffeIDTableMatchesGoSpiffe pins the shared table to go-spiffe, and
// TLS.ServerID to the table: the Python SDK runs the same table against its
// own parser, so the two cannot disagree on what a SPIFFE ID is.
func TestSpiffeIDTableMatchesGoSpiffe(t *testing.T) {
	raw, err := os.ReadFile(spiffeIDsFile)
	if err != nil {
		t.Fatal(err)
	}
	var table struct{ Valid, Invalid []string }
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Valid) == 0 || len(table.Invalid) == 0 {
		t.Fatal("the table is empty — this test is asserting nothing")
	}
	ca := newAuthority(t)
	f := writeFiles(t, t.TempDir(), ca.pem, ca.issue(t, true, nil, nil))
	for _, id := range table.Valid {
		if _, err := spiffeid.FromString(id); err != nil {
			t.Errorf("go-spiffe refuses %q, which the table calls valid: %v", id, err)
		}
		if _, err := (paladin.TLS{CAFile: f.ca, ServerID: id}).Transport(); err != nil {
			t.Errorf("ServerID %q: %v", id, err)
		}
	}
	for _, id := range table.Invalid {
		if _, err := spiffeid.FromString(id); err == nil {
			t.Errorf("go-spiffe accepts %q, which the table calls invalid", id)
		}
		_, err := (paladin.TLS{CAFile: f.ca, ServerID: id}).Transport()
		if id != "" && !errors.Is(err, paladin.ErrServerID) {
			t.Errorf("ServerID %q: err = %v, want ErrServerID", id, err)
		}
	}
}
