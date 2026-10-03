package objecth

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/presignttl"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// Covers the handler's pure helpers, the opt-in setters, and the ADR-0003
// event fan-out guard. The RPC bodies need the full Repository/Storage/
// Authorizer wiring and are covered by the per-RPC suites.

// ─── ObjectLock.Reason ─────────────────────────────────────────────────────

func TestObjectLockReason(t *testing.T) {
	until := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)

	t.Run("legal hold wins over retention", func(t *testing.T) {
		l := ObjectLock{LegalHold: true, Mode: "COMPLIANCE", RetainUntil: &until}
		if got := l.Reason(); got != "object is under legal hold" {
			t.Errorf("Reason = %q", got)
		}
	})

	// The deadline must be in the message — a client cannot act on "locked".
	t.Run("retention names the mode and deadline", func(t *testing.T) {
		l := ObjectLock{Mode: "GOVERNANCE", RetainUntil: &until}
		got := l.Reason()
		if got != "GOVERNANCE retention lock active until 2026-07-22T10:00:00Z" {
			t.Errorf("Reason = %q", got)
		}
	})

	t.Run("bare lock", func(t *testing.T) {
		if got := (ObjectLock{}).Reason(); got != "object is locked" {
			t.Errorf("Reason = %q", got)
		}
	})
}

// ─── MapResolveErr ─────────────────────────────────────────────────────────

// This is the single chokepoint that decides how a resolution failure reaches
// the client. "Exists but not usable right now" must be FailedPrecondition so
// clients retry, while an unresolvable key stays NotFound.
func TestMapResolveErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want connect.Code
	}{
		{"disabled backend", ErrBackendDisabled, connect.CodeFailedPrecondition},
		{"read-only backend", ErrBackendReadOnly, connect.CodeFailedPrecondition},
		{"provisioning bucket", ErrBucketProvisioning, connect.CodeFailedPrecondition},
		{"anything else", errors.New("no such object key"), connect.CodeNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := connect.CodeOf(MapResolveErr(tc.err)); got != tc.want {
				t.Errorf("code = %v, want %v", got, tc.want)
			}
		})
	}
}

// Wrapped sentinels must still classify — callers wrap with context before
// they reach the mapper.
func TestMapResolveErrUnwraps(t *testing.T) {
	wrapped := errors.New("resolve bucket: " + ErrBackendDisabled.Error())
	_ = wrapped // a same-text error must NOT match

	for _, tc := range []struct {
		name string
		err  error
		want connect.Code
	}{
		{"wrapped once", errWrap(ErrBackendDisabled), connect.CodeFailedPrecondition},
		{"wrapped twice", errWrap(errWrap(ErrBackendReadOnly)), connect.CodeFailedPrecondition},
		{"same text, different error", wrapped, connect.CodeNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := connect.CodeOf(MapResolveErr(tc.err)); got != tc.want {
				t.Errorf("code = %v, want %v", got, tc.want)
			}
		})
	}
}

func errWrap(err error) error { return errors.Join(errors.New("context"), err) }

// ─── small helpers ─────────────────────────────────────────────────────────

func TestParseInt64(t *testing.T) {
	t.Run("empty is zero, not an error", func(t *testing.T) {
		got, err := parseInt64("")
		if err != nil || got != 0 {
			t.Errorf("got %d, %v", got, err)
		}
	})
	t.Run("digits", func(t *testing.T) {
		got, err := parseInt64("-42")
		if err != nil || got != -42 {
			t.Errorf("got %d, %v", got, err)
		}
	})
	t.Run("rejects garbage", func(t *testing.T) {
		if _, err := parseInt64("12abc"); err == nil {
			t.Error("want an error")
		}
	})
}

// coalesceMap picks the request's map when it has entries, otherwise the
// stored one — an explicitly empty map does NOT clear the fallback.
func TestCoalesceMap(t *testing.T) {
	primary := map[string]string{"a": "1"}
	fallback := map[string]string{"b": "2"}

	if got := coalesceMap(primary, fallback); got["a"] != "1" {
		t.Errorf("a non-empty primary must win, got %v", got)
	}
	if got := coalesceMap(nil, fallback); got["b"] != "2" {
		t.Errorf("nil primary must fall back, got %v", got)
	}
	if got := coalesceMap(map[string]string{}, fallback); got["b"] != "2" {
		t.Errorf("an empty primary must fall back, got %v", got)
	}
	if got := coalesceMap(nil, nil); got != nil {
		t.Errorf("both nil must stay nil, got %v", got)
	}
}

