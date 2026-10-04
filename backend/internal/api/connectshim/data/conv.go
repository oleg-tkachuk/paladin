// Package data wires the generated paladin.data.v1 Connect server stubs onto the
// handler packages under internal/api/data/v1/. Each *.go file in this
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
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// ─── ts helpers ─────────────────────────────────────────────────────────────

// ─── Resource-name parsers ──────────────────────────────────────────────────

// Names are parsed by the Go SDK's parsers, which both SDKs' tests hold to
// one shared table (sdk/testdata/names.json), as do this package's: the server
// and the SDKs cannot disagree on a name. A collection may contain '/', and
// UUIDs come back in their canonical lower-case form.

// parseObjectName decodes "tenants/{tenant_id}/collections/{collection}/objects/{object_id}"
// and asserts the tenant matches the caller's (assertJWTTenant): cross-tenant
// access on the data plane is refused.
func parseObjectName(ctx context.Context, name string) (paladin.ObjectName, error) {
	n, err := paladin.ParseObjectName(name)
	if err != nil {
		return paladin.ObjectName{}, err
	}
	if err := assertJWTTenant(ctx, n.Tenant); err != nil {
		return paladin.ObjectName{}, err
	}
	return n, nil
}

// objectNameParts is parseObjectName's collection and object id.
func objectNameParts(ctx context.Context, name string) (collection, objectID string, err error) {
	n, err := parseObjectName(ctx, name)
	if err != nil {
		return "", "", err
	}
	return n.Collection, n.Object, nil
}

// collectionNameParts decodes "tenants/{tenant_id}/collections/{collection}"
// and asserts the tenant as objectNameParts does.
func collectionNameParts(ctx context.Context, name string) (collection string, err error) {
	n, err := paladin.ParseCollectionName(name)
	if err != nil {
		return "", err
	}
	if err := assertJWTTenant(ctx, n.Tenant); err != nil {
		return "", err
	}
	return n.Collection, nil
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

// objectName is o's resource name, printed as the SDKs print it.
func objectName(o *objecth.Object) paladin.ObjectName {
	return paladin.ObjectName{
		CollectionName: paladin.CollectionName{Tenant: o.TenantID.String(), Collection: o.Collection},
		Object:         o.ObjectID.String(),
	}
}

func objectToProto(o *objecth.Object) *pb.Object {
	if o == nil {
		return nil
	}
	out := &pb.Object{
		Name:             objectName(o).String(),
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
		ResourceVersion:  convx.ResourceVersion(o.ResourceVersion),
		CreatedAt:        convx.TsProto(o.CreatedAt),
		UpdatedAt:        convx.TsProto(o.UpdatedAt),
		CommittedAt:      convx.TsPtrProto(o.CommittedAt),
		TerminatedAt:     convx.TsPtrProto(o.TerminatedAt),
		PresignExpiresAt: convx.TsPtrProto(o.PresignExpiresAt),
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
			RetainUntil: convx.TsPtrProto(o.Lock.RetainUntil),
			LegalHold:   o.Lock.LegalHold,
		}
	}
	out.Taint = taintToProto(o.Taint)
	// PhysicalPlacement only surfaced for privileged callers — slice 4 wires
	// the role check; for now we leave it unset.
	return out
}

// taintSignals maps the stored signal names to the proto enum and back.
var taintSignals = map[string]pb.TaintSignal{
	objecth.TaintPromptInjection: pb.TaintSignal_TAINT_SIGNAL_PROMPT_INJECTION,
	objecth.TaintPII:             pb.TaintSignal_TAINT_SIGNAL_PII,
	objecth.TaintSecrets:         pb.TaintSignal_TAINT_SIGNAL_SECRETS,
}

func taintToProto(signals []string) []pb.TaintSignal {
	if len(signals) == 0 {
		return nil
	}
	out := make([]pb.TaintSignal, 0, len(signals))
	for _, s := range signals {
		if v, ok := taintSignals[s]; ok {
			out = append(out, v)
		}
	}
	return out
}

// taintFromProto maps request signals to their stored names. An enum value
// with no stored name — UNSPECIFIED, or one this server does not know —
// passes through as its proto name, so the handler's validation rejects it
// by name instead of the signal being silently dropped.
func taintFromProto(signals []pb.TaintSignal) []string {
	out := make([]string, 0, len(signals))
	for _, v := range signals {
		name := v.String()
		for stored, pv := range taintSignals {
			if pv == v {
				name = stored
				break
			}
		}
		out = append(out, name)
	}
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
		return checksum.CRC32C
	case commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256:
		return checksum.SHA256
	case commonpb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_MD5:
		return checksum.MD5
	}
	// UNSPECIFIED → SHA256 default. Connect-JSON omits enum-zero on
	// the wire, so any client (including stale browser bundles) that
	// forgets to set the field would otherwise blow up at the
	// validator. SHA256 is what the data-plane verifies on
	// CompleteObject anyway.
	return checksum.SHA256
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

func completionModeProto(m objecth.CompletionMode) commonpb.CompletionMode {
	switch m {
	case objecth.CompletionModeImplicit:
		return commonpb.CompletionMode_COMPLETION_MODE_IMPLICIT
	case objecth.CompletionModeExplicit:
		return commonpb.CompletionMode_COMPLETION_MODE_EXPLICIT
	}
	return commonpb.CompletionMode_COMPLETION_MODE_UNSPECIFIED
}
