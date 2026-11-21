package pgerr

import (
	"fmt"
	"io"
)

// Withhold is an error that reads as public alone and wraps both public and
// cause: errors.Is and errors.As find each, while the text a caller is sent
// names nothing of the driver's. The operator still reads cause: the RPC log
// line looks for a driver error the message does not quote, and a log line
// written with zap.Error carries it as errorVerbose, through %+v.
//
// For a refusal built from a Postgres error: the constraint or table it names
// is the operator's to read, the reason is the caller's.
func Withhold(public, cause error) error {
	return withheld{public: public, cause: cause}
}

type withheld struct {
	public error
	cause  error
}

// The labels the verbose form names a Postgres error's relation and
// constraint by: the text of a PgError carries neither.
const (
	verboseCauseSep  = "; withheld: "
	verboseTable     = " table="
	verboseConstrain = " constraint="
)

func (w withheld) Error() string { return w.public.Error() }

func (w withheld) Unwrap() []error { return []error{w.public, w.cause} }

// Format is the public text, and with %+v the withheld cause too, with the
// relation and constraint a Postgres error names.
func (w withheld) Format(s fmt.State, verb rune) {
	out := w.public.Error()
	if verb == 'v' && s.Flag('+') {
		out += verboseCauseSep + w.cause.Error()
		if t := Table(w.cause); t != "" {
			out += verboseTable + t
		}
		if c := Constraint(w.cause); c != "" {
			out += verboseConstrain + c
		}
	}
	_, _ = io.WriteString(s, out)
}