// ─── mapCreateErr ──────────────────────────────────────────────────────────

// A unique-index conflict is the "this key already exists" case and must
// surface as AlreadyExists, not as a generic internal error.
func TestMapCreateErrUniqueViolation(t *testing.T) {
	err := mapCreateErr(&pgconn.PgError{Code: "23505", Message: "duplicate key"})
	if got := connect.CodeOf(err); got != connect.CodeAlreadyExists {
		t.Errorf("code = %v, want AlreadyExists", got)
	}
}

func TestMapCreateErrNil(t *testing.T) {
	if mapCreateErr(nil) != nil {
		t.Error("nil in, nil out")
	}
}

func TestMapCreateErrOtherPgError(t *testing.T) {
	// A different SQLSTATE must not be mistaken for a duplicate.
	err := mapCreateErr(&pgconn.PgError{Code: "23503", Message: "fk violation"})
	if got := connect.CodeOf(err); got == connect.CodeAlreadyExists {
		t.Error("only 23505 may map to AlreadyExists")
	}
}

// Registered sentinels must route through the central mapper.
func TestMapCreateErrRoutesSentinels(t *testing.T) {
	if got := connect.CodeOf(mapCreateErr(ErrVersionMismatch)); got != connect.CodeAborted {
		t.Errorf("ErrVersionMismatch = %v, want Aborted", got)
	}
	if got := connect.CodeOf(mapCreateErr(ErrBackendDisabled)); got != connect.CodeFailedPrecondition {
		t.Errorf("ErrBackendDisabled = %v, want FailedPrecondition", got)
	}
}

// ─── construction + setters ────────────────────────────────────────────────

func TestNewHandlerStoresDependencies(t *testing.T) {
	cfg := PresignConfig{}
	h := NewHandler(nil, nil, nil, nil, nil, cfg)
	if h == nil {
		t.Fatal("NewHandler returned nil")
	}
	// The opt-in hooks start unset; nothing may assume they are wired.
	if h.versions != nil || h.quota != nil || h.events != nil {
		t.Error("optional hooks must start nil")
	}
}

func TestSetters(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil, PresignConfig{})

	v := &VersionHandler{}
	h.SetVersionHandler(v)
	if h.versions != v {
		t.Error("SetVersionHandler did not attach")
	}

	q := &fakeQuota{}
	h.SetQuotaUpdater(q)
	if h.quota == nil {
		t.Error("SetQuotaUpdater did not attach")
	}

	p := &fakeEvents{}
	h.SetEventProducer(p)
	if h.events == nil {
		t.Error("SetEventProducer did not attach")
	}
}

// SetLogger must ignore nil rather than blanking an already-wired logger —
// otherwise a later nil call would silence the handler.
func TestSetLoggerIgnoresNil(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil, PresignConfig{})

	real := zap.NewNop()
	h.SetLogger(real)
	if h.log != real {
		t.Fatal("SetLogger did not attach the logger")
	}
	h.SetLogger(nil)
	if h.log != real {
		t.Error("a nil logger must not clear the existing one")
	}
}

type fakeQuota struct{ calls int }

func (q *fakeQuota) OnObjectPromoted(context.Context, uuid.UUID, int64) error {
	q.calls++
	return nil
}

type fakeEvents struct {
	err  error
	evts []worker.Event
	tids []string
}

func (f *fakeEvents) Dispatch(_ context.Context, tenantID string, evt worker.Event) (int, error) {
	f.tids = append(f.tids, tenantID)
	f.evts = append(f.evts, evt)
	return 1, f.err
}

func (f *fakeEvents) DispatchTx(_ context.Context, _ pgx.Tx, tenantID string, evt worker.Event) (int, error) {
	f.tids = append(f.tids, tenantID)
	f.evts = append(f.evts, evt)
	return 1, f.err
}

// ─── dispatchEventTx ───────────────────────────────────────────────────────

// Events are opt-in: with no producer wired the fan-out must be a silent
// no-op, never a nil dereference on the transition path.
func TestDispatchEventTxNoProducerIsNoop(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil, PresignConfig{})

	if err := h.dispatchEventTx(context.Background(), nil, uuid.New(), "t", "r", nil); err != nil {
		t.Fatalf("want a no-op, got %v", err)
	}
}

