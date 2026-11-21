package middleware

import (
	"cmp"
	"errors"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// driverSentinels are the driver's own errors, matched by identity: a store's
// wrap of one satisfies errors.Is as well, and the wrap's own words are the
// store's to keep.
var driverSentinels = []error{pgx.ErrNoRows, pgx.ErrTooManyRows}

// wrapJoin is what a %w wrap puts between a message and the error it wraps.
const wrapJoin = ": "

// messageTrim are the characters a removed driver text leaves at either end
// of a message.
const messageTrim = ": "

// A code's own words are its name with these separators turned into spaces:
// already_exists reads "already exists".
const (
	codeNameSep = "_"
	codeWordSep = " "
)

// carriesDriverText reports whether err is a refusal whose message holds the
// text of a database driver's error — the defect scrub repairs and the server
// logs. A server fault is not one: its whole message is replaced anyway.
func carriesDriverText(err error) bool {
	cerr, ok := errors.AsType[*connect.Error](err)
	if !ok || scrubbedCodes[cerr.Code()] {
		return false
	}
	return withoutDriverText(cerr.Message(), cerr.Code(), driverTexts(err)) != cerr.Message()
}

// isDriverError reports whether e itself, rather than an error wrapping it,
// is the database driver's: a server error with its constraint, table and
// SQLSTATE, a connection failure with its host, or one of its sentinels.
func isDriverError(e error) bool {
	switch e.(type) { //nolint:errorlint // the node itself, not what wraps it: walkErrors visits each
	case *pgconn.PgError, *pgconn.ConnectError:
		return true
	}
	return slices.Contains(driverSentinels, e)
}

// driverTexts is the text of every driver error in err's tree, and of the
// driver's sentinels, which a %v may have flattened into a message with no
// chain left to find them by. Longest first, so an error that quotes another
// goes whole.
func driverTexts(err error) []string {
	texts := make([]string, 0, len(driverSentinels))
	for _, s := range driverSentinels {
		texts = append(texts, s.Error())
	}
	walkErrors(err, func(e error) {
		if isDriverError(e) {
			texts = append(texts, e.Error())
		}
	})
	slices.SortFunc(texts, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	return texts
}

// walkErrors visits err and everything it wraps, through both a single wrap
// and a join.
func walkErrors(err error, visit func(error)) {
	if err == nil {
		return
	}
	visit(err)
	switch u := err.(type) { //nolint:errorlint // walks the tree errors.Is would, one node at a time
	case interface{ Unwrap() error }:
		walkErrors(u.Unwrap(), visit)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			walkErrors(e, visit)
		}
	}
}

// withoutDriverText is msg without texts, and without the joins that put them
// there. A message that was nothing but a driver's error becomes code's own
// words, so the caller is not answered with an empty message.
func withoutDriverText(msg string, code connect.Code, texts []string) string {
	out := msg
	for _, t := range texts {
		out = strings.ReplaceAll(out, wrapJoin+t, "")
		out = strings.ReplaceAll(out, t, "")
	}
	if out == msg {
		return msg
	}
	if out = strings.Trim(out, messageTrim); out == "" {
		return strings.ReplaceAll(code.String(), codeNameSep, codeWordSep)
	}
	return out
}
