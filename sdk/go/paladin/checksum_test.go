package paladin

import (
	"strings"
	"testing"
)

func TestChecksum(t *testing.T) {
	for algo, want := range map[string]string{
		ChecksumSHA256: "LPJNul+wow4m6DsqxbninhsWHlwfp0JecwQzYpOLmCQ=",
		ChecksumCRC32C: "mnG7TA==",
		ChecksumMD5:    "XUFAKrxLKna5cZ2REBfFkg==",
	} {
		got, err := Checksum(algo, strings.NewReader("hello"))
		if err != nil || got != want {
			t.Errorf("Checksum(%s, hello) = %q, %v; want %q", algo, got, err, want)
		}
	}
	if _, err := Checksum("SHA1", strings.NewReader("")); err == nil {
		t.Error("an unknown algorithm was accepted")
	}
}
