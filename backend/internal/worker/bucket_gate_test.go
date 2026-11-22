package worker

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
)

// gateBucket is a bucket in the given state, under a fresh id and a name the
// test can find it by in the log.
func gateBucket(name string, enabled, readOnly bool, provision string) bucketGateRow {
	return bucketGateRow{
		ID:          pgtype.UUID{Bytes: uuid.New(), Valid: true},
		BackendName: "be-" + name, BucketName: name,
		Enabled: enabled, ReadOnly: readOnly, ProvisionState: provision,
	}
}

var (
	gateHealthy  = gateBucket("healthy", true, false, bucketgate.ProvisionReady)
	gateDraining = gateBucket("draining", true, true, bucketgate.ProvisionReady)
	gateDisabled = gateBucket("disabled", false, false, bucketgate.ProvisionReady)
	gatePending  = gateBucket("pending", true, false, bucketgate.ProvisionPending)
	gateDeleting = gateBucket("deleting", true, false, bucketgate.ProvisionDeleting)
)

// admit applies bucketgate.Check row by row: what it admits is what Check
// lets through, each bucket it refuses is logged once, at Warn, with the
// gate's reason, and deferred from then on.
func TestBucketDeferralsAdmit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		access bucketgate.Access
		rows   []bucketGateRow
		admit  []bool
		refuse map[string]error
	}{
		{"a write of bytes is refused on a draining or disabled backend and an unready bucket",
			bucketgate.BytesWrite,
			[]bucketGateRow{gateDraining, gateHealthy, gateDisabled, gatePending, gateDeleting, gateDraining},
			[]bool{false, true, false, false, false, false},
			map[string]error{
				gateDraining.BucketName: bucketgate.ErrBackendReadOnly,
				gateDisabled.BucketName: bucketgate.ErrBackendDisabled,
				gatePending.BucketName:  bucketgate.ErrBucketProvisioning,
				gateDeleting.BucketName: bucketgate.ErrBucketNotReady,
			}},
		{"a read is refused on a disabled backend alone",
			bucketgate.Read,
			[]bucketGateRow{gateDraining, gateDisabled, gateHealthy, gatePending, gateDisabled},
			[]bool{true, false, true, true, false},
			map[string]error{gateDisabled.BucketName: bucketgate.ErrBackendDisabled}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			gate := newBucketDeferrals(zap.New(core), tc.access)
			for i, r := range tc.rows {
				if got := gate.admit(r); got != tc.admit[i] {
					t.Errorf("row %d (%s): admit = %v, want %v", i, r.BucketName, got, tc.admit[i])
				}
			}
			if got := len(gate.deferred()); got != len(tc.refuse) {
				t.Errorf("deferred %d buckets, want each refused one once (%d)", got, len(tc.refuse))
			}

			entries := logs.All()
			if len(entries) != len(tc.refuse) {
				t.Fatalf("%d log lines, want one per refused bucket (%d): %v", len(entries), len(tc.refuse), entries)
			}
			for _, e := range entries {
				fields := e.ContextMap()
				bucket, _ := fields["bucket_name"].(string)
				reason, ok := tc.refuse[bucket]
				if !ok {
					t.Errorf("logged bucket %q, which the gate admits", bucket)
					continue
				}
				if e.Level != zapcore.WarnLevel {
					t.Errorf("%s logged at %s, want warn", bucket, e.Level)
				}
				if fields["reason"] != reason.Error() {
					t.Errorf("%s reason = %v, want %q", bucket, fields["reason"], reason.Error())
				}
				if fields["backend_id"] != "be-"+bucket {
					t.Errorf("%s backend_id = %v, want be-%s", bucket, fields["backend_id"], bucket)
				}
				if fields["access"] != tc.access.String() {
					t.Errorf("%s access = %v, want %s", bucket, fields["access"], tc.access)
				}
			}
		})
	}
}

// A fresh deferral set defers nothing, as an empty array the claim can send.
func TestBucketDeferralsStartEmpty(t *testing.T) {
	d := newBucketDeferrals(zap.NewNop(), bucketgate.BytesWrite)
	if d.deferred() == nil || len(d.deferred()) != 0 {
		t.Fatalf("deferred = %#v, want an empty, non-nil slice", d.deferred())
	}
}

// fakeClaims is a work queue in claim order: each claim returns the first
// rows whose bucket is not deferred, up to the limit, as the SQL does.
type fakeClaims struct {
	queue  []bucketGateRow
	claims []int32
}

func (f *fakeClaims) claim(limit int32, deferred []pgtype.UUID) ([]bucketGateRow, error) {
	f.claims = append(f.claims, limit)
	var out []bucketGateRow
	for _, r := range f.queue {
		if len(out) == int(limit) {
			break
		}
		if !slices.Contains(deferred, r.ID) {
			out = append(out, r)
		}
	}
	return out, nil
}

// take removes a handled row from the queue, as the work does.
func (f *fakeClaims) take(r bucketGateRow) {
	f.queue = slices.DeleteFunc(f.queue, func(q bucketGateRow) bool { return q == r })
}

func identity(r bucketGateRow) bucketGateRow { return r }

// rowsIn returns n rows of bucket b, each its own work item.
func rowsIn(b bucketGateRow, n int) []bucketGateRow {
	out := make([]bucketGateRow, n)
	for i := range out {
		out[i] = b
		out[i].BackendName = b.BackendName + "-" + uuid.New().String()[:4]
	}
	return out
}

