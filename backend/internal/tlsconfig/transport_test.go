package tlsconfig

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestClientTransportIsTheDefaultWithNothingSet(t *testing.T) {
	tr, err := ClientTransport("", "", "", "", false)
	if err != nil || tr != nil {
		t.Errorf("ClientTransport = %v, %v; want nil, keeping http.DefaultTransport", tr, err)
	}
}

// A server whose certificate chains to the CA is reached; one the transport
// was not told to trust is not.
func TestClientTransportTrustsTheCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}

	tr, err := ClientTransport("", "", ca, "", false)
	if err != nil {
		t.Fatalf("ClientTransport: %v", err)
	}
	if err := get(t, &http.Client{Transport: tr}, srv.URL); err != nil {
		t.Fatalf("GET with the CA: %v", err)
	}
	if err := get(t, http.DefaultClient, srv.URL); err == nil {
		t.Error("GET without the CA succeeded, want a certificate error")
	}
}

func get(t *testing.T, c *http.Client, url string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

func TestClientTransportRefusesAnUnreadableCA(t *testing.T) {
	if _, err := ClientTransport("", "", filepath.Join(t.TempDir(), "missing.crt"), "", false); err == nil {
		t.Fatal("a CA path that does not exist was accepted")
	}
}
