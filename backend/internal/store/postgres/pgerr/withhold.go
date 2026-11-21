package pgerr

// Withhold is an error that reads as public alone and wraps both public and
// cause: errors.Is and errors.As find each, while the text a caller is sent
// names nothing of the driver's. The RPC log line looks for a driver error the
// message does not quote, so cause still reaches the operator there.
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

func (w withheld) Error() string { return w.public.Error() }

func (w withheld) Unwrap() []error { return []error{w.public, w.cause} }
