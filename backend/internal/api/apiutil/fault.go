package apiutil

import "connectrpc.com/connect/v2"

// ServerFault reports whether code says the server, not the caller, is at
// fault: the codes a handler logs at error, where every other answer is a
// refusal the caller can act on. The RPC log (middleware.logFailure) draws the
// same line.
func ServerFault(code connect.Code) bool {
	switch code {
	case connect.CodeInternal, connect.CodeUnknown, connect.CodeDataLoss, connect.CodeUnavailable:
		return true
	}
	return false
}
