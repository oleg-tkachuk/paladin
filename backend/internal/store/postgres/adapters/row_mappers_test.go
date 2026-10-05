package adapters

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// Row mappers are long struct literals over same-typed fields, which is the
// one shape where a transposition survives the compiler, the linter and a
// reading. Each fixture below gives every field of a given type a value no
// other field of that type has, so a swapped pair changes an assertion.

// quotaFromSQLC maps eight adjacent int64 columns: four limits and four
// usages. Enforcement compares one against the other, so a transposition
// does not fail — it changes what the system allows. Distinct powers of two
// make any pairing visible.
func TestQuotaFromSQLC(t *testing.T) {
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	tenant := uuid.MustParse("7ba7b810-9dad-11d1-80b4-00c04fd430c8")
	reset := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	updated := time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC)

	got := quotaFromSQLC(sqlc.Quota{
		ID:                pgUUID(id),
		TenantID:          pgUUID(tenant),
		MaxTotalBytes:     1 << 10,
		MaxObjectCount:    1 << 11,
		MaxBytesPerDay:    1 << 12,
		MaxObjectsPerDay:  1 << 13,
		UsageTotalBytes:   1 << 14,
		UsageObjectCount:  1 << 15,
		UsageBytesToday:   1 << 16,
		UsageObjectsToday: 1 << 17,
		LastResetAt:       pgTS(reset),
		ResourceVersion:   11,
		UpdatedAt:         pgTS(updated),
	}, "backend-name", "bucket-name")

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"QuotaID", got.QuotaID, id},
		{"TenantID", got.TenantID, tenant},
		{"BackendID", got.BackendID, "backend-name"},
		{"BucketName", got.BucketName, "bucket-name"},
		{"MaxTotalBytes", got.MaxTotalBytes, int64(1 << 10)},
		{"MaxObjectCount", got.MaxObjectCount, int64(1 << 11)},
		{"MaxBytesPerDay", got.MaxBytesPerDay, int64(1 << 12)},
		{"MaxObjectsPerDay", got.MaxObjectsPerDay, int64(1 << 13)},
		{"UsageTotalBytes", got.UsageTotalBytes, int64(1 << 14)},
		{"UsageObjectCount", got.UsageObjectCount, int64(1 << 15)},
		{"UsageBytesToday", got.UsageBytesToday, int64(1 << 16)},
		{"UsageObjectsToday", got.UsageObjectsToday, int64(1 << 17)},
		{"ResourceVersion", got.ResourceVersion, int64(11)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
	if got.LastResetAt == nil || !got.LastResetAt.Equal(reset) {
		t.Errorf("LastResetAt = %v, want %v", got.LastResetAt, reset)
	}
	if !got.UpdatedAt.Equal(updated) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, updated)
	}

	// A quota that has never been reset has no reset instant; the zero time
	// would read as "reset at the beginning of the epoch", which is a date,
	// not an absence.
	if got := quotaFromSQLC(sqlc.Quota{}, "b", "n"); got.LastResetAt != nil {
		t.Errorf("LastResetAt on an unreset quota = %v, want nil", got.LastResetAt)
	}
}

