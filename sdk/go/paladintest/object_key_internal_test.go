package paladintest

import (
	"encoding/json"
	"os"
	"testing"
)

// The fake's limit is the server's, as the shared table states it.
func TestMaxKeyBytesIsTheTables(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/object_keys.json")
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		MaxKeyBytes     int `json:"max_key_bytes"`
		MaxSegmentBytes int `json:"max_segment_bytes"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if table.MaxKeyBytes != maxKeyBytes || table.MaxSegmentBytes != maxSegmentBytes {
		t.Errorf("the table's limits are %d and %d, the fake's %d and %d",
			table.MaxKeyBytes, table.MaxSegmentBytes, maxKeyBytes, maxSegmentBytes)
	}
}

// Invalid UTF-8 never crosses the wire, which refuses to marshal it, and JSON
// cannot hold it, so the fake's check of it is tested here.
func TestKeyRefusalRefusesInvalidUTF8(t *testing.T) {
	if keyRefusal("a\xff") == "" {
		t.Error(`keyRefusal("a\xff") = "", want a refusal`)
	}
}
