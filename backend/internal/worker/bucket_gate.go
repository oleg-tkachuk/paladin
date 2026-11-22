package worker

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
)

// This file holds how a background worker asks the bucket gate (ADR-0015)
// before it touches a backend. Request paths ask it in the bucket resolver;
// the workers that pay debts against a backend — removing bytes a deleted row
// left behind, aborting multipart parts — ask it here, through the same
// bucketgate.Check, so a backend an operator disabled or set draining is
// spared by them as it is by the API.
//
// Each claim is the worker's own capped query, and each row it returns
// carries its bucket's state. The gate is asked about every row; a bucket it
// refuses is logged once and passed back to the next claim, which leaves its
// rows out. A refused bucket's work is deferred, not failed and not dropped:
// its rows stay as they are — no attempt counted, no backoff grown — and the
// first tick whose gate admits the bucket again takes them. Claiming again
// without the refused buckets is what keeps them from heading every claim and
// starving the buckets behind them, while every query stays capped.

// bucketGateRow is the bucket a claimed row belongs to, with the state the
// gate decides on.
type bucketGateRow struct {
	ID             pgtype.UUID
	BackendName    string
	BucketName     string
	Enabled        bool
	ReadOnly       bool
	ProvisionState string
}

// bucketDeferrals is one tick's answers from the gate for one access: the
// buckets it refused, each logged once.
type bucketDeferrals struct {
	log     *zap.Logger
	access  bucketgate.Access
	refused map[[16]byte]bool
	ids     []pgtype.UUID
}

func newBucketDeferrals(log *zap.Logger, access bucketgate.Access) *bucketDeferrals {
	return &bucketDeferrals{log: log, access: access, refused: map[[16]byte]bool{}, ids: []pgtype.UUID{}}
}

// admit reports whether the gate lets the access through on b. A bucket it
// refuses is logged with the gate's reason the first time, and deferred from
// then on.
func (d *bucketDeferrals) admit(b bucketGateRow) bool {
	if d.refused[b.ID.Bytes] {
		return false
	}
	err := bucketgate.Check(d.access, bucketgate.State{
		Enabled: b.Enabled, ReadOnly: b.ReadOnly, ProvisionState: b.ProvisionState,
	})
	if err == nil {
		return true
	}
	d.refused[b.ID.Bytes] = true
	d.ids = append(d.ids, b.ID)
	d.log.Warn("the bucket gate refuses this bucket; its work is deferred until the gate admits it",
		zap.String("backend_id", b.BackendName),
		zap.String("bucket_name", b.BucketName),
		zap.Stringer("access", d.access),
		zap.String("reason", err.Error()))
	return false
}

// deferred is the buckets refused so far, for the next claim to leave out.
func (d *bucketDeferrals) deferred() []pgtype.UUID { return d.ids }

// claimAdmitted claims up to limit rows the gate admits and hands each to
// work, in claim order. A claim that refuses a bucket it had not refused
// before is followed by another, without that bucket, for the slots its rows
// took; a claim that refuses nothing new, or comes back short, is the last.
// So it issues at most one claim more than there are buckets refused, each
// capped by what is left of limit.
//
// It returns how many rows work was handed, and stops at the first error
// claim or work returns, or with ctx's error before a row once ctx is done.
func claimAdmitted[R any](
	ctx context.Context, gate *bucketDeferrals, limit int32,
	claim func(limit int32, deferred []pgtype.UUID) ([]R, error),
	bucketOf func(R) bucketGateRow,
	work func(R) error,
) (int32, error) {
	var handled int32
	for handled < limit {
		want := limit - handled
		rows, err := claim(want, gate.deferred())
		if err != nil {
			return handled, err
		}
		before := len(gate.deferred())
		for _, r := range rows {
			if err := ctx.Err(); err != nil {
				return handled, err
			}
			if !gate.admit(bucketOf(r)) {
				continue
			}
			handled++
			if err := work(r); err != nil {
				return handled, err
			}
		}
		if len(gate.deferred()) == before || len(rows) < int(want) {
			return handled, nil
		}
	}
	return handled, nil
}

// tickCutShort answers what a tick that claimAdmitted stopped with err
// returns: nil when ctx ended it, since the worker is stopping and nothing
// failed, and otherwise err, logged with msg.
func tickCutShort(ctx context.Context, log *zap.Logger, msg string, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	log.Warn(msg, zap.Error(err))
	return err
}

// mutate4go-manifest-begin
// {"version":1,"tested_at":"2026-11-22T13:57:53+02:00","module_hash":"4d1d76d95df7270bb18f3279ad4594fc8876291add6869259c94fe73588bba53","functions":[{"id":"func/newBucketDeferrals","name":"newBucketDeferrals","line":48,"end_line":50,"hash":"ae0cce5621a8dd7f70df96b327b1c3985409c368608f5e0778638ca68fb49057"},{"id":"func/bucketDeferrals.admit","name":"bucketDeferrals.admit","line":55,"end_line":73,"hash":"6fb895dc56d2b90666b1820194fb11399eb806f72717dcb701a06ab45c7f27e0"},{"id":"func/bucketDeferrals.deferred","name":"bucketDeferrals.deferred","line":76,"end_line":76,"hash":"eb8e5f5ba759df1cc2d66871ceac6ac4930febdc2b252b4c438747dc35c17251"},{"id":"func/claimAdmitted","name":"claimAdmitted","line":87,"end_line":118,"hash":"2d422c51f5e78aa5f76ebf228cb4314570a23783d0ba0c40d2cbf118db148e25"},{"id":"func/tickCutShort","name":"tickCutShort","line":123,"end_line":129,"hash":"837b5e1b19da13a52a52bf1c4a17e9b437f2cb11a70d67baac248a2bc2a48a92"}]}
// mutate4go-manifest-end