func TestClaimAdmitted(t *testing.T) {
	const limit = 3
	for _, tc := range []struct {
		name string
		// queue is the due work, in claim order.
		queue []bucketGateRow
		// handled is the rows work is handed, and claims the limit each
		// claim was issued with.
		handled []bucketGateRow
		claims  []int32
	}{
		{"nothing due", nil, nil, []int32{limit}},
		{"a short claim is the last", rowsIn(gateHealthy, 2), rowsIn(gateHealthy, 2), []int32{limit}},
		{"a full claim of admitted rows fills the batch",
			rowsIn(gateHealthy, 5), rowsIn(gateHealthy, 3), []int32{limit}},
		{"a refused bucket heading the queue is claimed past, not waited on",
			append(rowsIn(gateDraining, 5), rowsIn(gateHealthy, 2)...),
			rowsIn(gateHealthy, 2), []int32{limit, limit}},
		{"the slots a refused bucket took are claimed again, and no more",
			append(append(rowsIn(gateHealthy, 1), rowsIn(gateDisabled, 1)...), rowsIn(gateHealthy, 4)...),
			rowsIn(gateHealthy, 3), []int32{limit, 1}},
		{"a queue of refused buckets ends at the claim that comes back short",
			append(append(rowsIn(gateDraining, 1), rowsIn(gateDisabled, 3)...), rowsIn(gatePending, 2)...),
			nil, []int32{limit, limit}},
		{"each newly refused bucket costs one claim at most",
			append(append(append(rowsIn(gateDraining, 3), rowsIn(gateDisabled, 3)...), rowsIn(gatePending, 3)...),
				rowsIn(gateDeleting, 3)...),
			nil, []int32{limit, limit, limit, limit, limit}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeClaims{queue: tc.queue}
			var handled []bucketGateRow
			gate := newBucketDeferrals(zap.NewNop(), bucketgate.BytesWrite)
			n, err := claimAdmitted(context.Background(), gate, limit, f.claim, identity, func(r bucketGateRow) error {
				handled = append(handled, r)
				f.take(r)
				return nil
			})
			if err != nil {
				t.Fatalf("claimAdmitted = %v", err)
			}
			if int(n) != len(handled) {
				t.Errorf("reported %d handled, work saw %d", n, len(handled))
			}
			if len(handled) != len(tc.handled) {
				t.Fatalf("handled %d rows, want %d", len(handled), len(tc.handled))
			}
			for _, r := range handled {
				if r.ID != gateHealthy.ID {
					t.Errorf("handled a row of %s, which the gate refuses", r.BucketName)
				}
			}
			if !slices.Equal(f.claims, tc.claims) {
				t.Errorf("claims issued with limits %v, want %v", f.claims, tc.claims)
			}
		})
	}
}

// A claim or a piece of work that fails stops the loop with its error.
func TestClaimAdmittedStopsAtTheFirstError(t *testing.T) {
	boom := errors.New("boom")
	gate := newBucketDeferrals(zap.NewNop(), bucketgate.BytesWrite)
	_, err := claimAdmitted(context.Background(), gate, 1,
		func(int32, []pgtype.UUID) ([]bucketGateRow, error) { return nil, boom },
		identity, func(bucketGateRow) error { return nil })
	if !errors.Is(err, boom) {
		t.Errorf("claim failure: err = %v, want %v", err, boom)
	}

	f := &fakeClaims{queue: rowsIn(gateHealthy, 2)}
	calls := 0
	n, err := claimAdmitted(context.Background(), gate, 2, f.claim, identity, func(bucketGateRow) error {
		calls++
		return boom
	})
	if !errors.Is(err, boom) || n != 1 || calls != 1 {
		t.Errorf("work failure: n=%d calls=%d err=%v, want 1, 1, %v", n, calls, err, boom)
	}
}

// Once the context is done no further row is handed to work, and the loop
// answers the context's error.
func TestClaimAdmittedStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakeClaims{queue: rowsIn(gateHealthy, 3)}
	gate := newBucketDeferrals(zap.NewNop(), bucketgate.BytesWrite)
	calls := 0
	n, err := claimAdmitted(ctx, gate, 3, f.claim, identity, func(bucketGateRow) error {
		calls++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || n != 1 || calls != 1 {
		t.Errorf("n=%d calls=%d err=%v, want 1, 1, %v", n, calls, err, context.Canceled)
	}
}

// A tick cut short by its context is a stop, not a failure: nothing logged,
// nil returned. Any other cause is logged at Warn and returned.
func TestTickCutShort(t *testing.T) {
	boom := errors.New("boom")
	const msg = "tick cut short"

	done, cancel := context.WithCancel(context.Background())
	cancel()
	core, logs := observer.New(zap.DebugLevel)
	if err := tickCutShort(done, zap.New(core), msg, boom); err != nil {
		t.Errorf("ended context: tickCutShort = %v, want nil", err)
	}
	if logs.Len() != 0 {
		t.Errorf("ended context: logged %v, want nothing", logs.All())
	}

	core, logs = observer.New(zap.DebugLevel)
	if err := tickCutShort(context.Background(), zap.New(core), msg, boom); !errors.Is(err, boom) {
		t.Errorf("live context: tickCutShort = %v, want %v", err, boom)
	}
	entries := logs.All()
	if len(entries) != 1 || entries[0].Message != msg || entries[0].Level != zapcore.WarnLevel {
		t.Errorf("live context: logged %v, want one Warn %q", entries, msg)
	}
}
