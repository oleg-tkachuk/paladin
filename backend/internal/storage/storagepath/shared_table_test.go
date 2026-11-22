package storagepath

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// sharedKeys is the table both SDKs' fakes and the console hold their key
// check to.
const sharedKeys = "../../../../sdk/testdata/object_keys.json"

// keyTable is sdk/testdata/object_keys.json.
type keyTable struct {
	MaxKeyBytes     int          `json:"max_key_bytes"`
	MaxSegmentBytes int          `json:"max_segment_bytes"`
	Canonical       []string     `json:"canonical"`
	Refused         []string     `json:"refused"`
	Lengths         []lengthCase `json:"lengths"`
}

// lengthCase is a key of segments of unit, each repeated to its bytes.
type lengthCase struct {
	Unit      string `json:"unit"`
	Segments  []int  `json:"segments"`
	Canonical bool   `json:"canonical"`
}

func (c lengthCase) key(t *testing.T) string {
	t.Helper()
	segments := make([]string, 0, len(c.Segments))
	for _, n := range c.Segments {
		if n%len(c.Unit) != 0 {
			t.Fatalf("length case %q: %d bytes is not a whole number of units", c.Unit, n)
		}
		segments = append(segments, strings.Repeat(c.Unit, n/len(c.Unit)))
	}
	return strings.Join(segments, Separator)
}

func readKeyTable(t *testing.T) keyTable {
	t.Helper()
	raw, err := os.ReadFile(sharedKeys)
	if err != nil {
		t.Fatal(err)
	}
	var table keyTable
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Canonical) == 0 || len(table.Refused) == 0 || len(table.Lengths) == 0 {
		t.Fatalf("the table lists %d canonical, %d refused and %d length cases",
			len(table.Canonical), len(table.Refused), len(table.Lengths))
	}
	return table
}

func TestValidateKeyMatchesTheSharedTable(t *testing.T) {
	table := readKeyTable(t)
	if table.MaxKeyBytes != MaxKeyBytes || table.MaxSegmentBytes != MaxSegmentBytes {
		t.Errorf("the table's limits are %d and %d, MaxKeyBytes and MaxSegmentBytes %d and %d",
			table.MaxKeyBytes, table.MaxSegmentBytes, MaxKeyBytes, MaxSegmentBytes)
	}
	for _, k := range table.Canonical {
		if err := ValidateKey(k); err != nil {
			t.Errorf("%q is canonical in the table, refused here: %v", k, err)
		}
	}
	for _, k := range table.Refused {
		if ValidateKey(k) == nil {
			t.Errorf("%q is refused in the table, accepted here", k)
		}
	}
	for _, c := range table.Lengths {
		if got := ValidateKey(c.key(t)) == nil; got != c.Canonical {
			t.Errorf("%q in segments of %v bytes: canonical %v here, %v in the table", c.Unit, c.Segments, got, c.Canonical)
		}
	}
}
