// Package fuzzassert holds the invariants a handler-level fuzz target asserts
// about what a client is told, whatever it sent:
//
//   - an error is a *connect.Error whose code is one the handler answers
//     client input with — never Internal or Unknown for a malformed request;
//   - the text the client reads names nothing of the server's insides: no Go
//     type or package name, no driver or SQL text, no formatting verb, no
//     stack trace;
//   - and nothing unprintable reaches it raw. Input echoed back is quoted
//     (%q), which escapes what cannot be printed and leaves the message valid
//     UTF-8.
//
// Check and CheckText return the violation, so the rules are tested as plain
// functions; Refusal and Text fail a test with it.
package fuzzassert

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"testing"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect/v2"
)

// ClientCodes are the codes a handler answers a client's own input with. A
// target narrows them to what its handler can say.
var ClientCodes = []connect.Code{
	connect.CodeInvalidArgument,
	connect.CodeNotFound,
	connect.CodePermissionDenied,
	connect.CodeFailedPrecondition,
	connect.CodeAlreadyExists,
	connect.CodeOutOfRange,
	connect.CodeUnauthenticated,
}

// leaks are patterns that match a message only when an internal error, a
// value's Go representation or a runtime failure was passed through to the
// client. They are case-sensitive.
var leaks = compile(
	// A Go type name, as %T or %v of an error value prints it.
	`\*(errors|fmt|connect|url|strconv|json)\.`,
	// A standard-library error passed through verbatim names its package.
	`(strconv|runtime|reflect)\.[A-Z]`, `json: `, `proto: `,
	// The database driver and the SQL it ran.
	`pgx`, `pgconn`, `sql: `, `SQLSTATE`,
	// A stack trace or a recovered panic: a source line, a goroutine header.
	`\.go:[0-9]`, `goroutine [0-9]`, `panic\(`,
	// A formatting verb with a wrong or missing argument, a nil printed as a
	// value, a pointer printed as a struct.
	`%!`, `<nil>`, `&\{`,
)

func compile(patterns ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		out[i] = regexp.MustCompile(p)
	}
	return out
}

// Check reports what is wrong with err as an answer to client input: nil when
// err is nil, or a connect error with a code in allowed and a message
// CheckText accepts. inputs are the request's client-controlled values; a
// leak pattern that one of them matches is the client's own text echoed back.
func Check(err error, allowed []connect.Code, inputs ...string) error {
	if err == nil {
		return nil
	}
	ce, ok := errors.AsType[*connect.Error](err)
	if !ok {
		return fmt.Errorf("not a connect error (%T): %q; a client reads it as Unknown", err, err.Error())
	}
	if !slices.Contains(allowed, ce.Code()) {
		return fmt.Errorf("code %v is not one this handler answers client input with (%v): %q", ce.Code(), allowed, ce.Message())
	}
	if verr := CheckText(ce.Message(), inputs...); verr != nil {
		return fmt.Errorf("%v message: %w", ce.Code(), verr)
	}
	return nil
}

// CheckText reports what is wrong with text a client reads — an error's
// message, or a diagnostic a validation answer carries: invalid UTF-8, a
// character that does not print, or a fragment of the server's insides that
// none of inputs matches.
func CheckText(text string, inputs ...string) error {
	if !utf8.ValidString(text) {
		return fmt.Errorf("not valid UTF-8: %q", text)
	}
	for i, r := range text {
		if r != ' ' && !unicode.IsPrint(r) {
			return fmt.Errorf("unprintable %U at byte %d, unquoted: %q", r, i, text)
		}
	}
	for _, leak := range leaks {
		if !leak.MatchString(text) || slices.ContainsFunc(inputs, leak.MatchString) {
			continue
		}
		return fmt.Errorf("names the server's insides (%s): %q", leak, text)
	}
	return nil
}

// Refusal fails tb when Check finds something wrong with err.
func Refusal(tb testing.TB, err error, allowed []connect.Code, inputs ...string) {
	tb.Helper()
	if verr := Check(err, allowed, inputs...); verr != nil {
		tb.Fatal(verr)
	}
}

// Text fails tb when CheckText finds something wrong with text.
func Text(tb testing.TB, text string, inputs ...string) {
	tb.Helper()
	if verr := CheckText(text, inputs...); verr != nil {
		tb.Fatal(verr)
	}
}
