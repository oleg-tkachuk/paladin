// Package data wires the generated paladin.data.v1 Connect server stubs onto the
// existing handler packages under internal/api/v1/. Each *.go file in this
// package maps one data-plane service.
//
// The data plane uses fully hierarchical AIP-122 names like
//
//	tenants/{tenant_id}/objectKeys/{object_key}/objects/{object_id}
//
// The shim parses each name, asserts the URL tenant matches the JWT tenant,
// and forwards the (object_key, object_id) pair to the handler — handlers
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

// objectNameParts decodes "tenants/{tenant_id}/objectKeys/{object_key}/objects/{object_id}".
// It also asserts the parsed tenant matches the JWT-bound tenant (when the
// caller has one). Cross-tenant access on the data plane is rejected.
//
// `object_key` can be a multi-segment slash-separated path
// (`invoices/2026/q1`); rather than splitting by `/` and counting
// fixed positions we anchor on the literal `tenants/<id>/objectKeys/`
// prefix and the `/objects/<uuid>` suffix, treating everything in
// between as the object_key body.
func objectNameParts(ctx context.Context, name string) (objectKey, objectID string, err error) {
	const prefix = "tenants/"
	const okSep = "/objectKeys/"
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
	// the object_key (unusual but legal) can't shadow the suffix.
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

// objectKeyNameParts decodes "tenants/{tenant_id}/objectKeys/{object_key}".
// object_key can be a multi-segment slash-separated path; everything
// after `objectKeys/` is the body.
func objectKeyNameParts(ctx context.Context, name string) (objectKey string, err error) {
	const prefix = "tenants/"
	const okSep = "/objectKeys/"
	if !strings.HasPrefix(name, prefix) {
		return "", fmt.Errorf("invalid object_key name %q", name)
	}
	rest := name[len(prefix):]
	tIDEnd := strings.Index(rest, okSep)
	if tIDEnd <= 0 {
		return "", fmt.Errorf("invalid object_key name %q", name)
	}
	tIDStr := rest[:tIDEnd]
	ok := rest[tIDEnd+len(okSep):]
	if ok == "" {
		return "", fmt.Errorf("invalid object_key name %q", name)
	}
	if _, err := uuid.Parse(tIDStr); err != nil {
		return "", fmt.Errorf("invalid tenant_id in name: %w", err)
	}
	if err := assertJWTTenant(ctx, tIDStr); err != nil {
		return "", err
	}
	return ok, nil
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
		Name:             fmt.Sprintf("tenants/%s/objectKeys/%s/objects/%s", o.TenantID, o.ObjectKey, o.ObjectID),
		ObjectId:         o.ObjectID.String(),
		TenantId:         o.TenantID.String(),
		ObjectKey:        o.ObjectKey,
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