// versionFromSQLC carries three *string columns through derefStr and two
// JSONB columns through decodeMap. A version whose tags are served as its
// metadata is wrong on every read of that object.
func TestVersionFromSQLC(t *testing.T) {
	versionID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	objectID := uuid.MustParse("7ba7b810-9dad-11d1-80b4-00c04fd430c8")
	created := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	retain := time.Date(2027, 2, 3, 4, 5, 6, 0, time.UTC)

	etag, checksum, contentType := "etag-value", "checksum-value", "text/csv"
	size := int64(4096)

	got := versionFromSQLC(sqlc.ObjectVersion{
		ID:                pgUUID(versionID),
		ObjectID:          pgUUID(objectID),
		IsDeleteMarker:    true,
		StoragePath:       "tenant/coll/key",
		SizeBytes:         &size,
		Etag:              &etag,
		ChecksumAlgorithm: checksumAlgoInt("SHA256"),
		Checksum:          &checksum,
		ContentType:       &contentType,
		Metadata:          []byte(`{"origin":"metadata"}`),
		Tags:              []byte(`{"origin":"tags"}`),
		CreatedAt:         pgTS(created),
	}, "COMPLIANCE", pgTS(retain), true)

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"VersionID", got.VersionID, versionID},
		{"ObjectID", got.ObjectID, objectID},
		{"IsDeleteMarker", got.IsDeleteMarker, true},
		{"StoragePath", got.StoragePath, "tenant/coll/key"},
		{"SizeBytes", got.SizeBytes, int64(4096)},
		{"ETag", got.ETag, "etag-value"},
		{"ChecksumAlgo", got.ChecksumAlgo, "SHA256"},
		{"Checksum", got.Checksum, "checksum-value"},
		{"ContentType", got.ContentType, "text/csv"},
		{"LockMode", got.LockMode, "COMPLIANCE"},
		{"LegalHold", got.LegalHold, true},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
	if got.Metadata["origin"] != "metadata" {
		t.Errorf("Metadata = %v, want origin=metadata", got.Metadata)
	}
	if got.Tags["origin"] != "tags" {
		t.Errorf("Tags = %v, want origin=tags", got.Tags)
	}
	if got.LockRetainUntil == nil || !got.LockRetainUntil.Equal(retain) {
		t.Errorf("LockRetainUntil = %v, want %v", got.LockRetainUntil, retain)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}

	// A NULL size is 0 rather than a panic, and a version with no retention
	// has none — a zero instant would read as retention that has expired.
	bare := versionFromSQLC(sqlc.ObjectVersion{}, "", pgtype.Timestamptz{}, false)
	if bare.SizeBytes != 0 {
		t.Errorf("SizeBytes on a NULL column = %d, want 0", bare.SizeBytes)
	}
	if bare.LockRetainUntil != nil {
		t.Errorf("LockRetainUntil with no retention = %v, want nil", bare.LockRetainUntil)
	}
}

// collectionFromSQLC takes the backend and bucket names as two adjacent
// string parameters. Swapped, a collection reports a binding that resolves
// to nothing — and the caller has no second source to check it against.
func TestCollectionFromSQLC(t *testing.T) {
	tenant := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2026, 7, 8, 9, 10, 11, 0, time.UTC)

	got := collectionFromSQLC(sqlc.Collection{
		TenantID:        pgUUID(tenant),
		Name:            "collection-name",
		DisplayName:     "display-name",
		CedarPolicy:     "permit(principal, action, resource);",
		LifecycleRules:  []byte(`[{"id":"expire"}]`),
		ResourceVersion: 5,
		CreatedAt:       pgTS(created),
		UpdatedAt:       pgTS(updated),
	}, "backend-name", "bucket-name")

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"TenantID", got.TenantID, tenant},
		{"Collection", got.Collection, "collection-name"},
		{"DisplayName", got.DisplayName, "display-name"},
		{"BackendID", got.BackendID, "backend-name"},
		{"BucketName", got.BucketName, "bucket-name"},
		{"CedarPolicy", got.CedarPolicy, "permit(principal, action, resource);"},
		{"ResourceVersion", got.ResourceVersion, int64(5)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
	if string(got.LifecycleRules) != `[{"id":"expire"}]` {
		t.Errorf("LifecycleRules = %s", got.LifecycleRules)
	}
	if !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(updated) {
		t.Errorf("timestamps = (%v, %v), want (%v, %v)", got.CreatedAt, got.UpdatedAt, created, updated)
	}
}

