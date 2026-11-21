package bucketgate

import (
	"errors"
	"testing"

	"connectrpc.com/connect/v2"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// The states a bucket's backend and row can be in, as the table below names
// them.
var (
	healthy      = State{Enabled: true, ProvisionState: ProvisionReady}
	disabled     = State{Enabled: false, ProvisionState: ProvisionReady}
	draining     = State{Enabled: true, ReadOnly: true, ProvisionState: ProvisionReady}
	pending      = State{Enabled: true, ProvisionState: ProvisionPending}
	failed       = State{Enabled: true, ProvisionState: ProvisionFailed}
	deleting     = State{Enabled: true, ProvisionState: ProvisionDeleting}
	unknownState = State{Enabled: true, ProvisionState: "unheard-of"}
	// A disabled backend that is also draining a pending bucket: the most
	// final refusal answers.
	disabledDraining = State{Enabled: false, ReadOnly: true, ProvisionState: ProvisionPending}
	// Draining a bucket still provisioning: retrying does not help while the
	// backend drains, so the drain answers.
	drainingPending = State{Enabled: true, ReadOnly: true, ProvisionState: ProvisionPending}
)

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  State
		access Access
		want   error
	}{
		{"healthy read", healthy, Read, nil},
		{"healthy write", healthy, Write, nil},
		{"disabled read", disabled, Read, ErrBackendDisabled},
		{"disabled write", disabled, Write, ErrBackendDisabled},
		{"draining read", draining, Read, nil},
		{"draining write", draining, Write, ErrBackendReadOnly},
		{"pending read", pending, Read, nil},
		{"pending write", pending, Write, ErrBucketProvisioning},
		{"failed read", failed, Read, nil},
		{"failed write", failed, Write, ErrBucketNotReady},
		{"deleting read", deleting, Read, nil},
		{"deleting write", deleting, Write, ErrBucketNotReady},
		{"unknown provision state write", unknownState, Write, ErrBucketNotReady},
		{"disabled and draining write", disabledDraining, Write, ErrBackendDisabled},
		{"draining and pending write", drainingPending, Write, ErrBackendReadOnly},
		{"draining and pending read", drainingPending, Read, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Check(tc.access, tc.state); !errors.Is(got, tc.want) || (got == nil) != (tc.want == nil) {
				t.Fatalf("Check(%v, %+v) = %v, want %v", tc.access, tc.state, got, tc.want)
			}
		})
	}
}

// An access the gate does not know is gated as a write: a caller that forgot
// to say is refused rather than let through.
func TestCheckTreatsAnUnknownAccessAsAWrite(t *testing.T) {
	const unknown Access = 0
	if got := Check(unknown, draining); !errors.Is(got, ErrBackendReadOnly) {
		t.Fatalf("Check(unknown, draining) = %v, want %v", got, ErrBackendReadOnly)
	}
}

// Each refusal answers the code its doc table promises — FAILED_PRECONDITION
// where only an operator changes the state, UNAVAILABLE where waiting does —
// and a reason a client can read.
func TestRefusalCodes(t *testing.T) {
	for _, tc := range []struct {
		err    *Refusal
		code   connect.Code
		reason commonv1.ErrorReason
	}{
		{ErrBackendDisabled, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_BACKEND_DISABLED},
		{ErrBackendReadOnly, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_BACKEND_READ_ONLY},
		{ErrBucketProvisioning, connect.CodeUnavailable, commonv1.ErrorReason_ERROR_REASON_BUCKET_PROVISIONING},
		{ErrBucketNotReady, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_FAILED_PRECONDITION},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			if tc.err.Code != tc.code || tc.err.Reason != tc.reason {
				t.Fatalf("%q = (%v, %v), want (%v, %v)", tc.err, tc.err.Code, tc.err.Reason, tc.code, tc.reason)
			}
		})
	}
}

// Refusals lists every refusal Check can answer, once, so registering them
// with the error mapper misses none.
func TestRefusalsListsEveryRefusalOnce(t *testing.T) {
	want := []*Refusal{ErrBackendDisabled, ErrBackendReadOnly, ErrBucketProvisioning, ErrBucketNotReady}
	got := Refusals()
	if len(got) != len(want) {
		t.Fatalf("Refusals() = %d refusals, want %d", len(got), len(want))
	}
	seen := map[*Refusal]bool{}
	for _, r := range got {
		seen[r] = true
	}
	for _, r := range want {
		if !seen[r] {
			t.Errorf("Refusals() misses %q", r)
		}
	}
}

func TestAccessOf(t *testing.T) {
	if got := AccessOf(true); got != Write {
		t.Errorf("AccessOf(true) = %v, want Write", got)
	}
	if got := AccessOf(false); got != Read {
		t.Errorf("AccessOf(false) = %v, want Read", got)
	}
}

func TestRefusalMessages(t *testing.T) {
	for _, tc := range []struct {
		err  *Refusal
		want string
	}{
		{ErrBackendDisabled, "storage backend is disabled"},
		{ErrBackendReadOnly, "storage backend is read-only (draining)"},
		{ErrBucketProvisioning, "storage bucket is still provisioning"},
		{ErrBucketNotReady, "storage bucket is not ready for writes"},
	} {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("Error() = %q, want %q", got, tc.want)
		}
	}
}
