package checksum

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// sharedComposites is the table both SDKs' readers and fakes run too
// (sdk/testdata/composite_checksums.json): the three must compute the same
// bytes, or a download the server recorded would fail every SDK's check.
var sharedComposites = filepath.Join("..", "..", "..", "sdk", "testdata", "composite_checksums.json")

type compositeCase struct {
	Name      string   `json:"name"`
	Algorithm string   `json:"algorithm"`
	Parts     []string `json:"parts"`
	Composite string   `json:"composite"`
}

func TestCompositeMatchesTheSharedVectors(t *testing.T) {
	raw, err := os.ReadFile(sharedComposites)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []compositeCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, tc := range doc.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			got, err := Composite(tc.Algorithm, tc.Parts)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.Composite {
				t.Errorf("Composite = %q, want %q", got, tc.Composite)
			}
		})
	}
}

func TestCompositeRefuses(t *testing.T) {
	sha := b64(make([]byte, digestBytes[SHA256]))
	for name, tc := range map[string]struct {
		algo  string
		parts []string
	}{
		"no parts":                   {SHA256, nil},
		"an unknown algorithm":       {"BLAKE3", []string{sha}},
		"a part not base64":          {SHA256, []string{sha, "not base64!"}},
		"a part of the wrong length": {SHA256, []string{sha, b64([]byte{1, 2, 3, 4})}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Composite(tc.algo, tc.parts); err == nil {
				t.Error("accepted")
			}
		})
	}
	if _, err := Composite(SHA256, nil); !errors.Is(err, ErrNoParts) {
		t.Errorf("no parts: %v, want ErrNoParts", err)
	}
}