// backendFromGetRow maps twenty-odd columns, among them four pairs of
// adjacent strings — endpoint/public endpoint, SSE type/key id, events
// target/queue url, and the current and previous credential refs. The last
// pair is the one that matters: the previous ref exists so a rotated-out
// credential keeps working for its grace window, and reading it as the
// current one means authenticating with the key that was just retired.
func TestBackendFromGetRow(t *testing.T) {
	checked := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	validUntil := time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC)
	created := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	got := backendFromGetRow(sqlc.GetStorageBackendV2Row{
		Name:                          "backend-name",
		DisplayName:                   "display-name",
		Kind:                          "s3-compatible",
		Provider:                      "garage",
		Endpoint:                      "http://internal.endpoint",
		PublicEndpoint:                "https://public.endpoint",
		Region:                        "eu-west-3",
		ForcePathStyle:                true,
		CredentialsSecretRef:          "secret/current",
		SseType:                       "aws:kms",
		SseKeyID:                      "key-id",
		EventsEnabled:                 true,
		EventsTarget:                  "events-target",
		EventsQueueUrl:                "https://queue.url",
		EventsPollIntervalMs:          1500,
		CedarPolicy:                   "permit(principal, action, resource);",
		Enabled:                       true,
		ReadOnly:                      false,
		Maintenance:                   true,
		HealthStatus:                  "degraded",
		HealthMessage:                 "health-message",
		HealthCheckedAt:               pgTS(checked),
		PreviousCredentialsSecretRef:  "secret/previous",
		PreviousCredentialsValidUntil: pgTS(validUntil),
		ResourceVersion:               13,
		CreatedAt:                     pgTS(created),
		UpdatedAt:                     pgTS(updated),
	})

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"BackendID", got.BackendID, "backend-name"},
		{"DisplayName", got.DisplayName, "display-name"},
		{"Kind", got.Kind, "s3-compatible"},
		{"Provider", got.Provider, "garage"},
		{"Endpoint", got.Endpoint, "http://internal.endpoint"},
		{"PublicEndpoint", got.PublicEndpoint, "https://public.endpoint"},
		{"Region", got.Region, "eu-west-3"},
		{"ForcePathStyle", got.ForcePathStyle, true},
		{"CredentialsSecretRef", got.CredentialsSecretRef, "secret/current"},
		{"PreviousCredentialsSecretRef", got.PreviousCredentialsSecretRef, "secret/previous"},
		{"SSE.Type", got.SSE.Type, "aws:kms"},
		{"SSE.KeyID", got.SSE.KeyID, "key-id"},
		{"Events.Enabled", got.Events.Enabled, true},
		{"Events.Target", got.Events.Target, "events-target"},
		{"Events.QueueURL", got.Events.QueueURL, "https://queue.url"},
		{"Events.PollInterval", got.Events.PollInterval, 1500 * time.Millisecond},
		{"CedarPolicy", got.CedarPolicy, "permit(principal, action, resource);"},
		{"Enabled", got.Enabled, true},
		{"ReadOnly", got.ReadOnly, false},
		{"Maintenance", got.Maintenance, true},
		{"HealthStatus", got.HealthStatus, "degraded"},
		{"HealthMessage", got.HealthMessage, "health-message"},
		{"ResourceVersion", got.ResourceVersion, int64(13)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
	for _, ts := range []struct {
		field string
		got   time.Time
		want  time.Time
	}{
		{"HealthCheckedAt", got.HealthCheckedAt, checked},
		{"PreviousCredentialsValidUntil", got.PreviousCredentialsValidUntil, validUntil},
		{"CreatedAt", got.CreatedAt, created},
		{"UpdatedAt", got.UpdatedAt, updated},
	} {
		if !ts.got.Equal(ts.want) {
			t.Errorf("%s = %v, want %v", ts.field, ts.got, ts.want)
		}
	}

	// A backend that has never rotated has no grace window. A zero time here
	// would be a deadline in the past, which reads the same as an expired
	// one — harmless today, and exactly the sort of thing that stops being
	// harmless when someone starts comparing against it.
	bare := backendFromGetRow(sqlc.GetStorageBackendV2Row{Name: "b"})
	if !bare.PreviousCredentialsValidUntil.IsZero() || !bare.HealthCheckedAt.IsZero() {
		t.Errorf("unset timestamps = (%v, %v), want zero",
			bare.PreviousCredentialsValidUntil, bare.HealthCheckedAt)
	}
}

