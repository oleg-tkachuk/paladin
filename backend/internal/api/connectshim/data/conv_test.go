package data

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
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

// ─── objectNameParts ───────────────────────────────────────────────────────

func name(tenant uuid.UUID, collection string, obj uuid.UUID) string {
	return "tenants/" + tenant.String() + "/collections/" + collection + "/objects/" + obj.String()
}

func TestObjectNamePartsHappyPath(t *testing.T) {
	ok, oid, err := objectNameParts(ctxTenant(tenantA), name(tenantA, "logs", objUUID))
	if err != nil {
		t.Fatalf("objectNameParts: %v", err)
	}
	if ok != "logs" {
		t.Errorf("collection = %q, want logs", ok)
	}
	if oid != objUUID.String() {
		t.Errorf("objectID = %q, want %q", oid, objUUID)
	}
}

// collection is legally a multi-segment path, so the parser must anchor on the
// literal separators rather than counting slash positions.
func TestObjectNamePartsMultiSegmentKey(t *testing.T) {
	ok, _, err := objectNameParts(ctxTenant(tenantA), name(tenantA, "invoices/2026/q1", objUUID))
	if err != nil {
		t.Fatalf("objectNameParts: %v", err)
	}
	if ok != "invoices/2026/q1" {
		t.Errorf("collection = %q, want the full path", ok)
	}
}

// An collection containing the literal "/objects/" must not shadow the real
// suffix — that is why the parser uses LastIndex.
func TestObjectNamePartsKeyContainingObjectsSegment(t *testing.T) {
	ok, oid, err := objectNameParts(ctxTenant(tenantA), name(tenantA, "a/objects/b", objUUID))
	if err != nil {
		t.Fatalf("objectNameParts: %v", err)
	}
	if ok != "a/objects/b" {
		t.Errorf("collection = %q, want a/objects/b", ok)
	}
	if oid != objUUID.String() {
		t.Errorf("objectID = %q", oid)
	}
}

func TestObjectNamePartsRejectsMalformed(t *testing.T) {
	ctx := ctxTenant(tenantA)
	cases := map[string]string{
		"empty":              "",
		"no tenants prefix":  "collections/logs/objects/" + objUUID.String(),
		"no collections":     "tenants/" + tenantA.String() + "/objects/" + objUUID.String(),
		"no objects":         "tenants/" + tenantA.String() + "/collections/logs",
		"empty tenant id":    "tenants//collections/logs/objects/" + objUUID.String(),
		"empty object key":   name(tenantA, "", objUUID),
		"bad tenant uuid":    "tenants/not-a-uuid/collections/logs/objects/" + objUUID.String(),
		"bad object uuid":    "tenants/" + tenantA.String() + "/collections/logs/objects/not-a-uuid",
		"slash in object id": "tenants/" + tenantA.String() + "/collections/logs/objects/a/b",
	}
	for label, n := range cases {
		t.Run(label, func(t *testing.T) {
			if _, _, err := objectNameParts(ctx, n); err == nil {
				t.Errorf("want an error for %q", n)
			}
		})
	}
}

// ─── collectionNameParts ────────────────────────────────────────────────────

