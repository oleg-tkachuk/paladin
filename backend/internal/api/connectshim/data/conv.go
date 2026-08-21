// Package data wires the generated paladin.data.v1 Connect server stubs onto the
// existing handler packages under internal/api/v1/. Each *.go file in this
// package maps one data-plane service.
//
// The data plane uses fully hierarchical AIP-122 names like
//
//	tenants/{tenant_id}/collections/{collection}/objects/{object_id}
//
// The shim parses each name, asserts the URL tenant matches the JWT tenant,
// and forwards the (collection, object_id) pair to the handler — handlers
// pull the tenant from JWT context, so it is never re-passed.
package data

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonpb "github.com/oleg-tkachuk/paladin/internal/api/pb/common/v1"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

// ─── ts helpers ─────────────────────────────────────────────────────────────

func tsProto(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func tsPtrProto(t *time.Time) *timestamppb.Timestamp {
	if t == nil || t.IsZero() {
		return nil
	}
	return timestamppb.New(*t)
}

func resourceVersion(v int64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatInt(v, 10)
}

func parseRV(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

func pageResponseProto(next string) *commonpb.PageResponse {
	if next == "" {
		return nil
	}
	return &commonpb.PageResponse{NextPageToken: next}
}

// ─── Resource-name parsers ──────────────────────────────────────────────────

// objectNameParts decodes "tenants/{tenant_id}/collections/{collection}/objects/{object_id}".
// It also asserts the parsed tenant matches the JWT-bound tenant (when the
// caller has one). Cross-tenant access on the data plane is rejected.
//
// `collection` can be a multi-segment slash-separated path
// (`invoices/2026/q1`); rather than splitting by `/` and counting
// fixed positions we anchor on the literal `tenants/<id>/collections/`
// prefix and the `/objects/<uuid>` suffix, treating everything in
// between as the collection body.
func objectNameParts(ctx context.Context, name string) (collection, objectID string, err error) {
	const prefix = "tenants/"
	const okSep = "/collections/"
	const objSep = "/objects/"
	if !strings.HasPrefix(name, prefix) {
		return "", "", fmt.Errorf("invalid object name %q", name)
	}
	rest := name[len(prefix):]
	tIDEnd := strings.Index(rest, okSep)
	if tIDEnd <= 0 {
		return "", "", fmt.Errorf("invalid object name %q", name)
	}
	tIDStr := rest[:tIDEnd]
	afterOK := rest[tIDEnd+len(okSep):]
	// Find the LAST "/objects/" so any "/objects/" substring inside
	// the collection (unusual but legal) can't shadow the suffix.
	objIdx := strings.LastIndex(afterOK, objSep)
	if objIdx <= 0 {
		return "", "", fmt.Errorf("invalid object name %q", name)
	}
	ok := afterOK[:objIdx]
	oIDStr := afterOK[objIdx+len(objSep):]
	if ok == "" || strings.Contains(oIDStr, "/") {
		return "", "", fmt.Errorf("invalid object name %q", name)
	}
	if _, err := uuid.Parse(tIDStr); err != nil {
		return "", "", fmt.Errorf("invalid tenant_id in name: %w", err)
	}
	if _, err := uuid.Parse(oIDStr); err != nil {
		return "", "", fmt.Errorf("invalid object_id in name: %w", err)
	}
	if err := assertJWTTenant(ctx, tIDStr); err != nil {
		return "", "", err
	}
	return ok, oIDStr, nil
}

// collectionNameParts decodes "tenants/{tenant_id}/collections/{collection}".
// collection can be a multi-segment slash-separated path; everything
// after `collections/` is the body.
func collectionNameParts(ctx context.Context, name string) (collection string, err error) {
	const prefix = "tenants/"
	const okSep = "/collections/"
	if !strings.HasPrefix(name, prefix) {
		return "", fmt.Errorf("invalid collection name %q", name)
	}
	rest := name[len(prefix):]
	tIDEnd := strings.Index(rest, okSep)
	if tIDEnd <= 0 {
		return "", fmt.Errorf("invalid collection name %q", name)
	}
	tIDStr := rest[:tIDEnd]
	ok := rest[tIDEnd+len(okSep):]
	if ok == "" {
		return "", fmt.Errorf("invalid collection name %q", name)
	}
	if _, err := uuid.Parse(tIDStr); err != nil {
		return "", fmt.Errorf("invalid tenant_id in name: %w", err)
	}
	if err := assertJWTTenant(ctx, tIDStr); err != nil {
		return "", err
	}
	return ok, nil
}

// badName turns a resource-name failure into a Connect status, keeping the
// status a lower layer already chose.
//
// A name parser fails for two unrelated reasons. The string may be malformed —
// the caller's mistake, and invalid_argument is the right answer. Or the name
// may be well-formed but address a tenant the caller may not touch, which
// assertJWTTenant reports as permission_denied. Re-wrapping both as
// invalid_argument told the second caller to fix a request that was never
// malformed, and buried an authorization decision inside a validation error:
// clients (and their operators) read a 400 and went looking for a bad field
// instead of a misscoped credential.
//
// A plain error still becomes invalid_argument, so malformed names are
// unaffected.
func badName(err error) error {
	if connect.CodeOf(err) != connect.CodeUnknown {
		return err
	}

	return connect.NewError(connect.CodeInvalidArgument, err)
}

// assertJWTTenant returns an error when the URL tenant does not match the
// caller's JWT tenant. Platform admins bypass the check.
func assertJWTTenant(ctx context.Context, urlTenantID string) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	if p.HasRole("platform.admin") {
		return nil
	}
	if p.TenantID == uuid.Nil {
		return connect.NewError(connect.CodePermissionDenied, errors.New("token has no tenant"))
	}
	if p.TenantID.String() != urlTenantID {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("URL tenant does not match token tenant"))
	}
	return nil
}

