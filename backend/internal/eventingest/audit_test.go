package eventingest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

type recordedAudit struct {
	rows []admindomain.AuditEntry
	err  error
}

func (r *recordedAudit) Insert(_ context.Context, e admindomain.AuditEntry) error {
	if r.err != nil {
		return r.err
	}
	r.rows = append(r.rows, e)
	return nil
}

// promoterOutcome promotes with a fixed outcome, running the fan-out as the
// real transition does when it changed the row.
type promoterOutcome struct{ changed bool }

func (p promoterOutcome) PromoteToAvailableInTx(ctx context.Context, _ uuid.UUID, _ string, _ int64, _, _ string, _ statemachine.Source, onPromoted func(context.Context, pgx.Tx) error) (bool, error) {
	if p.changed {
		if err := onPromoted(ctx, nil); err != nil {
			return false, err
		}
	}
	return p.changed, nil
}

const ingestedSize = 42

func uploadFixture() (uuid.UUID, uuid.UUID, *rowLookup, CloudEvent) {
	id, tenant := uuid.New(), uuid.New()
	lookup := &rowLookup{row: sqlc.Object{
		ID: pgtype.UUID{Bytes: id, Valid: true}, TenantID: pgtype.UUID{Bytes: tenant, Valid: true},
		State: sqlc.ObjectStatePENDING,
	}}
	ev := CloudEvent{ID: "evt-1", Type: EventTypeUploaded, Source: "s3://assets", SubjectFields: SubjectFields{
		TenantID: tenant.String(), Collection: "docs", Key: "a.txt", SizeBytes: ingestedSize, Etag: "etag-1",
	}}
	return id, tenant, lookup, ev
}

// A storage event that promoted an object is recorded as the pipeline's, in
// the object's tenant, once: a redelivery that changed nothing is not.
func TestIngestRecordsAPromotion(t *testing.T) {
	for _, changed := range []bool{true, false} {
		_, tenant, lookup, ev := uploadFixture()
		audit := &recordedAudit{}
		h := &PromoteHandler{Lookup: lookup, Transitioner: promoterOutcome{changed: changed}, Audit: audit, Logger: zap.NewNop()}
		if err := h.Handle(context.Background(), ev); err != nil {
			t.Fatalf("Handle = %v", err)
		}
		if !changed {
			if len(audit.rows) != 0 {
				t.Errorf("an unchanged object was recorded: %+v", audit.rows)
			}
			continue
		}
		if len(audit.rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(audit.rows))
		}
		e := audit.rows[0]
		if e.Action != actionObjectUploaded || e.ActorSubject != ingestActor || e.ActorTenantID != tenant || e.ResourceName == "" {
			t.Errorf("row = %+v", e)
		}
	}
}

// Bytes gone from the store that moved an object to the trash are recorded.
func TestIngestRecordsADeletion(t *testing.T) {
	_, lookup, ev := deleteFixture(sqlc.ObjectStateAVAILABLE)
	tenant := uuid.MustParse(ev.SubjectFields.TenantID)
	lookup.row.TenantID = pgtype.UUID{Bytes: tenant, Valid: true}
	audit := &recordedAudit{}
	h := &PromoteHandler{Lookup: lookup, Probe: &fakeProbe{}, Deleter: &fakeDeleter{}, Audit: audit, Logger: zap.NewNop()}
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("Handle = %v", err)
	}
	if len(audit.rows) != 1 || audit.rows[0].Action != actionObjectDeleted || audit.rows[0].ActorTenantID != tenant {
		t.Errorf("rows = %+v", audit.rows)
	}
}

// A row that could not be written does not undo the transition, and is
// logged.
func TestIngestLogsAnUnwrittenRow(t *testing.T) {
	_, _, lookup, ev := uploadFixture()
	core, logs := observer.New(zap.ErrorLevel)
	h := &PromoteHandler{Lookup: lookup, Transitioner: promoterOutcome{changed: true},
		Audit: &recordedAudit{err: errors.New("pool closed")}, Logger: zap.New(core)}
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("Handle = %v, want the promotion to stand", err)
	}
	if logs.FilterMessage("audit entry not written").Len() != 1 {
		t.Errorf("logged %v", logs.All())
	}
}
