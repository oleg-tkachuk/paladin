package tenanth

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// recordingProducer keeps what the handler records on the tenant mutation's
// transaction, and fails it when err is set.
type recordingProducer struct {
	tenant string
	events []worker.Event
	err    error
}

func (p *recordingProducer) RecordTx(_ context.Context, _ pgx.Tx, tenantID string, evt worker.Event) error {
	p.tenant = tenantID
	p.events = append(p.events, evt)
	return p.err
}

// Each lifecycle change records its event on its own transaction, naming the
// tenant, its resource and the caller. The types are the wire contract a
// subscriber filters on, so they are spelled out here rather than taken from
// the constants the handler uses.
func TestTenantLifecycleRecordsItsEvent(t *testing.T) {
	tid := uuid.New()
	repo := &fakeRepo{
		updateTxFn: func(_ context.Context, _ pgx.Tx, a UpdateTenantArgs) (Tenant, error) {
			return Tenant{TenantID: a.TenantID, Slug: "acme"}, nil
		},
		restoreTxFn: func(_ context.Context, _ pgx.Tx, id uuid.UUID) (Tenant, error) {
			return Tenant{TenantID: id, Slug: "acme"}, nil
		},
	}
	for _, tc := range []struct {
		wantType string
		call     func(*Handler) error
	}{
		{"paladin.tenant.created", func(h *Handler) error {
			_, err := h.CreateTenant(adminCtx(tid), CreateTenantArgs{TenantID: tid, Slug: "acme"})
			return err
		}},
		{"paladin.tenant.updated", func(h *Handler) error {
			_, err := h.UpdateTenant(adminCtx(tid), UpdateTenantArgs{TenantID: tid, DisplayName: strptr("Acme")})
			return err
		}},
		{"paladin.tenant.trashed", func(h *Handler) error {
			return h.DeleteTenant(adminCtx(tid), tid, 0)
		}},
		{"paladin.tenant.restored", func(h *Handler) error {
			_, err := h.RestoreTenant(adminCtx(tid), tid)
			return err
		}},
	} {
		t.Run(tc.wantType, func(t *testing.T) {
			producer := &recordingProducer{}
			h := NewHandler(repo, allow())
			h.SetEventProducer(producer)
			if err := tc.call(h); err != nil {
				t.Fatalf("call: %v", err)
			}
			if len(producer.events) != 1 {
				t.Fatalf("recorded %d events, want 1", len(producer.events))
			}
			e := producer.events[0]
			if e.Type != tc.wantType {
				t.Errorf("type = %q, want %q", e.Type, tc.wantType)
			}
			if producer.tenant != tid.String() || e.TenantID != tid.String() {
				t.Errorf("recorded for %q / %q, want %s", producer.tenant, e.TenantID, tid)
			}
			if e.ResourceName != "tenants/"+tid.String() {
				t.Errorf("resource = %q", e.ResourceName)
			}
			// adminCtx's principal is "u1".
			if e.ActorSubject != "u1" {
				t.Errorf("actor = %q, want u1", e.ActorSubject)
			}
		})
	}
}

// The event goes on the mutation's transaction, so a failure to record it
// fails the call: the client retries both rather than losing the event.
func TestTenantCreateFailsWhenItsEventCannotBeRecorded(t *testing.T) {
	tid := uuid.New()
	boom := errors.New("outbox full")
	h := NewHandler(&fakeRepo{}, allow())
	h.SetEventProducer(&recordingProducer{err: boom})
	_, err := h.CreateTenant(adminCtx(tid), CreateTenantArgs{TenantID: tid, Slug: "acme"})
	if err == nil {
		t.Fatal("CreateTenant succeeded with its event unrecorded")
	}
}

// The created event carries what was created, as the row has it.
func TestTenantCreatedEventCarriesTheTenant(t *testing.T) {
	tid := uuid.New()
	producer := &recordingProducer{}
	h := NewHandler(&fakeRepo{}, allow())
	h.SetEventProducer(producer)
	if _, err := h.CreateTenant(adminCtx(tid), CreateTenantArgs{TenantID: tid, Slug: "acme", DisplayName: " Acme "}); err != nil {
		t.Fatal(err)
	}
	p := producer.events[0].Payload
	if p["tenant_id"] != tid.String() || p["slug"] != "acme" || p["display_name"] != "Acme" {
		t.Fatalf("payload = %v", p)
	}
}
