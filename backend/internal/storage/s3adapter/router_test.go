package s3adapter

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/object"
)

// TestObjectRouter_UnknownBackendPropagates proves the router routes on the
// backend id carried by the call and surfaces an unknown-backend error without
// touching any client — a wrong id never silently falls through to the default
// store. (testRegistry lives in registry_test.go.)
func TestObjectRouter_UnknownBackendPropagates(t *testing.T) {
	reg, builds := testRegistry()
	rt := NewObjectRouter(reg)
	ctx := context.Background()

	if _, _, _, _, err := rt.Head(ctx, "nope", "bkt", uuid.New(), "ok", "k"); err == nil {
		t.Fatal("Head to unknown backend: want error")
	}
	if err := rt.DeleteObject(ctx, "nope", "bkt", uuid.New(), "ok", "k"); err == nil {
		t.Fatal("DeleteObject to unknown backend: want error")
	}
	if _, _, _, err := rt.PresignGet(ctx, object.PresignGetArgs{BackendID: "nope"}); err == nil {
		t.Fatal("PresignGet to unknown backend: want error")
	}
	if n := atomic.LoadInt32(builds); n != 0 {
		t.Fatalf("no client should be built for an unknown backend, got %d", n)
	}
}

// Cross-backend CopyObject used to be rejected up front; ADR-0011 Phase 3
// slice 3 turned it into a GET→PUT stream-through (see ObjectRouter.streamThrough).
// The byte transfer needs a real/mock S3 on both ends, so it is covered by the
// migration integration test rather than a unit test here.

// TestProvisionerRouter_UnknownBackendPropagates mirrors the object router for
// the provisioner path (its backend id is an explicit parameter).
func TestProvisionerRouter_UnknownBackendPropagates(t *testing.T) {
	reg, _ := testRegistry()
	rt := NewProvisionerRouter(reg)
	ctx := context.Background()

	if err := rt.CreateBucket(ctx, "nope", "bkt", "us-east-1"); err == nil {
		t.Fatal("CreateBucket to unknown backend: want error")
	}
	if err := rt.DeleteBucket(ctx, "nope", "bkt"); err == nil {
		t.Fatal("DeleteBucket to unknown backend: want error")
	}
}
