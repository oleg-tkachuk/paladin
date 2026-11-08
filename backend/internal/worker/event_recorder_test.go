package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeTx satisfies pgx.Tx by embedding the interface (nil) and overriding
// only Exec: recording an event writes one row and reads nothing, so any
// other call panics as unexpected.
type fakeTx struct {
	pgx.Tx
	sqls    []string
	args    [][]any
	execErr error
}

func (f *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.sqls = append(f.sqls, sql)
	f.args = append(f.args, args)
	return pgconn.CommandTag{}, f.execErr
}

// Recording writes exactly one event_outbox row on the caller's tx, with
// the tenant, type, time and the event itself — and nothing else: no
// subscription is read, so the cost does not depend on how many exist.
func TestRecordTx_RecordsOneEventOnTheTx(t *testing.T) {
	tenant := uuid.NewV7()
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	evt := Event{Type: "paladin.object.uploaded", At: at, TenantID: tenant.String(), ResourceName: "r"}
	tx := &fakeTx{}

	if err := (&EventRecorder{}).RecordTx(context.Background(), tx, tenant.String(), evt); err != nil {
		t.Fatalf("RecordTx: %v", err)
	}
	if len(tx.sqls) != 1 || !strings.Contains(tx.sqls[0], "INSERT INTO event_outbox") {
		t.Fatalf("statements = %q, want one INSERT INTO event_outbox", tx.sqls)
	}
	args := tx.args[0]
	if args[1] != tenant || args[2] != evt.Type || args[3] != at {
		t.Errorf("tenant, type, at = %v, %v, %v; want %v, %v, %v", args[1], args[2], args[3], tenant, evt.Type, at)
	}
	var stored Event
	if err := json.Unmarshal(args[4].([]byte), &stored); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if stored.Type != evt.Type || stored.ResourceName != evt.ResourceName {
		t.Errorf("payload = %+v, want the event", stored)
	}
}

// A failed insert is returned: it has aborted the caller's transaction, and
// the caller must know to roll back rather than commit into an error.
func TestRecordTx_ReturnsTheInsertError(t *testing.T) {
	failure := errors.New("insert failed")
	tx := &fakeTx{execErr: failure}
	err := (&EventRecorder{}).RecordTx(context.Background(), tx, uuid.NewV7().String(), Event{Type: "paladin.object.uploaded"})
	if !errors.Is(err, failure) {
		t.Fatalf("RecordTx error = %v, want %v", err, failure)
	}
}

// An event with no tenant cannot reach any subscription, so it is not
// recorded — and does not fail the producer, as it did a backend's
// credential rotation when the empty tenant id failed to parse.
func TestRecordTx_NoTenantRecordsNothing(t *testing.T) {
	tx := &fakeTx{}
	if err := (&EventRecorder{}).RecordTx(context.Background(), tx, "", Event{Type: "paladin.backend.credentials_rotated"}); err != nil {
		t.Fatalf("RecordTx: %v", err)
	}
	if len(tx.sqls) != 0 {
		t.Errorf("statements = %q, want none", tx.sqls)
	}
}

// A tenant id that is not one is a producer defect, refused before the tx
// is touched.
func TestRecordTx_RejectsAMalformedTenant(t *testing.T) {
	tx := &fakeTx{}
	if err := (&EventRecorder{}).RecordTx(context.Background(), tx, "not-a-uuid", Event{Type: "paladin.object.uploaded"}); err == nil {
		t.Fatal("RecordTx accepted a malformed tenant id")
	}
	if len(tx.sqls) != 0 {
		t.Errorf("statements = %q, want none", tx.sqls)
	}
}
