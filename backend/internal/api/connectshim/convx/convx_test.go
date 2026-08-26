package convx

import (
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/protobuf/types/known/structpb"
)

// ParseRV replaced three per-plane copies, one of which — iam's hand-rolled
// digit loop — overflowed silently and disagreed with the other two about
// negatives. Those two cases lead here because they are the reason this
// package exists, not because integer parsing needs proving.
func TestParseRV(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{name: "empty is the absent guard", in: "", want: 0},
		{name: "zero", in: "0", want: 0},
		{name: "one", in: "1", want: 1},
		{name: "max int64", in: "9223372036854775807", want: 9223372036854775807},
		{
			// The iam copy returned -9223372036854775808 here, with no error:
			// a guard the caller never sent, silently manufactured.
			name:    "one past max int64 is an error, not a wrapped value",
			in:      "9223372036854775808",
			wantErr: true,
		},
		{
			name:    "far past max int64",
			in:      "99999999999999999999",
			wantErr: true,
		},
		{
			// admin/data accepted this via ParseInt; iam rejected it. Neither
			// is obviously right, but they must agree — ParseInt's behaviour
			// wins because a negative version can only come from a caller
			// that made it up, and the handlers compare it and refuse.
			name: "negative parses; the version compare rejects it downstream",
			in:   "-1",
			want: -1,
		},
		{name: "not a number", in: "abc", wantErr: true},
		{name: "trailing garbage", in: "12x", wantErr: true},
		{name: "whitespace is not trimmed", in: " 12", wantErr: true},
		{name: "float is not an integer", in: "1.5", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRV(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseRV(%q) = %d, want an error", tc.in, got)
				}
				if got != 0 {
					t.Errorf("ParseRV(%q) returned %d alongside its error; callers "+
						"that ignore the error would use it", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRV(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseRV(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestResourceVersionRoundTrip pins the pair: whatever ResourceVersion emits,
// ParseRV must read back unchanged. The zero/"" mapping is the one that
// matters — it is how "no guard" travels.
func TestResourceVersionRoundTrip(t *testing.T) {
	t.Parallel()

	for _, v := range []int64{0, 1, 42, 9223372036854775807} {
		s := ResourceVersion(v)
		back, err := ParseRV(s)
		if err != nil {
			t.Fatalf("ParseRV(ResourceVersion(%d)) = %v", v, err)
		}
		if back != v {
			t.Errorf("round trip of %d produced %d (wire form %q)", v, back, s)
		}
	}
	if got := ResourceVersion(0); got != "" {
		t.Errorf("ResourceVersion(0) = %q, want \"\" — the absent-guard form", got)
	}
}

func TestPageResponseProto(t *testing.T) {
	t.Parallel()

	// A final page must carry no page field at all. Returning an empty token
	// invites a client to request one more page forever.
	if got := PageResponseProto(""); got != nil {
		t.Errorf("PageResponseProto(\"\") = %v, want nil", got)
	}
	got := PageResponseProto("cursor-1")
	if got == nil || got.GetNextPageToken() != "cursor-1" {
		t.Errorf("PageResponseProto(\"cursor-1\") = %v", got)
	}
}

func TestTsProto(t *testing.T) {
	t.Parallel()

	// nil for the zero time keeps "unset" distinguishable from "the epoch",
	// which a naive conversion would collapse.
	if got := TsProto(time.Time{}); got != nil {
		t.Errorf("TsProto(zero) = %v, want nil", got)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if got := TsProto(now); got == nil || !got.AsTime().Equal(now) {
		t.Errorf("TsProto(%v) = %v", now, got)
	}
}

func TestTsPtrProto(t *testing.T) {
	t.Parallel()

	if got := TsPtrProto(nil); got != nil {
		t.Errorf("TsPtrProto(nil) = %v, want nil", got)
	}
	zero := time.Time{}
	if got := TsPtrProto(&zero); got != nil {
		t.Errorf("TsPtrProto(&zero) = %v, want nil", got)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if got := TsPtrProto(&now); got == nil || !got.AsTime().Equal(now) {
		t.Errorf("TsPtrProto(%v) = %v", now, got)
	}
}

// Every payload used to land in Operation.result.response, `error` left unset
// — so a client asking "did this fail?" got "no" for every failed operation.
// The failure payload has to reach the error arm, and it has to bring the
// stored details with it: for a reclaimed operation those details are the only
// record of how far the work got.
func TestOperationErrorCarriesCodeAndDetails(t *testing.T) {
	st := OperationError("WORKER_LOST", "the worker stopped",
		[]byte(`{"code":"WORKER_LOST","last_progress":{"processed":7,"total":9}}`), false)

	if st.GetCode() != int32(code.Code_ABORTED) {
		t.Errorf("code = %d, want ABORTED (%d) — the work may be half-applied, "+
			"which is exactly what ABORTED means", st.GetCode(), code.Code_ABORTED)
	}
	if st.GetMessage() != "the worker stopped" {
		t.Errorf("message = %q", st.GetMessage())
	}
	if len(st.GetDetails()) != 1 {
		t.Fatalf("details = %d, want the stored payload", len(st.GetDetails()))
	}
	var payload structpb.Struct
	if err := st.GetDetails()[0].UnmarshalTo(&payload); err != nil {
		t.Fatalf("details are not a readable struct: %v", err)
	}
	progress := payload.GetFields()["last_progress"].GetStructValue()
	if got := progress.GetFields()["processed"].GetNumberValue(); got != 7 {
		t.Errorf("processed = %v, want 7", got)
	}
}

// A cancellation is not a failure, and a payload-free row is not a crash.
func TestOperationErrorEdges(t *testing.T) {
	cancelled := OperationError("", "", nil, true)
	if cancelled.GetCode() != int32(code.Code_CANCELLED) {
		t.Errorf("cancelled code = %d, want CANCELLED", cancelled.GetCode())
	}
	if len(cancelled.GetDetails()) != 0 {
		t.Errorf("invented details for a row that stored none")
	}

	// A row with a code but no message still has to say something.
	bare := OperationError("EXEC_FAILED", "", nil, false)
	if bare.GetMessage() != "EXEC_FAILED" {
		t.Errorf("message = %q, want the code as a fallback", bare.GetMessage())
	}
	if bare.GetCode() != int32(code.Code_UNKNOWN) {
		t.Errorf("code = %d, want UNKNOWN for an executor failure", bare.GetCode())
	}
}

// Metadata that is not a JSON object is dropped, not fatal: a listing is more
// useful without one row's payload than not at all.
func TestJSONToAny(t *testing.T) {
	if JSONToAny(nil) != nil {
		t.Error("empty payload produced an Any")
	}
	if JSONToAny([]byte(`[1,2,3]`)) != nil {
		t.Error("a JSON array was packed as a Struct")
	}
	if JSONToAny([]byte(`{"processed":7}`)) == nil {
		t.Error("a JSON object was dropped")
	}
}