// ─── Object ↔ proto ─────────────────────────────────────────────────────────

func objectToProto(o *object.Object) *pb.Object {
	if o == nil {
		return nil
	}
	out := &pb.Object{
		Name:             fmt.Sprintf("tenants/%s/collections/%s/objects/%s", o.TenantID, o.Collection, o.ObjectID),
		ObjectId:         o.ObjectID.String(),
		TenantId:         o.TenantID.String(),
		Collection:       o.Collection,
		Key:              o.Key,
		State:            objectStateProto(o.State),
		ContentType:      o.ContentType,
		SizeBytes:        o.SizeBytes,
		Etag:             o.ETag,
		Sequencer:        o.Sequencer,
		Metadata:         o.Metadata,
		Tags:             o.Tags,
		ExternalRef:      o.ExternalRef,
		ResourceVersion:  resourceVersion(o.ResourceVersion),
		CreatedAt:        tsProto(o.CreatedAt),
		UpdatedAt:        tsProto(o.UpdatedAt),
		CommittedAt:      tsPtrProto(o.CommittedAt),
		TerminatedAt:     tsPtrProto(o.TerminatedAt),
		PresignExpiresAt: tsPtrProto(o.PresignExpiresAt),
	}
	if o.ChecksumAlgo != "" || o.Checksum != "" {
		out.Checksum = &pb.ChecksumDigest{
			Algorithm: o.ChecksumAlgo,
			Value:     o.Checksum,
		}
	}
	// Lock is set only where the handler populated it (GetObject /
	// LookupObject). The nil check keeps ListObjects rows from carrying an
	// all-zero ObjectLockState, which a client would read as "definitely
	// unlocked" rather than "not reported here".
	if o.Lock.Mode != "" || o.Lock.LegalHold || o.Lock.RetainUntil != nil {
		out.Lock = &pb.ObjectLockState{
			Mode:        o.Lock.Mode,
			RetainUntil: tsPtrProto(o.Lock.RetainUntil),
			LegalHold:   o.Lock.LegalHold,
		}
	}
	// PhysicalPlacement only surfaced for privileged callers — slice 4 wires
	// the role check; for now we leave it unset.
	return out
}

func objectStateProto(s statemachine.State) pb.ObjectState {
	switch s {
	case statemachine.StatePending:
		return pb.ObjectState_OBJECT_STATE_PENDING
	case statemachine.StateAvailable:
		return pb.ObjectState_OBJECT_STATE_AVAILABLE
	case statemachine.StateFailed:
		return pb.ObjectState_OBJECT_STATE_FAILED
	case statemachine.StateDeleted:
		return pb.ObjectState_OBJECT_STATE_DELETED
	}
	return pb.ObjectState_OBJECT_STATE_UNSPECIFIED
}

// ─── Checksum + presigned URL helpers ───────────────────────────────────────

func checksumAlgoStr(a commonpb.ChecksumAlgorithm) string {
	switch a {
	case commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C:
		return "CRC32C"
	case commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256:
		return "SHA256"
	case commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_MD5:
		return "MD5"
	}
	// UNSPECIFIED → SHA256 default. Connect-JSON omits enum-zero on
	// the wire, so any client (including stale browser bundles) that
	// forgets to set the field would otherwise blow up at the
	// validator. SHA256 is what the data-plane verifies on
	// CompleteObject anyway.
	return "SHA256"
}

func presignedUrlProto(url, method string, headers map[string]string, expires time.Time, postAction string, postFields map[string]string) *commonpb.PresignedUrl {
	out := &commonpb.PresignedUrl{
		Url:             url,
		Method:          method,
		RequiredHeaders: headers,
	}
	if !expires.IsZero() {
		out.ExpiresAtRfc3339 = expires.UTC().Format(time.RFC3339)
	}
	if postAction != "" {
		out.PostPolicy = &commonpb.PresignedPostPolicy{
			Action: postAction,
			Fields: postFields,
		}
	}
	return out
}

func completionModeProto(m object.CompletionMode) commonpb.CompletionMode {
	switch m {
	case object.CompletionModeImplicit:
		return commonpb.CompletionMode_COMPLETION_MODE_IMPLICIT
	case object.CompletionModeExplicit:
		return commonpb.CompletionMode_COMPLETION_MODE_EXPLICIT
	}
	return commonpb.CompletionMode_COMPLETION_MODE_UNSPECIFIED
}
