package admin

import (
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin-private/internal/api/admin/v1/admindomain"
	pb "github.com/oleg-tkachuk/paladin-private/internal/api/pb/admin/v1"
	commonpb "github.com/oleg-tkachuk/paladin-private/internal/api/pb/common/v1"
)

// The admin shim is a pile of symmetric domain↔proto converters. Round-trip is
// the assertion that matters: a dropped field silently reverts an operator's
// setting on the next write-back, which no single-direction test would catch.

// ─── small helpers ─────────────────────────────────────────────────────────

func TestTsHelpers(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	if tsProto(time.Time{}) != nil {
		t.Error("a zero time must project as nil, not epoch 0")
	}
	if got := tsProto(now); got == nil || !got.AsTime().Equal(now) {
		t.Errorf("tsProto = %v", got)
	}
	if tsPtrProto(nil) != nil {
		t.Error("nil must project as nil")
	}
	zero := time.Time{}
	if tsPtrProto(&zero) != nil {
		t.Error("a pointer to a zero time must project as nil")
	}
	if got := tsPtrProto(&now); got == nil || !got.AsTime().Equal(now) {
		t.Errorf("tsPtrProto = %v", got)
	}
}

func TestResourceVersionAndParseRV(t *testing.T) {
	// 0 means "unset" and must travel as empty so clients do not echo "0".
	if got := resourceVersion(0); got != "" {
		t.Errorf("resourceVersion(0) = %q, want empty", got)
	}
	if got := resourceVersion(12); got != "12" {
		t.Errorf("resourceVersion(12) = %q", got)
	}

	if got, err := parseRV(""); err != nil || got != 0 {
		t.Errorf("parseRV(\"\") = %d, %v", got, err)
	}
	if got, err := parseRV("12"); err != nil || got != 12 {
		t.Errorf("parseRV round trip = %d, %v", got, err)
	}
	if _, err := parseRV("nope"); err == nil {
		t.Error("parseRV must reject garbage")
	}
}

func TestPageResponseProto(t *testing.T) {
	if pageResponseProto("") != nil {
		t.Error("no next token must omit the page response")
	}
	if got := pageResponseProto("tok"); got == nil || got.NextPageToken != "tok" {
		t.Errorf("pageResponseProto = %v", got)
	}
}

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

// tenantIDFromName tolerates a child segment because callers pass both the
// bare tenant name and deeper resource names.
func TestTenantIDFromName(t *testing.T) {
	if got, err := tenantIDFromName("tenants/acme"); err != nil || got != "acme" {
		t.Errorf("got %q, %v", got, err)
	}
	if got, err := tenantIDFromName("tenants/acme/eventSubscriptions/s1"); err != nil || got != "acme" {
		t.Errorf("child segment must be trimmed, got %q, %v", got, err)
	}
	for label, n := range map[string]string{
		"empty":        "",
		"prefix only":  "tenants/",
		"wrong prefix": "orgs/acme",
	} {
		t.Run(label, func(t *testing.T) {
			if _, err := tenantIDFromName(n); err == nil {
				t.Errorf("want an error for %q", n)
			}
		})
	}
}

func TestSubscriptionIDFromName(t *testing.T) {
	got, err := subscriptionIDFromName("tenants/acme/eventSubscriptions/s1")
	if err != nil || got != "s1" {
		t.Errorf("got %q, %v", got, err)
	}
	for label, n := range map[string]string{
		"empty":        "",
		"too few":      "tenants/acme",
		"too many":     "tenants/acme/eventSubscriptions/s1/x",
		"wrong child":  "tenants/acme/buckets/s1",
		"wrong prefix": "orgs/acme/eventSubscriptions/s1",
	} {
		t.Run(label, func(t *testing.T) {
			if _, err := subscriptionIDFromName(n); err == nil {
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