// An audit entry is the record of what happened. Five adjacent strings carry
// who, what and against which resource; two adjacent JSONB columns carry the
// before and after. Transposing the last pair does not corrupt the log in any
// way a reader can detect — it inverts the direction of every change in it.
func TestAuditEntryFromModel(t *testing.T) {
	entryID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	actorTenant := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	capability := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	resourceTenant := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	at := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	sourceIP, errMsg := "203.0.113.7", "permission denied"

	got := auditEntryFromModel(sqlc.AuditLog{
		ID:            pgUUID(entryID),
		At:            pgTS(at),
		ActorSubject:  "actor-subject",
		ActorTenantID: pgUUID(actorTenant),
		ActorAudience: "actor-audience",
		Action:        "action-name",
		ResourceName:  "resource-name",
		RequestID:     "request-id",
		SourceIp:      &sourceIP,
		CapabilityID:  pgUUID(capability),
		ErrorMessage:  &errMsg,
		BeforeJson:    []byte(`{"side":"before"}`),
		AfterJson:     []byte(`{"side":"after"}`),

		ResourceTenantID: pgUUID(resourceTenant),
	})

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"EntryID", got.EntryID, entryID},
		{"ActorSubject", got.ActorSubject, "actor-subject"},
		{"ActorTenantID", got.ActorTenantID, actorTenant},
		{"ActorAudience", got.ActorAudience, "actor-audience"},
		{"Action", got.Action, "action-name"},
		{"ResourceName", got.ResourceName, "resource-name"},
		{"RequestID", got.RequestID, "request-id"},
		{"SourceIP", got.SourceIP, "203.0.113.7"},
		{"ErrorMessage", got.ErrorMessage, "permission denied"},
		{"CapabilityID", got.CapabilityID, capability},
		{"ResourceTenantID", got.ResourceTenantID, resourceTenant},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
	if string(got.BeforeJSON) != `{"side":"before"}` {
		t.Errorf("BeforeJSON = %s", got.BeforeJSON)
	}
	if string(got.AfterJSON) != `{"side":"after"}` {
		t.Errorf("AfterJSON = %s", got.AfterJSON)
	}
	if !got.At.Equal(at) {
		t.Errorf("At = %v, want %v", got.At, at)
	}

	// NULL source_ip and error_message are the ordinary case — a successful
	// call from inside the cluster — and must read as absent, not as the
	// string "<nil>" or a panic.
	bare := auditEntryFromModel(sqlc.AuditLog{})
	if bare.SourceIP != "" || bare.ErrorMessage != "" {
		t.Errorf("NULL columns = (%q, %q), want empty", bare.SourceIP, bare.ErrorMessage)
	}
	if bare.CapabilityID != uuid.Nil {
		t.Errorf("CapabilityID with no capability = %v, want Nil", bare.CapabilityID)
	}
}

