package adapters

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// actorFromContext fills the `set_by` column recording who bound a tenant to
// its default bucket. Its previous implementation read a context key nothing
// writes and asserted to a method *Principal does not have, so it returned ""
// for every caller — and "" is also its legitimate answer for the bootstrap
// paths, which is why nothing ever looked wrong.
func TestActorFromContext(t *testing.T) {
	if got := actorFromContext(context.Background()); got != "" {
		t.Errorf("no principal: got %q, want \"\"", got)
	}
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "admin@local"})
	if got := actorFromContext(ctx); got != "admin@local" {
		t.Errorf("with principal: got %q, want the subject", got)
	}
}

// splitBucketResourceName is the only thing between a malformed reference and
// a binding pointed at nothing. Every conjunct in its guard rejects a distinct
// malformation, and the doc is explicit that the alternative to rejecting is
// binding silently.
func TestSplitBucketResourceName(t *testing.T) {
	backend, bucket, err := splitBucketResourceName("storageBackends/primary/buckets/photos")
	if err != nil {
		t.Fatalf("well-formed name: %v", err)
	}
	if backend != "primary" || bucket != "photos" {
		t.Errorf("split = (%q, %q), want (primary, photos)", backend, bucket)
	}

	// One case per conjunct, so dropping any half of the guard leaves a
	// malformation that gets through.
	bad := map[string]string{
		"empty":                   "",
		"too few segments":        "storageBackends/primary/buckets",
		"too many segments":       "storageBackends/primary/buckets/photos/extra",
		"wrong first collection":  "backends/primary/buckets/photos",
		"wrong second collection": "storageBackends/primary/bucket/photos",
		"empty backend":           "storageBackends//buckets/photos",
		"empty bucket":            "storageBackends/primary/buckets/",
		"bare name":               "photos",
	}
	for name, in := range bad {
		t.Run(name, func(t *testing.T) {
			backend, bucket, err := splitBucketResourceName(in)
			if !errors.Is(err, tenant.ErrDefaultBindingBucketMissing) {
				t.Fatalf("splitBucketResourceName(%q) err = %v, want ErrDefaultBindingBucketMissing", in, err)
			}
			if backend != "" || bucket != "" {
				t.Errorf("rejected name still yielded (%q, %q), want empty", backend, bucket)
			}
		})
	}
}

// defaultBucketName composes the reference the API serves back. Both halves
// come from a LEFT JOIN, so either can be absent, and half a name —
// "storageBackends//buckets/photos" — is worse than none: it parses as
// well-formed to the eye and resolves to nothing.
func TestDefaultBucketName(t *testing.T) {
	if got := defaultBucketName("primary", "photos"); got != "storageBackends/primary/buckets/photos" {
		t.Errorf("bound tenant: got %q", got)
	}
	for name, in := range map[string][2]string{
		"neither":    {"", ""},
		"no backend": {"", "photos"},
		"no bucket":  {"primary", ""},
	} {
		if got := defaultBucketName(in[0], in[1]); got != "" {
			t.Errorf("%s: got %q, want \"\"", name, got)
		}
	}
}

// A tenant carries two adjacent []byte columns and three adjacent timestamps,
// the last of which decides whether the tenant is deleted. Reading created_at
// as deleted_at would make every tenant that ever existed look soft-deleted.
func TestTenantFromSQLC(t *testing.T) {
	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	deleted := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	got := tenantFromSQLC(sqlc.Tenant{
		ID:                   pgUUID(id),
		Slug:                 "acme",
		DisplayName:          "Acme Corp",
		Labels:               []byte(`{"tier":"gold"}`),
		InheritedCedarPolicy: "permit(principal, action, resource);",
		InheritedPolicyHash:  []byte("policy-hash"),
		ResourceVersion:      7,
		CreatedAt:            pgTS(created),
		UpdatedAt:            pgTS(updated),
		DeletedAt:            pgTS(deleted),
		StorageLayout:        "dedicated",
	})

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"TenantID", got.TenantID, id},
		{"Slug", got.Slug, "acme"},
		{"DisplayName", got.DisplayName, "Acme Corp"},
		{"InheritedCedarPolicy", got.InheritedCedarPolicy, "permit(principal, action, resource);"},
		{"ResourceVersion", got.ResourceVersion, int64(7)},
		{"StorageLayout", got.StorageLayout, "dedicated"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
	if string(got.Labels) != `{"tier":"gold"}` {
		t.Errorf("Labels = %s", got.Labels)
	}
	if string(got.InheritedPolicyHash) != "policy-hash" {
		t.Errorf("InheritedPolicyHash = %s", got.InheritedPolicyHash)
	}
	for _, ts := range []struct {
		field string
		got   time.Time
		want  time.Time
	}{
		{"CreatedAt", got.CreatedAt, created},
		{"UpdatedAt", got.UpdatedAt, updated},
		{"DeletedAt", got.DeletedAt, deleted},
	} {
		if !ts.got.Equal(ts.want) {
			t.Errorf("%s = %v, want %v", ts.field, ts.got, ts.want)
		}
	}

	// A live tenant has a NULL deleted_at, and the zero time is how every
	// caller in this package asks "is it deleted".
	if live := tenantFromSQLC(sqlc.Tenant{Slug: "live"}); !live.DeletedAt.IsZero() {
		t.Errorf("DeletedAt on a live tenant = %v, want zero", live.DeletedAt)
	}
}

// The binding mapper takes the backend and bucket names as two adjacent
// string parameters, resolved by a JOIN the row itself does not carry.
func TestDefaultBindingFromSQLC(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	bucketID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	setAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	got := defaultBindingFromSQLC(sqlc.TenantDefaultBinding{
		TenantID: pgUUID(tenantID),
		BucketID: pgUUID(bucketID),
		SetAt:    pgTS(setAt),
		SetBy:    "admin@local",
	}, "backend-name", "bucket-name")

	if got.TenantID != tenantID || got.BucketID != bucketID {
		t.Errorf("ids = (%v, %v), want (%v, %v)", got.TenantID, got.BucketID, tenantID, bucketID)
	}
	if got.BackendName != "backend-name" || got.BucketName != "bucket-name" {
		t.Errorf("names = (%q, %q), want (backend-name, bucket-name)", got.BackendName, got.BucketName)
	}
	if got.SetBy != "admin@local" {
		t.Errorf("SetBy = %q, want admin@local", got.SetBy)
	}
	if !got.SetAt.Equal(setAt) {
		t.Errorf("SetAt = %v, want %v", got.SetAt, setAt)
	}
}
