package apiutil

import (
	"testing"

	"connectrpc.com/connect/v2"
)

// The server is at fault for exactly the codes the RPC log reports as failed;
// every other code is the caller's to act on.
func TestServerFault(t *testing.T) {
	fault := map[connect.Code]bool{
		connect.CodeInternal:    true,
		connect.CodeUnknown:     true,
		connect.CodeDataLoss:    true,
		connect.CodeUnavailable: true,
	}
	for code := connect.CodeCanceled; code <= connect.CodeUnauthenticated; code++ {
		if got := ServerFault(code); got != fault[code] {
			t.Errorf("ServerFault(%v) = %v, want %v", code, got, fault[code])
		}
	}
}
