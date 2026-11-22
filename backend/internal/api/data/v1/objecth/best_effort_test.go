package objecth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// errBucketStore is the store failing under a best-effort bucket read.
var errBucketStore = errors.New("bucket store unreachable")

// unresolvedRepo serves the object every RPC asks for and fails every bucket
// lookup, as a store outage would.
type unresolvedRepo struct {
	fakeObjectRepo
}

func (*unresolvedRepo) FindByName(_ context.Context, tenantID uuid.UUID, collection, _ string) (Object, error) {
	return Object{ObjectID: uuid.NewV7(), TenantID: tenantID, Collection: collection, Key: "k", State: statemachine.StateAvailable}, nil
}

func (r *unresolvedRepo) FindByPath(ctx context.Context, tenantID uuid.UUID, collection, key string) (Object, error) {
	return r.FindByName(ctx, tenantID, collection, key)
}

func (*unresolvedRepo) LookupBucket(context.Context, uuid.UUID, string, bucketgate.Access) (string, string, error) {
	return "", "", errBucketStore
}

// warnedOnce asserts logs hold exactly one best-effort warning for op, naming
// the tenant, the collection and the error.
func warnedOnce(t *testing.T, logs *observer.ObservedLogs, op string, tenantID uuid.UUID, collection string) {
	t.Helper()
	var found []observer.LoggedEntry
	for _, e := range logs.All() {
		if e.Message == msgBestEffortFailed {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("best-effort warnings = %d, want 1; logs %v", len(found), logs.All())
	}
	e := found[0]
	f := e.ContextMap()
	if e.Level != zapcore.WarnLevel || f["op"] != op || f["tenant_id"] != tenantID.String() ||
		f[fieldCollection] != collection || f["error"] != errBucketStore.Error() {
		t.Errorf("warning = %v %v, want warn naming op %q, the tenant, %q and the error", e.Level, f, op, collection)
	}
}

// The reads that resolve a collection's bucket only to scope their
// authorization go on without it when the lookup fails, and a scoped token is
// then denied. That denial used to come with no line saying the lookup
// failed; each now warns once. Writes do not go on: see write_gate_test.go.
func TestBestEffortBucketLookupsWarnWhenTheStoreFails(t *testing.T) {
	tenantID := uuid.New()
	const collection = "docs"
	// denyAll stops each RPC at its authorization: the scoped token denied for
	// want of the bucket the lookup failed to resolve.
	newHandler := func(l *zap.Logger) *Handler {
		h := &Handler{repo: &unresolvedRepo{}, policy: denyAll{}}
		h.SetLogger(l)
		return h
	}
	for _, tc := range []struct {
		op   string
		call func(ctx context.Context, h *Handler)
	}{
		{opListFolders, func(ctx context.Context, h *Handler) {
			_, _, _ = h.ListCommonPrefixes(ctx, ListObjectsInput{Collection: collection, Delimiter: "/"})
		}},
		{opList, func(ctx context.Context, h *Handler) {
			_, _, _ = h.ListObjects(ctx, ListObjectsInput{Collection: collection})
		}},
		{opCount, func(ctx context.Context, h *Handler) {
			_, _ = h.CountObjects(ctx, CountObjectsInput{Collection: collection})
		}},
		{opCountFacets, func(ctx context.Context, h *Handler) {
			_, _ = h.CountObjectFacets(ctx, ListObjectsInput{Collection: collection})
		}},
		{opListTags, func(ctx context.Context, h *Handler) { _, _ = h.ListDistinctTags(ctx, collection, "", 0) }},
		{opGet, func(ctx context.Context, h *Handler) { _, _ = h.GetObject(ctx, collection, uuid.New().String()) }},
		{opLookup, func(ctx context.Context, h *Handler) { _, _ = h.LookupObject(ctx, collection, "k") }},
	} {
		t.Run(tc.op, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
			tc.call(ctx, newHandler(zap.New(core)))
			warnedOnce(t, logs, tc.op, tenantID, collection)
		})
	}
}

// A write whose bucket binding cannot be read fails, and the RPC's outcome
// line says why at Error: the store failure is the server's, not the
// caller's.
func TestWriteBindingFailureIsLoggedAtError(t *testing.T) {
	tenantID := uuid.New()
	const collection = "docs"
	for _, tc := range []struct {
		op   string
		call func(ctx context.Context, h *Handler) error
	}{
		{opComplete, func(ctx context.Context, h *Handler) error {
			_, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: collection, ObjectID: uuid.New().String()})
			return err
		}},
		{opDelete, func(ctx context.Context, h *Handler) error {
			return h.DeleteObject(ctx, collection, uuid.New().String(), "1", false, false)
		}},
		{opRestore, func(ctx context.Context, h *Handler) error {
			_, err := h.RestoreObject(ctx, collection, uuid.New().String(), "1")
			return err
		}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			h := &Handler{repo: &unresolvedRepo{}, policy: denyAll{}}
			h.SetLogger(zap.New(core))
			ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
			if err := tc.call(ctx, h); err == nil {
				t.Fatal("a write with an unreadable binding succeeded")
			}
			var errs []observer.LoggedEntry
			for _, e := range logs.All() {
				if e.Level == zapcore.ErrorLevel {
					errs = append(errs, e)
				}
			}
			if len(errs) != 1 {
				t.Fatalf("error lines = %d, want 1; logs %v", len(errs), logs.All())
			}
			if f := errs[0].ContextMap(); f["op"] != tc.op || !strings.Contains(fmt.Sprint(f["error"]), errBucketStore.Error()) {
				t.Errorf("error line = %v, want op %q and the store's error", f, tc.op)
			}
		})
	}
}
