// Package rpcerr builds a Connect error that keeps the error it reports, and
// names the errors that are a refusal whatever code they are answered with.
//
// connect-go v2 sends a *connect.Error's message and keeps its cause local:
// connect.NewError(code, err.Error()) sends the text and drops err, so
// errors.Is no longer finds the sentinel a handler wrapped — the contract a
// caller inside the server, or a test, matches on. New sends the same text and
// keeps err as the cause.
package rpcerr

import (
	"errors"

	"connectrpc.com/connect/v2"
)

// New is a Connect error with code whose message is err's text and whose
// cause is err.
func New(code connect.Code, err error) *connect.Error {
	return connect.NewError(code, err.Error()).WithCause(err)
}

// Refusal is an error that is a verdict on the request — the server decided,
// and said no — whatever code it is answered with. A provisioning bucket is
// answered Unavailable, so the caller retries, yet nothing on the server
// failed. The package that decides implements it and the failure log reads
// it, and neither imports the other.
type Refusal interface {
	error
	// Refuses marks the error as a refusal. It does nothing.
	Refuses()
}

// Refused reports whether err, however wrapped — a *connect.Error's cause
// included — is a Refusal.
func Refused(err error) bool {
	_, ok := errors.AsType[Refusal](err)
	return ok
}
