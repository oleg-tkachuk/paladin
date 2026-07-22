package data

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	commonpb "github.com/oleg-tkachuk/paladin/internal/api/pb/common/v1"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

// The data-plane shim's job is name parsing, tenant assertion, and proto
// projection. All of it is pure, so it unit-tests directly.

var (
	tenantA = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tenantB = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	objUUID = uuid.MustParse("33333333-3333-3333-3333-333333333333")
)

func ctxTenant(id uuid.UUID, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "tester", TenantID: id, Roles: roles,
	})
}

func code(err error) connect.Code { return connect.CodeOf(err) }

// ─── small helpers ─────────────────────────────────────────────────────────

func TestTsProto(t *testing.T) {
	if tsProto(time.Time{}) != nil {
		t.Error("a zero time must project as nil, not epoch 0")
	}
	now := time.Now().UTC().Truncate(time.Second)
	if got := tsProto(now); got == nil || !got.AsTime().Equal(now) {
		t.Errorf("tsProto(%v) = %v", now, got)
	}
}

func TestTsPtrProto(t *testing.T) {
	if tsPtrProto(nil) != nil {
		t.Error("nil must project as nil")
	}
	zero := time.Time{}
	if tsPtrProto(&zero) != nil {
		t.Error("a pointer to a zero time must project as nil")
	}
	now := time.Now().UTC().Truncate(time.Second)
	if got := tsPtrProto(&now); got == nil || !got.AsTime().Equal(now) {
		t.Errorf("tsPtrProto(%v) = %v", now, got)
	}
}

// resourceVersion is an optimistic-concurrency token; 0 means "unset" and must
// travel as an empty string so clients do not send a literal "0" back.
func TestResourceVersion(t *testing.T) {
	if got := resourceVersion(0); got != "" {
		t.Errorf("resourceVersion(0) = %q, want empty", got)
	}
	if got := resourceVersion(42); got != "42" {
		t.Errorf("resourceVersion(42) = %q", got)
	}
	if got := resourceVersion(-1); got != "-1" {
		t.Errorf("resourceVersion(-1) = %q", got)
	}
}

func TestParseRV(t *testing.T) {
	t.Run("empty is zero, not an error", func(t *testing.T) {
		got, err := parseRV("")
		if err != nil || got != 0 {
			t.Errorf("parseRV(\"\") = %d, %v", got, err)
		}
	})
	t.Run("round-trips resourceVersion", func(t *testing.T) {
		got, err := parseRV(resourceVersion(99))
		if err != nil || got != 99 {
			t.Errorf("round trip = %d, %v", got, err)
		}
	})
	t.Run("rejects garbage", func(t *testing.T) {
		if _, err := parseRV("not-a-number"); err == nil {
			t.Error("want an error")
		}
	})
}

func TestPageResponseProto(t *testing.T) {
	if pageResponseProto("") != nil {
		t.Error("no next token must project as nil, so clients see no page cursor")
	}
	if got := pageResponseProto("tok"); got == nil || got.NextPageToken != "tok" {
		t.Errorf("pageResponseProto = %v", got)
	}
}

// ─── objectNameParts ───────────────────────────────────────────────────────

func name(tenant uuid.UUID, objectKey string, obj uuid.UUID) string {
	return "tenants/" + tenant.String() + "/objectKeys/" + objectKey + "/objects/" + obj.String()
}

func TestObjectNamePartsHappyPath(t *testing.T) {
	ok, oid, err := objectNameParts(ctxTenant(tenantA), name(tenantA, "logs", objUUID))
	if err != nil {
		t.Fatalf("objectNameParts: %v", err)
	}
	if ok != "logs" {
		t.Errorf("objectKey = %q, want logs", ok)
	}
	if oid != objUUID.String() {
		t.Errorf("objectID = %q, want %q", oid, objUUID)
	}
}

// object_key is legally a multi-segment path, so the parser must anchor on the
// literal separators rather than counting slash positions.
func TestObjectNamePartsMultiSegmentKey(t *testing.T) {
	ok, _, err := objectNameParts(ctxTenant(tenantA), name(tenantA, "invoices/2026/q1", objUUID))
	if err != nil {
		t.Fatalf("objectNameParts: %v", err)
	}
	if ok != "invoices/2026/q1" {
		t.Errorf("objectKey = %q, want the full path", ok)
	}
}