func TestObjectFromSQLC(t *testing.T) {
	objectID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	tenant := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC)
	committed := time.Date(2026, 1, 4, 3, 4, 5, 0, time.UTC)
	terminated := time.Date(2026, 1, 5, 3, 4, 5, 0, time.UTC)
	presign := time.Date(2026, 1, 6, 3, 4, 5, 0, time.UTC)

	etag, checksum, sequencer, extRef := "etag-value", "checksum-value", "sequencer-value", "external-ref"
	size := int64(8192)

	got := objectFromSQLC(sqlc.Object{
		ID:                pgUUID(objectID),
		TenantID:          pgUUID(tenant),
		Path:              "some/key",
		State:             "COMMITTED",
		ContentType:       "application/json",
		SizeBytes:         &size,
		Etag:              &etag,
		ChecksumAlgorithm: checksumAlgoInt("CRC32C"),
		Checksum:          &checksum,
		Sequencer:         &sequencer,
		Metadata:          []byte(`{"origin":"metadata"}`),
		Tags:              []byte(`{"origin":"tags"}`),
		ExternalRef:       &extRef,
		ResourceVersion:   4,
		CreatedAt:         pgTS(created),
		UpdatedAt:         pgTS(updated),
		CommittedAt:       pgTS(committed),
		TerminatedAt:      pgTS(terminated),
		PresignExpiresAt:  pgTS(presign),
	}, "collection-name")

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"ObjectID", got.ObjectID, objectID},
		{"TenantID", got.TenantID, tenant},
		{"Collection", got.Collection, "collection-name"},
		{"Key", got.Key, "some/key"},
		{"State", string(got.State), "COMMITTED"},
		{"ContentType", got.ContentType, "application/json"},
		{"SizeBytes", got.SizeBytes, int64(8192)},
		{"ETag", got.ETag, "etag-value"},
		{"ChecksumAlgo", got.ChecksumAlgo, "CRC32C"},
		{"Checksum", got.Checksum, "checksum-value"},
		{"Sequencer", got.Sequencer, "sequencer-value"},
		{"ExternalRef", got.ExternalRef, "external-ref"},
		{"ResourceVersion", got.ResourceVersion, int64(4)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
	if got.Metadata["origin"] != "metadata" || got.Tags["origin"] != "tags" {
		t.Errorf("metadata/tags = (%v, %v)", got.Metadata, got.Tags)
	}
	// Three optional instants in a row, and they mean opposite things: a
	// committed object is live, a terminated one is gone. Reading one as the
	// other changes what the object is.
	for _, ts := range []struct {
		field string
		got   *time.Time
		want  time.Time
	}{
		{"CommittedAt", got.CommittedAt, committed},
		{"TerminatedAt", got.TerminatedAt, terminated},
		{"PresignExpiresAt", got.PresignExpiresAt, presign},
	} {
		if ts.got == nil || !ts.got.Equal(ts.want) {
			t.Errorf("%s = %v, want %v", ts.field, ts.got, ts.want)
		}
	}
	if !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(updated) {
		t.Errorf("timestamps = (%v, %v)", got.CreatedAt, got.UpdatedAt)
	}

	// A live object has never been terminated: the pointer stays nil rather
	// than becoming a zero instant, which any "terminated before now" check
	// would read as terminated.
	bare := objectFromSQLC(sqlc.Object{}, "c")
	if bare.TerminatedAt != nil || bare.CommittedAt != nil || bare.PresignExpiresAt != nil {
		t.Errorf("unset instants = (%v, %v, %v), want nil",
			bare.CommittedAt, bare.TerminatedAt, bare.PresignExpiresAt)
	}
	if bare.SizeBytes != 0 {
		t.Errorf("SizeBytes on a NULL column = %d, want 0", bare.SizeBytes)
	}
}

func TestEventSubFromSQLC(t *testing.T) {
	subID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	tenant := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

	got := eventSubFromSQLC(sqlc.EventSubscription{
		ID:              pgUUID(subID),
		TenantID:        pgUUID(tenant),
		CelFilter:       `event.type == "object.committed"`,
		SinkKind:        "webhook",
		SinkConfig:      []byte(`{"url":"https://sink.example"}`),
		Disabled:        true,
		ResourceVersion: 6,
		CreatedAt:       pgTS(created),
		UpdatedAt:       pgTS(updated),
	})

	if got.SubscriptionID != subID || got.TenantID != tenant {
		t.Errorf("ids = (%v, %v), want (%v, %v)", got.SubscriptionID, got.TenantID, subID, tenant)
	}
	if got.CELFilter != `event.type == "object.committed"` {
		t.Errorf("CELFilter = %q", got.CELFilter)
	}
	if got.SinkKind != "webhook" {
		t.Errorf("SinkKind = %q, want webhook", got.SinkKind)
	}
	if string(got.SinkConfig) != `{"url":"https://sink.example"}` {
		t.Errorf("SinkConfig = %s", got.SinkConfig)
	}
	if !got.Disabled {
		t.Error("Disabled = false, want true")
	}
	if got.ResourceVersion != 6 {
		t.Errorf("ResourceVersion = %d, want 6", got.ResourceVersion)
	}
	if !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(updated) {
		t.Errorf("timestamps = (%v, %v), want (%v, %v)", got.CreatedAt, got.UpdatedAt, created, updated)
	}
}