func TestDispatchEventTxBuildsTheEvent(t *testing.T) {
	f := &fakeEvents{}
	h := NewHandler(nil, nil, nil, nil, nil, PresignConfig{})
	h.SetEventProducer(f)

	tenant := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "alice", TenantID: tenant,
	})
	payload := map[string]any{"size": 42}

	if err := h.dispatchEventTx(ctx, nil, tenant, "paladin.object.created", "tenants/x/objects/y", payload); err != nil {
		t.Fatalf("dispatchEventTx: %v", err)
	}
	if len(f.evts) != 1 {
		t.Fatalf("want 1 event, got %d", len(f.evts))
	}
	evt := f.evts[0]
	if evt.Type != "paladin.object.created" || evt.ResourceName != "tenants/x/objects/y" {
		t.Errorf("event = %+v", evt)
	}
	if evt.TenantID != tenant.String() || f.tids[0] != tenant.String() {
		t.Errorf("tenant not stamped: %q / %q", evt.TenantID, f.tids[0])
	}
	// The actor is what makes the audit trail attributable.
	if evt.ActorSubject != "alice" {
		t.Errorf("ActorSubject = %q, want alice", evt.ActorSubject)
	}
	if evt.Payload["size"] != 42 {
		t.Errorf("payload = %v", evt.Payload)
	}
	if evt.At.IsZero() {
		t.Error("event timestamp must be stamped")
	}
}

// A system-driven transition (reconciler, event consumer) has no principal;
// the event must still be emitted, just with an empty actor.
func TestDispatchEventTxWithoutPrincipal(t *testing.T) {
	f := &fakeEvents{}
	h := NewHandler(nil, nil, nil, nil, nil, PresignConfig{})
	h.SetEventProducer(f)

	if err := h.dispatchEventTx(context.Background(), nil, uuid.New(), "t", "r", nil); err != nil {
		t.Fatalf("dispatchEventTx: %v", err)
	}
	if len(f.evts) != 1 {
		t.Fatalf("want 1 event, got %d", len(f.evts))
	}
	if f.evts[0].ActorSubject != "" {
		t.Errorf("ActorSubject = %q, want empty for a system actor", f.evts[0].ActorSubject)
	}
}

// The whole point of the Tx variant: a fan-out failure must propagate so the
// caller rolls the state change back rather than committing a silent
// dual-write divergence.
func TestDispatchEventTxPropagatesError(t *testing.T) {
	boom := errors.New("outbox insert failed")
	h := NewHandler(nil, nil, nil, nil, nil, PresignConfig{})
	h.SetEventProducer(&fakeEvents{err: boom})

	if err := h.dispatchEventTx(context.Background(), nil, uuid.New(), "t", "r", nil); !errors.Is(err, boom) {
		t.Fatalf("want the dispatch error, got %v", err)
	}
}

// ─── objectResourceName ────────────────────────────────────────────────────

func TestObjectResourceName(t *testing.T) {
	tenant := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	got := objectResourceName(tenant, "logs", "a.txt")
	want := "tenants/" + tenant.String() + "/collections/logs/objects-by-key/a.txt"
	if got != want {
		t.Errorf("objectResourceName = %q, want %q", got, want)
	}
}

// testPresignTTL and testPresignMaxTTL are the lifetimes handler tests sign
// with: a one-hour default under a two-hour ceiling.
const (
	testPresignTTL    = time.Hour
	testPresignMaxTTL = 2 * time.Hour
	// testMaxObjectSize is the single-request upload cap tests run under.
	testMaxObjectSize = 5 << 30
)

// testChecksumValue is a well-formed SHA-256 checksum (of the empty body).
const testChecksumValue = "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="

// testUploadLimits are permissive global limits: tests that exercise a
// limit set it themselves.
var testUploadLimits = uploadpolicy.Limits{
	MaxObjectSize: testMaxObjectSize, MaxMultipartSize: 1 << 40,
	MinPartSize: uploadpolicy.S3MinPartSize, MaxPartSize: uploadpolicy.S3MaxPartSize,
	MaxParts: uploadpolicy.S3MaxParts,
}

func testPresignConfig() PresignConfig {
	p, err := presignttl.New(testPresignTTL, testPresignTTL, testPresignTTL, testPresignMaxTTL)
	if err != nil {
		panic(err)
	}
	return PresignConfig{TTL: p, Limits: testUploadLimits}
}
