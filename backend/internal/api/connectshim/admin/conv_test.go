package admin

import (
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// The admin shim is a pile of symmetric domain↔proto converters. Round-trip is
// the assertion that matters: a dropped field silently reverts an operator's
// setting on the next write-back, which no single-direction test would catch.

// ─── small helpers ─────────────────────────────────────────────────────────

// The all-zero UUID is how the DB spells "no value"; it must not reach the
// client as a literal 00000000-… which would read as a real id.
func TestUuidStrEmpty(t *testing.T) {
	if got := uuidStrEmpty("00000000-0000-0000-0000-000000000000"); got != "" {
		t.Errorf("the nil UUID must blank out, got %q", got)
	}
	if got := uuidStrEmpty("11111111-1111-1111-1111-111111111111"); got == "" {
		t.Error("a real UUID must survive")
	}
	if got := uuidStrEmpty(""); got != "" {
		t.Errorf("empty stays empty, got %q", got)
	}
}

// ─── enum mappings ─────────────────────────────────────────────────────────

func TestStorageKindRoundTrip(t *testing.T) {
	for _, kind := range []string{"aws-s3", "s3-compatible", "gcs"} {
		if got := storageKindFromProto(storageKindProto(kind)); got != kind {
			t.Errorf("round trip of %q gave %q", kind, got)
		}
	}
}

// An unknown kind must land on UNSPECIFIED / "" rather than silently becoming
// a real backend kind.
func TestStorageKindUnknown(t *testing.T) {
	if got := storageKindProto("azure-blob"); got != pb.StorageKind_STORAGE_KIND_UNSPECIFIED {
		t.Errorf("unknown kind = %v, want UNSPECIFIED", got)
	}
	if got := storageKindFromProto(pb.StorageKind_STORAGE_KIND_UNSPECIFIED); got != "" {
		t.Errorf("UNSPECIFIED = %q, want empty", got)
	}
	if got := storageKindFromProto(pb.StorageKind(99)); got != "" {
		t.Errorf("out-of-range = %q, want empty", got)
	}
}

// ─── SSE ───────────────────────────────────────────────────────────────────

func TestSSERoundTrip(t *testing.T) {
	for _, in := range []admindomain.ServerSideEncryption{
		{Type: "AES256"},
		{Type: "KMS", KeyID: "arn:aws:kms:key/1"},
		{}, // none
	} {
		got := sseFromProto(sseToProto(in))
		if got.Type != in.Type || got.KeyID != in.KeyID {
			t.Errorf("round trip of %+v gave %+v", in, got)
		}
	}
}

func TestSSEFromProtoNil(t *testing.T) {
	if got := sseFromProto(nil); got.Type != "" || got.KeyID != "" {
		t.Errorf("nil must yield the zero value, got %+v", got)
	}
}

// An unset SSE type must serialise as NONE, not as the zero enum with a
// different meaning.
func TestSSEEmptyTypeIsNone(t *testing.T) {
	if got := sseToProto(admindomain.ServerSideEncryption{}); got.GetType() != pb.SseType_SSE_TYPE_NONE {
		t.Errorf("empty SSE type = %v, want NONE", got.GetType())
	}
}

// ─── event source ──────────────────────────────────────────────────────────

func TestEventsRoundTrip(t *testing.T) {
	for _, in := range []admindomain.EventSourceConfig{
		{Enabled: true, Target: "sqs", QueueURL: "https://sqs/q", PollInterval: 20 * time.Second},
		{Target: "redis", QueueURL: "redis://x"},
		{}, // none
	} {
		got := eventsFromProto(eventsToProto(in))
		if got.Enabled != in.Enabled || got.Target != in.Target ||
			got.QueueURL != in.QueueURL || got.PollInterval != in.PollInterval {
			t.Errorf("round trip of %+v gave %+v", in, got)
		}
	}
}

func TestEventsFromProtoNil(t *testing.T) {
	if got := eventsFromProto(nil); got.Enabled || got.Target != "" {
		t.Errorf("nil must yield the zero value, got %+v", got)
	}
}

func TestEventsUnknownTargetIsNone(t *testing.T) {
	got := eventsToProto(admindomain.EventSourceConfig{Target: "kafka"})
	if got.GetTarget() != pb.EventTarget_EVENT_TARGET_NONE {
		t.Errorf("unknown target = %v, want NONE", got.GetTarget())
	}
}

// ─── StorageBackend ────────────────────────────────────────────────────────

func TestBackendToProtoNil(t *testing.T) {
	if backendToProto(nil) != nil {
		t.Error("nil in, nil out")
	}
}

func TestBackendFromProtoNil(t *testing.T) {
	if got := backendFromProto(nil); got.BackendID != "" {
		t.Errorf("nil must yield the zero value, got %+v", got)
	}
}

// Round-trips the operator-settable half of a backend. The status/audit fields
// are server-owned and deliberately not read back.
func TestBackendRoundTripsEditableFields(t *testing.T) {
	in := admindomain.StorageBackend{
		BackendID: "primary", DisplayName: "Primary", Kind: "s3-compatible",
		Provider: "garage", Endpoint: "http://garage:3900",
		PublicEndpoint: "https://s3.example.com", Region: "us-east-1",
		ForcePathStyle: true, CredentialsSecretRef: "secret/garage",
		SSE:         admindomain.ServerSideEncryption{Type: "KMS", KeyID: "k1"},
		Events:      admindomain.EventSourceConfig{Enabled: true, Target: "sqs", QueueURL: "q"},
		CedarPolicy: "permit(...);",
	}

	got := backendFromProto(backendToProto(&in))

	if got.BackendID != in.BackendID || got.DisplayName != in.DisplayName ||
		got.Kind != in.Kind || got.Provider != in.Provider {
		t.Errorf("identity fields = %+v", got)
	}
	// The endpoint split is the browser-vs-cluster distinction; collapsing the
	// two would hand browsers an unreachable host.
	if got.Endpoint != in.Endpoint || got.PublicEndpoint != in.PublicEndpoint {
		t.Errorf("endpoints = %q / %q", got.Endpoint, got.PublicEndpoint)
	}
	if got.ForcePathStyle != in.ForcePathStyle || got.Region != in.Region ||
		got.CredentialsSecretRef != in.CredentialsSecretRef || got.CedarPolicy != in.CedarPolicy {
		t.Errorf("config fields = %+v", got)
	}
	if got.SSE != in.SSE {
		t.Errorf("SSE = %+v, want %+v", got.SSE, in.SSE)
	}
	if got.Events.Target != in.Events.Target || got.Events.QueueURL != in.Events.QueueURL {
		t.Errorf("Events = %+v", got.Events)
	}
}

// The name is derived, not stored, so it must be built from the id.
func TestBackendToProtoBuildsResourceName(t *testing.T) {
	got := backendToProto(&admindomain.StorageBackend{BackendID: "primary"})
	if got.GetName() != "storageBackends/primary" {
		t.Errorf("Name = %q", got.GetName())
	}
}

// Operational flags are server-owned: they are surfaced outward but must not
// be read back from a client-supplied message.
func TestBackendFromProtoIgnoresServerOwnedFlags(t *testing.T) {
	got := backendFromProto(&pb.StorageBackend{
		BackendId: "primary", Enabled: true, ReadOnly: true, Maintenance: true,
		HealthStatus: "healthy", ResourceVersion: "9",
	})
	if got.Enabled || got.ReadOnly || got.Maintenance || got.HealthStatus != "" {
		t.Errorf("server-owned flags must not be read back: %+v", got)
	}
}

// ─── BucketConstraints ─────────────────────────────────────────────────────

func TestConstraintsRoundTrip(t *testing.T) {
	in := admindomain.BucketConstraints{
		MaxObjectSizeBytes: 1 << 30, MinPartSizeBytes: 5 << 20, MaxPartSizeBytes: 1 << 30,
		MaxParts: 10000, AllowedContentTypes: []string{"image/png", "application/pdf"},
		MaxPresignPutTTL: 15 * time.Minute, MaxPresignGetTTL: time.Hour,
		RequiredChecksumAlgorithm: "SHA256",
	}

	got := constraintsFromProto(constraintsToProto(in))

	if got.MaxObjectSizeBytes != in.MaxObjectSizeBytes || got.MinPartSizeBytes != in.MinPartSizeBytes ||
		got.MaxPartSizeBytes != in.MaxPartSizeBytes || got.MaxParts != in.MaxParts {
		t.Errorf("size limits = %+v", got)
	}
	if got.MaxPresignPutTTL != in.MaxPresignPutTTL || got.MaxPresignGetTTL != in.MaxPresignGetTTL {
		t.Errorf("TTLs = %v / %v", got.MaxPresignPutTTL, got.MaxPresignGetTTL)
	}
	if got.RequiredChecksumAlgorithm != in.RequiredChecksumAlgorithm {
		t.Errorf("checksum algo = %q", got.RequiredChecksumAlgorithm)
	}
	if len(got.AllowedContentTypes) != 2 {
		t.Errorf("AllowedContentTypes = %v", got.AllowedContentTypes)
	}
}

func TestConstraintsChecksumAlgorithms(t *testing.T) {
	for _, algo := range []string{"CRC32C", "SHA256", "MD5"} {
		got := constraintsFromProto(constraintsToProto(admindomain.BucketConstraints{
			RequiredChecksumAlgorithm: algo,
		}))
		if got.RequiredChecksumAlgorithm != algo {
			t.Errorf("round trip of %q gave %q", algo, got.RequiredChecksumAlgorithm)
		}
	}
	// No requirement must stay "no requirement", not default to an algorithm.
	got := constraintsFromProto(constraintsToProto(admindomain.BucketConstraints{}))
	if got.RequiredChecksumAlgorithm != "" {
		t.Errorf("unset algo = %q, want empty", got.RequiredChecksumAlgorithm)
	}
}

// A zero TTL means "no cap"; emitting a zero duration instead would clamp
// every presign to expire immediately.
func TestConstraintsOmitZeroTTLs(t *testing.T) {
	got := constraintsToProto(admindomain.BucketConstraints{})
	if got.GetMaxPresignPutTtl() != nil || got.GetMaxPresignGetTtl() != nil {
		t.Error("zero TTLs must be omitted, not sent as 0s")
	}
}

func TestConstraintsFromProtoNil(t *testing.T) {
	if got := constraintsFromProto(nil); got.MaxParts != 0 {
		t.Errorf("nil must yield the zero value, got %+v", got)
	}
}

// ─── resource-name parsers ─────────────────────────────────────────────────

func TestBackendIDFromName(t *testing.T) {
	got, err := backendIDFromName("storageBackends/primary")
	if err != nil || got != "primary" {
		t.Errorf("got %q, %v", got, err)
	}
	for label, n := range map[string]string{
		"empty":        "",
		"prefix only":  "storageBackends/",
		"wrong prefix": "backends/primary",
		"has child":    "storageBackends/primary/buckets/b",
		"no separator": "storageBackends",
	} {
		t.Run(label, func(t *testing.T) {
			if _, err := backendIDFromName(n); err == nil {
				t.Errorf("want an error for %q", n)
			}
		})
	}
}

func TestBucketNameParts(t *testing.T) {
	b, bk, err := bucketNameParts("storageBackends/primary/buckets/acme-logs")
	if err != nil || b != "primary" || bk != "acme-logs" {
		t.Errorf("got %q/%q, %v", b, bk, err)
	}
	for label, n := range map[string]string{
		"empty":        "",
		"too few":      "storageBackends/primary",
		"too many":     "storageBackends/primary/buckets/a/b",
		"wrong prefix": "backends/primary/buckets/a",
		"wrong child":  "storageBackends/primary/objects/a",
	} {
		t.Run(label, func(t *testing.T) {
			if _, _, err := bucketNameParts(n); err == nil {
				t.Errorf("want an error for %q", n)
			}
		})
	}
}

func TestSubscriptionIDFromName(t *testing.T) {
	// The tenant segment is returned now, not discarded — the handler needs
	// it to scope the connection before reading an RLS-isolated row.
	ref, got, err := subscriptionFromName("tenants/acme/eventSubscriptions/s1")
	if err != nil || got != "s1" {
		t.Errorf("got %q, %v", got, err)
	}
	if ref.Slug != "acme" {
		t.Errorf("tenant ref = %+v, want slug acme", ref)
	}
	for label, n := range map[string]string{
		"empty":        "",
		"too few":      "tenants/acme",
		"too many":     "tenants/acme/eventSubscriptions/s1/x",
		"wrong child":  "tenants/acme/buckets/s1",
		"wrong prefix": "orgs/acme/eventSubscriptions/s1",
	} {
		t.Run(label, func(t *testing.T) {
			if _, _, err := subscriptionFromName(n); err == nil {
				t.Errorf("want an error for %q", n)
			}
		})
	}
}

// ─── checksum enum (shared with the data plane) ────────────────────────────

func TestConstraintsChecksumUnknownEnum(t *testing.T) {
	got := constraintsFromProto(&pb.BucketConstraints{
		RequiredChecksumAlgorithm: commonpb.ChecksumAlgorithm(99),
	})
	if got.RequiredChecksumAlgorithm != "" {
		t.Errorf("an unknown algorithm must not map to a real one, got %q", got.RequiredChecksumAlgorithm)
	}
}

// ─── bucket sub-config conversions ──────────────────────────────────────────
//
// Four From/To pairs had no test at all: lock, versioning, replication and
// lifecycle. The generic AST check (connectshim/mapping_test.go) proves a shim
// READS every proto field, and says plainly what it cannot see — a value
// mishandled after extraction, put in the wrong slot or dropped in one
// direction. That is the half these cover, and the half that matters most
// here: every one of these pairs carries two same-typed neighbours a swap
// would not disturb the compiler.

func TestLockRoundTripsBothModes(t *testing.T) {
	// Object lock is compliance retention. GOVERNANCE can be bypassed by a
	// privileged caller; COMPLIANCE cannot, by anyone, until the retention
	// expires. Mapping one to the other is not a display bug — it either
	// makes deletable what a regulator was promised was not, or bricks data
	// nobody can remove.
	for _, mode := range []string{"GOVERNANCE", "COMPLIANCE"} {
		t.Run(mode, func(t *testing.T) {
			in := admindomain.ObjectLockConfig{
				Enabled: true, DefaultMode: mode, DefaultRetention: 72 * time.Hour,
			}
			got := lockFromProto(lockToProto(in))
			if got.DefaultMode != mode {
				t.Errorf("mode = %q, want %q", got.DefaultMode, mode)
			}
			if got.DefaultRetention != in.DefaultRetention {
				t.Errorf("retention = %v, want %v", got.DefaultRetention, in.DefaultRetention)
			}
			if !got.Enabled {
				t.Error("enabled was lost")
			}
		})
	}
}

func TestLockUnknownModeIsNoDefault(t *testing.T) {
	// An unrecognised mode must produce NO default rather than silently
	// picking one: guessing GOVERNANCE would weaken a bucket the operator
	// meant to lock, and guessing COMPLIANCE would make objects permanent.
	got := lockFromProto(&pb.ObjectLockConfig{Enabled: true})
	if got.DefaultMode != "" {
		t.Errorf("mode = %q, want empty for an unspecified mode", got.DefaultMode)
	}
}

func TestVersioningRoundTripsBothFlags(t *testing.T) {
	// Two adjacent booleans, and they mean opposite things to a delete:
	// Enabled keeps history, KeepDeletesForever decides whether a delete
	// marker is ever reclaimed. Swapped, a bucket asked to retain deletions
	// forever quietly stops versioning at all.
	in := admindomain.BucketVersioning{Enabled: true, KeepDeletesForever: false}
	got := versioningFromProto(versioningToProto(in))
	if got.Enabled != in.Enabled || got.KeepDeletesForever != in.KeepDeletesForever {
		t.Errorf("got %+v, want %+v", got, in)
	}

	flipped := admindomain.BucketVersioning{Enabled: false, KeepDeletesForever: true}
	if got := versioningFromProto(versioningToProto(flipped)); got != flipped {
		t.Errorf("got %+v, want %+v", got, flipped)
	}
}

func TestReplicationRoundTripsDestinationAndFilter(t *testing.T) {
	// Two strings side by side. A swap would send every object to a bucket
	// named after a CEL expression — and, worse, replicate everything,
	// because the filter would then be a bucket name that matches nothing
	// and is treated as no filter.
	in := admindomain.BucketReplication{
		Enabled:           true,
		DestinationBucket: "storageBackends/dr/buckets/mirror",
		Filter:            `key.startsWith("critical/")`,
	}
	got := replicationFromProto(replicationToProto(in))
	if got != in {
		t.Errorf("got %+v, want %+v", got, in)
	}
}

// The proto models the action as a ONEOF, so a rule is a transition or an
// expiration and never both. The domain type has two nilable pointers and can
// therefore express something the wire cannot — which is worth pinning rather
// than discovering: writing this test with both arms set is what surfaced the
// asymmetry, and the answer is the data model, not a lost field.
func TestLifecycleRoundTripsATransition(t *testing.T) {
	// Both arms carry an `After` duration, so they are the easiest pair in
	// this file to cross. A transition read as an expiration deletes what was
	// meant to be moved to cold storage.
	in := []admindomain.LifecycleRule{{
		ID: "to-cold", Enabled: true, Match: `key.startsWith("logs/")`,
		Transition: &admindomain.LifecycleTransition{
			After: 720 * time.Hour, StorageClass: "GLACIER",
		},
	}}

	got := lifecycleFromProto(lifecycleToProto(in))
	if len(got) != 1 {
		t.Fatalf("rules = %d, want 1", len(got))
	}
	r := got[0]
	if r.ID != in[0].ID || r.Match != in[0].Match || !r.Enabled {
		t.Errorf("identity = %+v", r)
	}
	if r.Transition == nil {
		t.Fatal("the transition arm was lost")
	}
	if r.Transition.After != in[0].Transition.After {
		t.Errorf("after = %v, want %v", r.Transition.After, in[0].Transition.After)
	}
	if r.Transition.StorageClass != in[0].Transition.StorageClass {
		t.Errorf("storage class = %q", r.Transition.StorageClass)
	}
	if r.Expiration != nil {
		t.Errorf("a transition rule gained an expiration: %+v", r.Expiration)
	}
}

func TestLifecycleRoundTripsAnExpiration(t *testing.T) {
	in := []admindomain.LifecycleRule{{
		ID: "expire", Enabled: true, Match: `key.startsWith("tmp/")`,
		Expiration: &admindomain.LifecycleExpiration{After: 24 * time.Hour},
	}}

	got := lifecycleFromProto(lifecycleToProto(in))
	if got[0].Expiration == nil {
		t.Fatal("the expiration arm was lost")
	}
	if got[0].Expiration.After != in[0].Expiration.After {
		t.Errorf("after = %v, want %v", got[0].Expiration.After, in[0].Expiration.After)
	}
	if got[0].Transition != nil {
		t.Errorf("an expiration rule gained a transition: %+v", got[0].Transition)
	}
}

// A domain rule carrying BOTH arms cannot be sent whole — the oneof holds one.
// The conversion resolves it by preferring the transition, and the expiration
// is dropped SILENTLY. Pinned because it is the kind of quiet narrowing that
// a caller assembling rules from another source would never see: the rule they
// stored is not the rule they wrote.
func TestLifecycleKeepsTransitionWhenBothArmsAreSet(t *testing.T) {
	got := lifecycleFromProto(lifecycleToProto([]admindomain.LifecycleRule{{
		ID: "both", Enabled: true,
		Transition: &admindomain.LifecycleTransition{After: time.Hour, StorageClass: "GLACIER"},
		Expiration: &admindomain.LifecycleExpiration{After: 2 * time.Hour},
	}}))
	if got[0].Transition == nil {
		t.Fatal("transition lost")
	}
	if got[0].Expiration != nil {
		t.Errorf("expiration = %+v; the oneof cannot carry both, so this must be dropped, not half-kept", got[0].Expiration)
	}
}

func TestLifecycleOmitsAbsentArms(t *testing.T) {
	// A rule with no transition must not gain an empty one: a zero-duration
	// transition is a rule that fires immediately.
	got := lifecycleFromProto(lifecycleToProto([]admindomain.LifecycleRule{
		{ID: "expire-only", Enabled: true, Expiration: &admindomain.LifecycleExpiration{After: time.Hour}},
	}))
	if got[0].Transition != nil {
		t.Errorf("transition = %+v, want nil", got[0].Transition)
	}
}
