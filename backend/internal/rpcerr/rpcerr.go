// Package rpcerr builds a Connect error that keeps the error it reports.
//
// connect-go v2 sends a *connect.Error's message and keeps its cause local:
// connect.NewError(code, err.Error()) sends the text and drops err, so
// errors.Is no longer finds the sentinel a handler wrapped — the contract a
// caller inside the server, or a test, matches on. New sends the same text and
// keeps err as the cause.
package rpcerr

import "connectrpc.com/connect/v2"

// New is a Connect error with code whose message is err's text and whose
// cause is err.
func New(code connect.Code, err error) *connect.Error {
	return connect.NewError(code, err.Error()).WithCause(err)
}