func TestCollectionNameParts(t *testing.T) {
	ctx := ctxTenant(tenantA)

	t.Run("happy path", func(t *testing.T) {
		ok, err := collectionNameParts(ctx, "tenants/"+tenantA.String()+"/collections/logs")
		if err != nil || ok != "logs" {
			t.Errorf("got %q, %v", ok, err)
		}
	})
	t.Run("multi-segment key", func(t *testing.T) {
		ok, err := collectionNameParts(ctx, "tenants/"+tenantA.String()+"/collections/a/b/c")
		if err != nil || ok != "a/b/c" {
			t.Errorf("got %q, %v", ok, err)
		}
	})
	t.Run("rejects malformed", func(t *testing.T) {
		for label, n := range map[string]string{
			"no prefix":     "collections/logs",
			"no separator":  "tenants/" + tenantA.String(),
			"empty key":     "tenants/" + tenantA.String() + "/collections/",
			"empty tenant":  "tenants//collections/logs",
			"bad tenant id": "tenants/nope/collections/logs",
		} {
			if _, err := collectionNameParts(ctx, n); err == nil {
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
	if _, err := collectionNameParts(ctxTenant(tenantA), "tenants/"+tenantB.String()+"/collections/logs"); code(err) != connect.CodePermissionDenied {
		t.Errorf("collectionNameParts code = %v, want PermissionDenied", code(err))
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
	o := &objecth.Object{
		ObjectID: objUUID, TenantID: tenantA, Collection: "logs", Key: "a.txt",
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
	got := objectToProto(&objecth.Object{ObjectID: objUUID, TenantID: tenantA})
	if got.Checksum != nil {
		t.Errorf("Checksum = %+v, want nil", got.Checksum)
	}
}

func TestObjectToProtoIncludesPartialChecksum(t *testing.T) {
	t.Run("algo only", func(t *testing.T) {
		got := objectToProto(&objecth.Object{ChecksumAlgo: "MD5"})
		if got.Checksum == nil || got.Checksum.Algorithm != "MD5" {
			t.Errorf("Checksum = %+v", got.Checksum)
		}
	})
	t.Run("value only", func(t *testing.T) {
		got := objectToProto(&objecth.Object{Checksum: "abc"})
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
	cases := map[objecth.CompletionMode]commonpb.CompletionMode{
		objecth.CompletionModeImplicit:    commonpb.CompletionMode_COMPLETION_MODE_IMPLICIT,
		objecth.CompletionModeExplicit:    commonpb.CompletionMode_COMPLETION_MODE_EXPLICIT,
		objecth.CompletionModeUnspecified: commonpb.CompletionMode_COMPLETION_MODE_UNSPECIFIED,
		objecth.CompletionMode(99):        commonpb.CompletionMode_COMPLETION_MODE_UNSPECIFIED,
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

// ─── badName ───────────────────────────────────────────────────────────────

// The status assertJWTTenant chose has to survive the trip back through the
// name parser and out to the client. It used to be overwritten with
// invalid_argument at every call site, which is how a misscoped credential
// reached a browser as a 400 on an ordinary upload.
func TestBadName(t *testing.T) {
	t.Run("malformed name stays invalid_argument", func(t *testing.T) {
		if got := code(badName(errors.New("invalid collection name"))); got != connect.CodeInvalidArgument {
			t.Errorf("code = %v, want InvalidArgument", got)
		}
	})

	t.Run("permission denial keeps its code", func(t *testing.T) {
		denial := connect.NewError(connect.CodePermissionDenied, errors.New("URL tenant does not match token tenant"))
		if got := code(badName(denial)); got != connect.CodePermissionDenied {
			t.Errorf("code = %v, want PermissionDenied", got)
		}
	})

	// Call sites that add context with %w must not lose the code either.
	t.Run("wrapped denial keeps its code", func(t *testing.T) {
		denial := connect.NewError(connect.CodePermissionDenied, errors.New("nope"))
		if got := code(badName(fmt.Errorf("source: %w", denial))); got != connect.CodePermissionDenied {
			t.Errorf("code = %v, want PermissionDenied", got)
		}
	})

	// End to end through the parser a data-plane RPC actually calls: a
	// well-formed name for somebody else's tenant is a denial, not a
	// malformed argument.
	t.Run("cross-tenant object key name is denied, not invalid", func(t *testing.T) {
		name := "tenants/" + tenantB.String() + "/collections/docs"

		_, err := collectionNameParts(ctxTenant(tenantA), name)

		if got := code(badName(err)); got != connect.CodePermissionDenied {
			t.Errorf("code = %v, want PermissionDenied", got)
		}
	})
}

// ─── version, lock and operation projections ────────────────────────────────
//
// Three one-way conversions with no test between them. The generic AST check
// (connectshim/mapping_test.go) proves a shim READS every field on the way IN;
// nothing watched the way OUT, and these are what a client sees.

func TestLockStateToProtoCarriesAllThreeFacts(t *testing.T) {
	// Mode, expiry and legal hold are three independent reasons a delete can
	// be refused. Dropping any one of them shows an operator a lock that is
	// weaker than the one actually enforced, which is worse than showing none.
	until := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	got := lockStateToProto(objecth.ObjectLock{
		Mode: "COMPLIANCE", RetainUntil: &until, LegalHold: true,
	})
	if got.GetMode() != "COMPLIANCE" {
		t.Errorf("mode = %q", got.GetMode())
	}
	if !got.GetRetainUntil().AsTime().Equal(until) {
		t.Errorf("retainUntil = %v, want %v", got.GetRetainUntil().AsTime(), until)
	}
	if !got.GetLegalHold() {
		t.Error("legal hold was lost — it holds independently of any retention date")
	}
}

func TestLockStateToProtoAnswersUnlockedRatherThanNil(t *testing.T) {
	// The RPC's question is "what is the lock here", and "none" is an answer.
	// A nil message would read to a client as "unknown", which is a different
	// claim entirely.
	got := lockStateToProto(objecth.ObjectLock{})
	if got == nil {
		t.Fatal("unlocked rendered as nil; the caller cannot tell that from an error")
	}
	if got.GetMode() != "" || got.GetLegalHold() || got.GetRetainUntil() != nil {
		t.Errorf("unlocked state is not zero: %+v", got)
	}
}

func TestVersionToProtoCarriesTheIdentityAndBody(t *testing.T) {
	vid, oid := uuid.New(), uuid.New()
	created := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	got := versionToProto("tenants/t/collections/docs/objects/o", &objecth.ObjectVersion{
		VersionID: vid, ObjectID: oid, StoragePath: "docs/a.txt", SizeBytes: 42,
		ETag: "etag-1", ContentType: "text/plain",
		Metadata: map[string]string{"k": "v"}, Tags: map[string]string{"env": "prod"},
		CreatedAt: created, IsCurrent: true,
	})
	if got.GetName() != "tenants/t/collections/docs/objects/o/versions/"+vid.String() {
		t.Errorf("name = %q", got.GetName())
	}
	// Two UUIDs side by side: crossed, every version would claim to be its own
	// object and the history would address nothing.
	if got.GetVersionId() != vid.String() || got.GetObjectId() != oid.String() {
		t.Errorf("ids = %s / %s, want %s / %s",
			got.GetVersionId(), got.GetObjectId(), vid, oid)
	}
	if got.GetSizeBytes() != 42 || got.GetEtag() != "etag-1" ||
		got.GetContentType() != "text/plain" || got.GetStoragePath() != "docs/a.txt" {
		t.Errorf("body = %+v", got)
	}
	if got.GetMetadata()["k"] != "v" || got.GetTags()["env"] != "prod" {
		t.Errorf("maps crossed or dropped: metadata=%v tags=%v", got.GetMetadata(), got.GetTags())
	}
	if !got.GetIsCurrent() || got.GetIsDeleteMarker() {
		t.Errorf("flags = current:%v marker:%v", got.GetIsCurrent(), got.GetIsDeleteMarker())
	}
	if !got.GetCreatedAt().AsTime().Equal(created) {
		t.Errorf("createdAt = %v", got.GetCreatedAt().AsTime())
	}
}

func TestVersionToProtoOmitsEmptySubMessages(t *testing.T) {
	// A version with no checksum and no lock must carry neither sub-message.
	// An empty ChecksumDigest reads as "checksummed with the empty algorithm",
	// and an empty ObjectLockState on a history entry invents a lock.
	got := versionToProto("p", &objecth.ObjectVersion{VersionID: uuid.New(), ObjectID: uuid.New()})
	if got.GetChecksum() != nil {
		t.Errorf("checksum = %+v, want absent", got.GetChecksum())
	}
	if got.GetLock() != nil {
		t.Errorf("lock = %+v, want absent", got.GetLock())
	}
}

func TestVersionToProtoIncludesALockHeldOnlyByLegalHold(t *testing.T) {
	// A legal hold with no mode and no date is a real lock, and the cheapest
	// one to lose: every field it travels with is zero.
	got := versionToProto("p", &objecth.ObjectVersion{
		VersionID: uuid.New(), ObjectID: uuid.New(), LegalHold: true,
	})
	if got.GetLock() == nil || !got.GetLock().GetLegalHold() {
		t.Fatalf("legal hold dropped: %+v", got.GetLock())
	}
}

func TestDataOperationToProtoMarksOnlyTerminalStatesDone(t *testing.T) {
	// `Done` is what a polling client waits on. Wrong in one direction it
	// polls a finished operation forever; wrong in the other it reads a
	// half-finished result as final.
	for _, tc := range []struct {
		state    operationh.State
		wantDone bool
	}{
		{operationh.StatePending, false},
		{operationh.StateRunning, false},
		{operationh.StateSucceeded, true},
		{operationh.StateFailed, true},
		{operationh.StateCancelled, true},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			got := dataOperationToProto(&operationh.Operation{
				OperationID: uuid.New(), Type: "BatchDelete", State: tc.state,
			})
			if got.GetDone() != tc.wantDone {
				t.Errorf("done = %v, want %v", got.GetDone(), tc.wantDone)
			}
		})
	}
}

func TestDataOperationToProtoPutsFailuresInTheErrorArm(t *testing.T) {
	// The result is a oneof. A failure delivered in the response arm is a
	// client that reports success with an empty payload.
	got := dataOperationToProto(&operationh.Operation{
		OperationID: uuid.New(), Type: "BatchDelete", State: operationh.StateFailed,
		ErrorCode: "internal", ErrorMessage: "boom",
	})
	if got.GetError() == nil {
		t.Fatalf("failed operation has no error arm: %+v", got.GetResult())
	}
	if got.GetResponse() != nil {
		t.Error("a failed operation also carried a response")
	}
}
