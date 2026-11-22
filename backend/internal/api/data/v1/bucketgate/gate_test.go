package bucketgate

import (
	"errors"
	"fmt"
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

// The accesses, by the name the table below prints them under.
var accesses = []struct {
	name   string
	access Access
}{
	{"bytes-write", BytesWrite},
	{"read", Read},
	{"metadata-write", MetadataWrite},
	{"protective", Protective},
}

// TestCheck runs every access against every state: the package table, cell
// by cell. A nil want is a let-through.
func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state State
		// want is indexed as accesses: bytes-write, read, metadata-write,
		// protective.
		want [4]error
	}{
		{"healthy", healthy, [4]error{nil, nil, nil, nil}},
		{"disabled", disabled, [4]error{ErrBackendDisabled, ErrBackendDisabled, ErrBackendDisabled, nil}},
		{"draining", draining, [4]error{ErrBackendReadOnly, nil, nil, nil}},
		{"pending", pending, [4]error{ErrBucketProvisioning, nil, nil, nil}},
		{"failed", failed, [4]error{ErrBucketNotReady, nil, nil, nil}},
		{"deleting", deleting, [4]error{ErrBucketNotReady, nil, nil, nil}},
		{"unknown provision state", unknownState, [4]error{ErrBucketNotReady, nil, nil, nil}},
		{"disabled and draining", disabledDraining, [4]error{ErrBackendDisabled, ErrBackendDisabled, ErrBackendDisabled, nil}},
		{"draining and pending", drainingPending, [4]error{ErrBackendReadOnly, nil, nil, nil}},
	} {
		for i, a := range accesses {
			t.Run(tc.name+" "+a.name, func(t *testing.T) {
				want := tc.want[i]
				if got := Check(a.access, tc.state); !errors.Is(got, want) || (got == nil) != (want == nil) {
					t.Fatalf("Check(%s, %+v) = %v, want %v", a.name, tc.state, got, want)
				}
			})
		}
	}
}

// An access left unset is the zero value, which is gated as a write of
// bytes: a caller that forgot to say is refused rather than let through.
func TestCheckTreatsAnUnsetAccessAsABytesWrite(t *testing.T) {
	var unset Access
	if unset != BytesWrite {
		t.Fatalf("zero Access = %+v, want BytesWrite", unset)
	}
	if got := Check(unset, draining); !errors.Is(got, ErrBackendReadOnly) {
		t.Fatalf("Check(unset, draining) = %v, want %v", got, ErrBackendReadOnly)
	}
}

// Every access is a distinct value, so comparing one with another never
// confuses two kinds.
func TestAccessesAreDistinct(t *testing.T) {
	seen := map[Access]string{}
	for _, a := range accesses {
		if other, dup := seen[a.access]; dup {
			t.Fatalf("%s and %s are the same Access", a.name, other)
		}
		seen[a.access] = a.name
	}
}

// Every refusal Check answers is Refused however it is wrapped, and so is
// logged as a refusal; an error of the gate's store, which decided nothing,
// is not.
func TestRefused(t *testing.T) {
	for _, r := range Refusals() {
		if !Refused(r) || !Refused(fmt.Errorf("resolve bucket: %w", r)) {
			t.Errorf("%v is not Refused", r)
		}
	}
	for _, err := range []error{nil, errors.New(ErrBucketProvisioning.Error()), connect.NewError(connect.CodeUnavailable, "store down")} {
		if Refused(err) {
			t.Errorf("%v is Refused; only the gate's own refusals are", err)
		}
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

func TestDeleteAccess(t *testing.T) {
	if got := DeleteAccess(true); got != BytesWrite {
		t.Errorf("DeleteAccess(permanent) = %v, want %v", got, BytesWrite)
	}
	if got := DeleteAccess(false); got != MetadataWrite {
		t.Errorf("DeleteAccess(trash) = %v, want %v", got, MetadataWrite)
	}
}

// String names every access the way the package table does.
func TestAccessString(t *testing.T) {
	for _, a := range accesses {
		if got := a.access.String(); got != a.name {
			t.Errorf("String() = %q, want %q", got, a.name)
		}
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
