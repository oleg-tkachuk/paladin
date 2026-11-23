package fuzzassert

import (
	"errors"
	"fmt"
	"strconv"
	"testing"

	"connectrpc.com/connect/v2"
)

func TestCheck(t *testing.T) {
	invalid := []connect.Code{connect.CodeInvalidArgument}
	cases := []struct {
		name    string
		err     error
		allowed []connect.Code
		inputs  []string
		ok      bool
	}{
		{"no error", nil, invalid, nil, true},
		{"an allowed refusal", connect.NewError(connect.CodeInvalidArgument, `key "a//b": not canonical`), invalid, nil, true},
		{"a wrapped connect error", fmt.Errorf("ctx: %w", connect.NewError(connect.CodeInvalidArgument, "bad")), invalid, nil, true},
		{"a plain error reads as Unknown", errors.New("bad"), invalid, nil, false},
		{"Internal for client input", connect.NewError(connect.CodeInternal, "lookup failed"), invalid, nil, false},
		{"a code the handler does not answer with", connect.NewError(connect.CodeNotFound, "missing"), invalid, nil, false},
		{"an allowed code with a leaking message", connect.NewError(connect.CodeInvalidArgument, "sql: no rows in result set"), invalid, nil, false},
		{"the client's own text echoed back", connect.NewError(connect.CodeInvalidArgument, fmt.Sprintf("name %q is not valid", "sql: x")), invalid, []string{"sql: x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Check(tc.err, tc.allowed, tc.inputs...)
			if (err == nil) != tc.ok {
				t.Errorf("Check = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestCheckText(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		inputs []string
		ok     bool
	}{
		{"plain refusal", `collection "docs" not found`, nil, true},
		{"printable non-ASCII", `delimiter "→" is one character`, nil, true},
		{"input quoted with %q", "key " + strconv.Quote("a\x00b\xff") + " refused", nil, true},
		{"empty", "", nil, true},

		{"invalid UTF-8", "key a\xffb", nil, false},
		{"a raw NUL", "key a\x00b", nil, false},
		{"a raw newline", "line one\nline two", nil, false},
		{"a raw escape sequence", "key \x1b[31mred", nil, false},
		{"a bidi override", "key a\u202eb", nil, false},

		{"an error's Go type", "unexpected *errors.errorString", nil, false},
		{"a pointer struct", "got &{a b}", nil, false},
		{"a strconv error", `strconv.ParseInt: parsing "x": invalid syntax`, nil, false},
		{"a json error", "json: cannot unmarshal string", nil, false},
		{"a proto error", "proto: cannot parse invalid wire-format data", nil, false},
		{"the driver", "pgx: conn closed", nil, false},
		{"a driver error", "pgconn: connect failed", nil, false},
		{"a SQL state", "violates constraint (SQLSTATE 23505)", nil, false},
		{"a database/sql error", "sql: no rows in result set", nil, false},
		{"a source line", "at handler.go:42", nil, false},
		{"a goroutine header", "goroutine 7 [running]", nil, false},
		{"a runtime error", "runtime.Error: index out of range", nil, false},
		{"a recovered panic", "panic(0x1)", nil, false},
		{"a bad verb", "size %!d(string=x)", nil, false},
		{"a nil printed", "error: <nil>", nil, false},

		{"a file name the client sent", `key "report.go" refused: too long`, []string{"report.go"}, true},
		{"a leak pattern the client sent", `filter "pgx == 1" does not compile`, []string{"pgx == 1"}, true},
		{"a leak no input holds", `filter "x" failed: pgx: conn closed`, []string{"x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckText(tc.text, tc.inputs...)
			if (err == nil) != tc.ok {
				t.Errorf("CheckText(%q) = %v, want ok=%v", tc.text, err, tc.ok)
			}
		})
	}
}

// Every client code is a refusal; the two that mean the server failed are
// not among them.
func TestClientCodesHoldNoServerFailure(t *testing.T) {
	for _, c := range ClientCodes {
		if c == connect.CodeInternal || c == connect.CodeUnknown {
			t.Errorf("ClientCodes holds %v", c)
		}
	}
}