// An object_key containing the literal "/objects/" must not shadow the real
// suffix — that is why the parser uses LastIndex.
func TestObjectNamePartsKeyContainingObjectsSegment(t *testing.T) {
	ok, oid, err := objectNameParts(ctxTenant(tenantA), name(tenantA, "a/objects/b", objUUID))
	if err != nil {
		t.Fatalf("objectNameParts: %v", err)
	}
	if ok != "a/objects/b" {
		t.Errorf("objectKey = %q, want a/objects/b", ok)
	}
	if oid != objUUID.String() {
		t.Errorf("objectID = %q", oid)
	}
}

func TestObjectNamePartsRejectsMalformed(t *testing.T) {
	ctx := ctxTenant(tenantA)
	cases := map[string]string{
		"empty":              "",
		"no tenants prefix":  "objectKeys/logs/objects/" + objUUID.String(),
		"no objectKeys":      "tenants/" + tenantA.String() + "/objects/" + objUUID.String(),
		"no objects":         "tenants/" + tenantA.String() + "/objectKeys/logs",
		"empty tenant id":    "tenants//objectKeys/logs/objects/" + objUUID.String(),
		"empty object key":   name(tenantA, "", objUUID),
		"bad tenant uuid":    "tenants/not-a-uuid/objectKeys/logs/objects/" + objUUID.String(),
		"bad object uuid":    "tenants/" + tenantA.String() + "/objectKeys/logs/objects/not-a-uuid",
		"slash in object id": "tenants/" + tenantA.String() + "/objectKeys/logs/objects/a/b",
	}
	for label, n := range cases {
		t.Run(label, func(t *testing.T) {
			if _, _, err := objectNameParts(ctx, n); err == nil {
				t.Errorf("want an error for %q", n)
			}
		})
	}
}

// ─── objectKeyNameParts ────────────────────────────────────────────────────

func TestObjectKeyNameParts(t *testing.T) {
	ctx := ctxTenant(tenantA)

	t.Run("happy path", func(t *testing.T) {
		ok, err := objectKeyNameParts(ctx, "tenants/"+tenantA.String()+"/objectKeys/logs")
		if err != nil || ok != "logs" {
			t.Errorf("got %q, %v", ok, err)
		}
	})
	t.Run("multi-segment key", func(t *testing.T) {
		ok, err := objectKeyNameParts(ctx, "tenants/"+tenantA.String()+"/objectKeys/a/b/c")
		if err != nil || ok != "a/b/c" {
			t.Errorf("got %q, %v", ok, err)
		}
	})
	t.Run("rejects malformed", func(t *testing.T) {
		for label, n := range map[string]string{
			"no prefix":     "objectKeys/logs",
			"no separator":  "tenants/" + tenantA.String(),
			"empty key":     "tenants/" + tenantA.String() + "/objectKeys/",
			"empty tenant":  "tenants//objectKeys/logs",
			"bad tenant id": "tenants/nope/objectKeys/logs",
		} {
			if _, err := objectKeyNameParts(ctx, n); err == nil {
				t.Errorf("%s: want an error for %q", label, n)
			}
		}
	})
}

// ─── assertJWTTenant ───────────────────────────────────────────────────────

// This is the data plane's cross-tenant gate; every name parser routes through
// it, so its branches matter more than anything else in this file.
func TestAssertJWTTenant(t *testing.T) {
	t.Run("anonymous is unauthenticated", func(t *testing.T) {
		err := assertJWTTenant(context.Background(), tenantA.String())
		if code(err) != connect.CodeUnauthenticated {
			t.Errorf("code = %v, want Unauthenticated", code(err))
		}
	})

	t.Run("matching tenant passes", func(t *testing.T) {
		if err := assertJWTTenant(ctxTenant(tenantA), tenantA.String()); err != nil {
			t.Errorf("want nil, got %v", err)
		}
	})

	t.Run("mismatched tenant is denied", func(t *testing.T) {
		err := assertJWTTenant(ctxTenant(tenantA), tenantB.String())
		if code(err) != connect.CodePermissionDenied {
			t.Errorf("code = %v, want PermissionDenied", code(err))
		}
	})

	// A token with no tenant cannot be scoped, so it must be refused rather
	// than treated as "matches everything".
	t.Run("token without a tenant is denied", func(t *testing.T) {
		err := assertJWTTenant(ctxTenant(uuid.Nil), tenantA.String())
		if code(err) != connect.CodePermissionDenied {
			t.Errorf("code = %v, want PermissionDenied", code(err))
		}
	})

	t.Run("platform admin may cross tenants", func(t *testing.T) {
		if err := assertJWTTenant(ctxTenant(tenantA, "platform.admin"), tenantB.String()); err != nil {
			t.Errorf("platform admin must bypass the check, got %v", err)
		}
	})

	// The bypass must be keyed on the platform.admin role specifically.
	t.Run("a plain admin may not cross tenants", func(t *testing.T) {
		err := assertJWTTenant(ctxTenant(tenantA, "admin"), tenantB.String())
		if code(err) != connect.CodePermissionDenied {
			t.Errorf("code = %v, want PermissionDenied", code(err))
		}
	})
}

