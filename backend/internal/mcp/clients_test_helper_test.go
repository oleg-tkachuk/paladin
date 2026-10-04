package mcp

import "testing"

// mustClients unwraps a Clients constructor in a test:
// mustClients(t)(NewClients(...)).
func mustClients(t *testing.T) func(*Clients, error) *Clients {
	t.Helper()
	return func(c *Clients, err error) *Clients {
		t.Helper()
		if err != nil {
			t.Fatalf("clients: %v", err)
		}
		return c
	}
}