// The Get and List queries select the same columns into two sqlc row structs,
// so the mapping onto the domain exists twice. Nothing made them agree, and
// they had stopped: the List copy dropped maintenance and all three health
// columns, both queries having selected them. A backend in maintenance showed
// as available in every listing while Get on the same backend told the truth.
//
// Rather than assert the same field list twice, this fills both rows from one
// set of values and requires the two mappers to produce the same domain
// object. A column added to one mapper and forgotten in the other fails here.
func TestBackendRowMappersAgree(t *testing.T) {
	checked := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	validUntil := time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC)
	created := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	get := sqlc.GetStorageBackendV2Row{
		Name:                          "backend-name",
		DisplayName:                   "display-name",
		Kind:                          "s3-compatible",
		Provider:                      "garage",
		Endpoint:                      "http://internal.endpoint",
		PublicEndpoint:                "https://public.endpoint",
		Region:                        "eu-west-3",
		ForcePathStyle:                true,
		CredentialsSecretRef:          "secret/current",
		SseType:                       "aws:kms",
		SseKeyID:                      "key-id",
		EventsEnabled:                 true,
		EventsTarget:                  "events-target",
		EventsQueueUrl:                "https://queue.url",
		EventsPollIntervalMs:          1500,
		CedarPolicy:                   "permit(principal, action, resource);",
		Enabled:                       true,
		ReadOnly:                      true,
		Maintenance:                   true,
		HealthStatus:                  "degraded",
		HealthMessage:                 "health-message",
		HealthCheckedAt:               pgTS(checked),
		PreviousCredentialsSecretRef:  "secret/previous",
		PreviousCredentialsValidUntil: pgTS(validUntil),
		ResourceVersion:               13,
		CreatedAt:                     pgTS(created),
		UpdatedAt:                     pgTS(updated),
	}
	list := sqlc.ListStorageBackendsRow{
		Name:                          get.Name,
		DisplayName:                   get.DisplayName,
		Kind:                          get.Kind,
		Provider:                      get.Provider,
		Endpoint:                      get.Endpoint,
		PublicEndpoint:                get.PublicEndpoint,
		Region:                        get.Region,
		ForcePathStyle:                get.ForcePathStyle,
		CredentialsSecretRef:          get.CredentialsSecretRef,
		SseType:                       get.SseType,
		SseKeyID:                      get.SseKeyID,
		EventsEnabled:                 get.EventsEnabled,
		EventsTarget:                  get.EventsTarget,
		EventsQueueUrl:                get.EventsQueueUrl,
		EventsPollIntervalMs:          get.EventsPollIntervalMs,
		CedarPolicy:                   get.CedarPolicy,
		Enabled:                       get.Enabled,
		ReadOnly:                      get.ReadOnly,
		Maintenance:                   get.Maintenance,
		HealthStatus:                  get.HealthStatus,
		HealthMessage:                 get.HealthMessage,
		HealthCheckedAt:               get.HealthCheckedAt,
		PreviousCredentialsSecretRef:  get.PreviousCredentialsSecretRef,
		PreviousCredentialsValidUntil: get.PreviousCredentialsValidUntil,
		ResourceVersion:               get.ResourceVersion,
		CreatedAt:                     get.CreatedAt,
		UpdatedAt:                     get.UpdatedAt,
	}

	fromGet, fromList := backendFromGetRow(get), backendFromListRow(list)
	if !reflect.DeepEqual(fromGet, fromList) {
		t.Errorf("Get and List mappers disagree over the same row\n get:  %+v\n list: %+v", fromGet, fromList)
	}
	// And the values have to actually arrive: two mappers that both drop a
	// column agree with each other perfectly.
	if !fromList.Maintenance || fromList.HealthStatus != "degraded" || fromList.HealthCheckedAt.IsZero() {
		t.Errorf("List mapper lost the operational state: maintenance=%v health=%q checked=%v",
			fromList.Maintenance, fromList.HealthStatus, fromList.HealthCheckedAt)
	}
}