// The gate must fire through the parsers too, not only when called directly.
func TestNameParsersEnforceTheTenantGate(t *testing.T) {
	crossTenant := name(tenantB, "logs", objUUID)

	if _, _, err := objectNameParts(ctxTenant(tenantA), crossTenant); code(err) != connect.CodePermissionDenied {
		t.Errorf("objectNameParts code = %v, want PermissionDenied", code(err))
	}
	if _, err := objectKeyNameParts(ctxTenant(tenantA), "tenants/"+tenantB.String()+"/objectKeys/logs"); code(err) != connect.CodePermissionDenied {
		t.Errorf("objectKeyNameParts code = %v, want PermissionDenied", code(err))
	}
}

// ─── objectToProto ─────────────────────────────────────────────────────────

func TestObjectToProtoNil(t *testing.T) {
	if objectToProto(nil) != nil {
		t.Error("nil in, nil out")
	}
}

func TestObjectToProtoProjectsEveryField(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	committed := now.Add(time.Minute)
	o := &object.Object{
		ObjectID: objUUID, TenantID: tenantA, ObjectKey: "logs", Key: "a.txt",
		State: statemachine.StateAvailable, ContentType: "text/plain", SizeBytes: 123,
		ETag: "etag-1", ChecksumAlgo: "SHA256", Checksum: "chk", Sequencer: "seq",
		Metadata: map[string]string{"k": "v"}, Tags: map[string]string{"t": "1"},
		ExternalRef: "ref", ResourceVersion: 7,
		CreatedAt: now, UpdatedAt: now, CommittedAt: &committed,
	}

	got := objectToProto(o)

	if want := name(tenantA, "logs", objUUID); got.Name != want {
		t.Errorf("Name = %q, want %q", got.Name, want)
	}
	if got.ObjectId != objUUID.String() || got.TenantId != tenantA.String() {
		t.Errorf("ids = %q/%q", got.ObjectId, got.TenantId)
	}
	if got.State != pb.ObjectState_OBJECT_STATE_AVAILABLE {
		t.Errorf("State = %v", got.State)
	}
	if got.SizeBytes != 123 || got.Etag != "etag-1" || got.Sequencer != "seq" {
		t.Errorf("scalar fields = %+v", got)
	}
	if got.ResourceVersion != "7" {
		t.Errorf("ResourceVersion = %q, want 7", got.ResourceVersion)
	}
	if got.Metadata["k"] != "v" || got.Tags["t"] != "1" || got.ExternalRef != "ref" {
		t.Errorf("maps/ref = %+v", got)
	}
	if got.Checksum == nil || got.Checksum.Algorithm != "SHA256" || got.Checksum.Value != "chk" {
		t.Errorf("Checksum = %+v", got.Checksum)
	}
	if got.CommittedAt == nil || !got.CommittedAt.AsTime().Equal(committed) {
		t.Errorf("CommittedAt = %v", got.CommittedAt)
	}
	// Unset optional timestamps must stay nil rather than becoming epoch 0.
	if got.TerminatedAt != nil || got.PresignExpiresAt != nil {
		t.Error("unset optional timestamps must project as nil")
	}
}

// The checksum message is omitted entirely when neither half is set, so
// clients can distinguish "no checksum" from "empty checksum".
func TestObjectToProtoOmitsEmptyChecksum(t *testing.T) {
	got := objectToProto(&object.Object{ObjectID: objUUID, TenantID: tenantA})
	if got.Checksum != nil {
		t.Errorf("Checksum = %+v, want nil", got.Checksum)
	}
}

func TestObjectToProtoIncludesPartialChecksum(t *testing.T) {
	t.Run("algo only", func(t *testing.T) {
		got := objectToProto(&object.Object{ChecksumAlgo: "MD5"})
		if got.Checksum == nil || got.Checksum.Algorithm != "MD5" {
			t.Errorf("Checksum = %+v", got.Checksum)
		}
	})
	t.Run("value only", func(t *testing.T) {
		got := objectToProto(&object.Object{Checksum: "abc"})
		if got.Checksum == nil || got.Checksum.Value != "abc" {
			t.Errorf("Checksum = %+v", got.Checksum)
		}
	})
}

// ─── enum mappings ─────────────────────────────────────────────────────────

