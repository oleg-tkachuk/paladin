package s3adapter

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRotatingTokenRetriever_ReReadsFile is the regression that justifies
// having our own retriever rather than stscreds.IdentityTokenFile: when the
// kubelet atomically replaces the projected service-account token, the
// next call to GetIdentityToken must observe the new bytes — without
// process restart, without a long-lived in-memory copy.
func TestRotatingTokenRetriever_ReReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")

	if err := os.WriteFile(path, []byte("token-v1"), 0o600); err != nil {
		t.Fatalf("write v1: %v", err)
	}

	r := &rotatingTokenRetriever{path: path}

	got, err := r.GetIdentityToken()
	if err != nil {
		t.Fatalf("read v1: %v", err)
	}
	if string(got) != "token-v1" {
		t.Fatalf("v1: got %q, want %q", got, "token-v1")
	}

	// EKS IRSA uses atomic-rename for safety. Mimic it: write the new
	// version to a temp file, then rename over the original.
	tmp := filepath.Join(dir, "token.next")
	if err := os.WriteFile(tmp, []byte("token-v2-after-rotation"), 0o600); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename: %v", err)
	}

	got, err = r.GetIdentityToken()
	if err != nil {
		t.Fatalf("read v2: %v", err)
	}
	if string(got) != "token-v2-after-rotation" {
		t.Fatalf("v2: got %q, want %q (rotation not observed)", got, "token-v2-after-rotation")
	}
}

func TestRotatingTokenRetriever_MissingFile(t *testing.T) {
	r := &rotatingTokenRetriever{path: "/no/such/path"}
	if _, err := r.GetIdentityToken(); err == nil {
		t.Fatal("want error for missing token file")
	}
}
