package paladin

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

// sharedComposites is sdk/testdata/composite_checksums.json, which the server
// and the Python SDK run too: the three must compute the same bytes.
const sharedComposites = "../../testdata/composite_checksums.json"

// chunkSizes feed the bytes in pieces that fall inside a part, on a boundary
// and across several, as a stream from storage does.
var chunkSizes = []int{1, 2, 3, 7, 1 << 20}

func TestCompositeHashMatchesTheSharedVectors(t *testing.T) {
	raw, err := os.ReadFile(sharedComposites)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			Name          string `json:"name"`
			Algorithm     string `json:"algorithm"`
			PartSizeBytes int64  `json:"part_size_bytes"`
			BodyBase64    string `json:"body_base64"`
			Composite     string `json:"composite"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, tc := range doc.Cases {
		body, err := base64.StdEncoding.DecodeString(tc.BodyBase64)
		if err != nil {
			t.Fatal(err)
		}
		newDigest := checksums[tc.Algorithm]
		for _, chunk := range chunkSizes {
			c := newCompositeHash(newDigest, tc.PartSizeBytes)
			for start := 0; start < len(body); start += chunk {
				c.write(body[start:min(start+chunk, len(body))])
			}
			digest, parts := c.sum()
			if got := compositeValue(digest, parts); got != tc.Composite {
				t.Errorf("%s, %d-byte chunks: %q, want %q", tc.Name, chunk, got, tc.Composite)
			}
		}
		if _, _, ok := parseComposite(tc.Composite, newDigest().Size()); !ok {
			t.Errorf("%s: %q does not parse", tc.Name, tc.Composite)
		}
	}
}

func TestParseCompositeRefuses(t *testing.T) {
	const sha256Size = 32
	for _, value := range []string{
		"no-separator-but-a-word",
		"AAAA",
		"D3qZqhMCO0j+h+RS7RQR8UFdX4mTed1iexltHESNUwA=-0",
		"D3qZqhMCO0j+h+RS7RQR8UFdX4mTed1iexltHESNUwA=-x",
		"not base64!-3",
		"AAAA-3",
	} {
		if _, _, ok := parseComposite(value, sha256Size); ok {
			t.Errorf("parseComposite(%q) accepted", value)
		}
	}
}
