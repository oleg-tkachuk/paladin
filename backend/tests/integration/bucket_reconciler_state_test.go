//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// stateTestBackend hosts the buckets these tests move between states.
const stateTestBackend = "be-provision-state"

// The reconciler lists a bucket, calls the backend, then records the outcome.
// The record used to match the bucket by name alone, so an outcome landed on
// whatever the row had become meanwhile: a deletion requested while creation
// was in flight was turned back into "ready", and lost.
func TestBucketProvisionWritesApplyOnlyToTheListedState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := pgharness.Setup(t)
	repo := adapters.NewBucketRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	seedBackend(t, h.PoolMigrate, stateTestBackend)

	bucketIn := func(name, state string) {
		t.Helper()
		if _, err := h.PoolMigrate.Exec(ctx, `
			INSERT INTO buckets (backend_id, name, provision_state)
			SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`,
			stateTestBackend, name, state); err != nil {
			t.Fatalf("seed bucket %s: %v", name, err)
		}
	}
	stateOf := func(name string) string {
		t.Helper()
		b, err := repo.Get(ctx, stateTestBackend, name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		return b.ProvisionState
	}
	refused := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, admindomain.ErrNotFound) {
			t.Errorf("%s returned %v, want ErrNotFound", what, err)
		}
	}

	// Marked for deletion while its creation was in flight.
	bucketIn("deleted-mid-create", "pending")
	if err := repo.MarkDeleting(ctx, stateTestBackend, "deleted-mid-create", 0); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	refused("MarkProvisionReady", repo.MarkProvisionReady(ctx, stateTestBackend, "deleted-mid-create"))
	refused("MarkProvisionFailed", repo.MarkProvisionFailed(ctx, stateTestBackend, "deleted-mid-create", false, "slow"))
	if got := stateOf("deleted-mid-create"); got != admindomain.BucketProvisionStateDeleting {
		t.Errorf("a late provisioning outcome undid the deletion: state = %s", got)
	}

	// Created again under the name of a bucket whose deletion was listed.
	bucketIn("created-again", "pending")
	refused("MarkDeletionFailed", repo.MarkDeletionFailed(ctx, stateTestBackend, "created-again", true, "not empty"))
	if got := stateOf("created-again"); got != "pending" {
		t.Errorf("a late deletion outcome landed on a new bucket: state = %s", got)
	}

	// The listed state still takes its outcome.
	if err := repo.MarkProvisionReady(ctx, stateTestBackend, "created-again"); err != nil {
		t.Fatalf("mark ready on a pending bucket: %v", err)
	}
	if got := stateOf("created-again"); got != "ready" {
		t.Errorf("state = %s, want ready", got)
	}
}
