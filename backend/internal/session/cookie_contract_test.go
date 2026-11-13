package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The console's server sends a request without this cookie to sign in
// (frontend/server, ADR-0049). It reads the name from its own contract; a name
// changed here alone would send every signed-in user to the login page.
func TestCookieNameMatchesTheConsoleContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "frontend", "server", "contract", "contract.json"))
	require.NoError(t, err)
	var contract struct {
		SessionCookie string `json:"sessionCookie"`
	}
	require.NoError(t, json.Unmarshal(raw, &contract))
	require.Equal(t, CookieName, contract.SessionCookie)
}
