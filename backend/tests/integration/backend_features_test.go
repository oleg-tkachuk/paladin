//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// A feature probe's results round-trip through the app role's pool, replace
// the previous probe's wholesale, reach both Get and List, and leave the
// backend's resource_version alone (ADR-0026).
func TestBackendFeatures_RecordAndSurface(t *testing.T) {
	h := pgharness.Setup(t)
	seedBackend(t, h.PoolMigrate, "features-be")
	seedBackend(t, h.PoolMigrate, "features-other")
	be := adapters.NewBackendRepoV2(sqlc.New(h.PoolApp), h.PoolApp)
	ctx := context.Background()

	got, err := be.Get(ctx, "features-be")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Features) != 0 {
		t.Errorf("never probed: %+v, want nothing recorded", got.Features)
	}
	rvBefore := got.ResourceVersion

	at := time.Now().UTC().Truncate(time.Microsecond)
	first := []features.Result{
		{Feature: features.ConditionalPut, Support: features.Supported, CheckedAt: at},
		{Feature: features.AnonymousReadPolicy, Support: features.Unsupported, Message: "no policies", CheckedAt: at},
	}
	if err := be.SetFeatures(ctx, "features-be", first); err != nil {
		t.Fatalf("set features: %v", err)
	}
	got, _ = be.Get(ctx, "features-be")
	if len(got.Features) != len(first) {
		t.Fatalf("recorded %+v, want %+v", got.Features, first)
	}
	byFeature := map[features.Feature]features.Result{}
	for _, r := range got.Features {
		byFeature[r.Feature] = r
	}
	if r := byFeature[features.AnonymousReadPolicy]; r.Support != features.Unsupported ||
		r.Message != "no policies" || !r.CheckedAt.Equal(at) {
		t.Errorf("anonymous_read_policy = %+v", r)
	}
	if got.ResourceVersion != rvBefore {
		t.Errorf("resource_version moved on a probe write: %d → %d", rvBefore, got.ResourceVersion)
	}

	second := []features.Result{{Feature: features.BucketCreate, Support: features.Unknown, CheckedAt: at}}
	if err := be.SetFeatures(ctx, "features-be", second); err != nil {
		t.Fatalf("set features again: %v", err)
	}
	got, _ = be.Get(ctx, "features-be")
	if len(got.Features) != 1 || got.Features[0].Feature != features.BucketCreate {
		t.Errorf("after a second probe: %+v, want only its result", got.Features)
	}

	listed, _, err := be.List(ctx, 0, "", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, b := range listed {
		switch b.BackendID {
		case "features-be":
			if len(b.Features) != 1 {
				t.Errorf("listed features-be with %+v", b.Features)
			}
		case "features-other":
			if len(b.Features) != 0 {
				t.Errorf("features-other carries %+v, recorded for another backend", b.Features)
			}
		}
	}
}