func TestObjectStateProto(t *testing.T) {
	cases := map[statemachine.State]pb.ObjectState{
		statemachine.StatePending:   pb.ObjectState_OBJECT_STATE_PENDING,
		statemachine.StateAvailable: pb.ObjectState_OBJECT_STATE_AVAILABLE,
		statemachine.StateFailed:    pb.ObjectState_OBJECT_STATE_FAILED,
		statemachine.StateDeleted:   pb.ObjectState_OBJECT_STATE_DELETED,
		statemachine.State("weird"): pb.ObjectState_OBJECT_STATE_UNSPECIFIED,
		statemachine.State(""):      pb.ObjectState_OBJECT_STATE_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := objectStateProto(in); got != want {
			t.Errorf("objectStateProto(%q) = %v, want %v", in, got, want)
		}
	}
}

// UNSPECIFIED must fall back to SHA256: Connect-JSON omits enum-zero, so a
// client that forgets the field would otherwise trip the validator.
func TestChecksumAlgoStr(t *testing.T) {
	cases := map[commonpb.ChecksumAlgorithm]string{
		commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C:      "CRC32C",
		commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256:      "SHA256",
		commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_MD5:         "MD5",
		commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_UNSPECIFIED: "SHA256",
		commonpb.ChecksumAlgorithm(99):                            "SHA256",
	}
	for in, want := range cases {
		if got := checksumAlgoStr(in); got != want {
			t.Errorf("checksumAlgoStr(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestCompletionModeProto(t *testing.T) {
	cases := map[object.CompletionMode]commonpb.CompletionMode{
		object.CompletionModeImplicit:    commonpb.CompletionMode_COMPLETION_MODE_IMPLICIT,
		object.CompletionModeExplicit:    commonpb.CompletionMode_COMPLETION_MODE_EXPLICIT,
		object.CompletionModeUnspecified: commonpb.CompletionMode_COMPLETION_MODE_UNSPECIFIED,
		object.CompletionMode(99):        commonpb.CompletionMode_COMPLETION_MODE_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := completionModeProto(in); got != want {
			t.Errorf("completionModeProto(%v) = %v, want %v", in, got, want)
		}
	}
}

// ─── presignedUrlProto ─────────────────────────────────────────────────────

func TestPresignedUrlProto(t *testing.T) {
	expires := time.Date(2026, 7, 22, 10, 30, 0, 0, time.UTC)
	hdrs := map[string]string{"host": "s3"}

	t.Run("plain PUT", func(t *testing.T) {
		got := presignedUrlProto("https://s3/o", "PUT", hdrs, expires, "", nil)
		if got.Url != "https://s3/o" || got.Method != "PUT" {
			t.Errorf("got %+v", got)
		}
		if got.RequiredHeaders["host"] != "s3" {
			t.Errorf("headers = %v", got.RequiredHeaders)
		}
		if got.ExpiresAtRfc3339 != "2026-07-22T10:30:00Z" {
			t.Errorf("expiry = %q, want RFC3339 UTC", got.ExpiresAtRfc3339)
		}
		if got.PostPolicy != nil {
			t.Error("no post action ⇒ no post policy")
		}
	})

	// A zero expiry must leave the field empty rather than emitting year 1.
	t.Run("zero expiry omitted", func(t *testing.T) {
		got := presignedUrlProto("u", "GET", nil, time.Time{}, "", nil)
		if got.ExpiresAtRfc3339 != "" {
			t.Errorf("expiry = %q, want empty", got.ExpiresAtRfc3339)
		}
	})

	// Non-UTC input must still serialise as UTC, or clients skew the deadline.
	t.Run("expiry normalised to UTC", func(t *testing.T) {
		zone := time.FixedZone("UTC+5", 5*3600)
		got := presignedUrlProto("u", "GET", nil, expires.In(zone), "", nil)
		if got.ExpiresAtRfc3339 != "2026-07-22T10:30:00Z" {
			t.Errorf("expiry = %q, want the UTC form", got.ExpiresAtRfc3339)
		}
		if !strings.HasSuffix(got.ExpiresAtRfc3339, "Z") {
			t.Error("expiry must serialise with a Z suffix")
		}
	})

	t.Run("post policy", func(t *testing.T) {
		got := presignedUrlProto("u", "POST", nil, expires, "https://s3/bucket",
			map[string]string{"key": "k"})
		if got.PostPolicy == nil {
			t.Fatal("want a post policy")
		}
		if got.PostPolicy.Action != "https://s3/bucket" || got.PostPolicy.Fields["key"] != "k" {
			t.Errorf("post policy = %+v", got.PostPolicy)
		}
	})
}
