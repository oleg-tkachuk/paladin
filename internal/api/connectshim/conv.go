// Package connectshim bridges the generated Connect service interfaces in
// internal/api/pb/v1/paladinv1connect to the business-logic
// handlers in internal/api/v1/*. It owns proto<->domain translation and has
// no dependencies outside aws/auth/std lib and the v2 handler packages.
package connectshim

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

func tsProto(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func tsProtoPtr(t *time.Time) *timestamppb.Timestamp {
	if t == nil || t.IsZero() {
		return nil
	}
	return timestamppb.New(*t)
}

func resourceVersion(v int64) string {
	return fmt.Sprintf("%d", v)
}

// parseTenantName extracts tenant_id from "tenants/{tenant_id}". Accepts the
// bare UUID form as well for permissive parsing.
func parseTenantName(name string) (uuid.UUID, error) {
	const prefix = "tenants/"
	if len(name) > len(prefix) && name[:len(prefix)] == prefix {
		return uuid.Parse(name[len(prefix):])
	}
	return uuid.Parse(name)
}

// parseObjectKeyName extracts objectKey from "object_keys/{objectKey}".
func parseObjectKeyName(name string) (string, error) {
	const prefix = "object_keys/"
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return "", fmt.Errorf("invalid objectKey name %q", name)
	}
	return name[len(prefix):], nil
}

// parseObjectName extracts (objectKey, object_id) from
// "object_keys/{objectKey}/objects/{object_id}".
func parseObjectName(name string) (string, uuid.UUID, error) {
	const prefix = "object_keys/"
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return "", uuid.Nil, fmt.Errorf("invalid object name %q", name)
	}
	rest := name[len(prefix):]
	sep := -1
	for i, c := range rest {
		if c == '/' {
			sep = i
			break
		}
	}
	if sep <= 0 {
		return "", uuid.Nil, fmt.Errorf("invalid object name %q", name)
	}
	objectKey := rest[:sep]
	remainder := rest[sep+1:]
	const objects = "objects/"
	if len(remainder) <= len(objects) || remainder[:len(objects)] != objects {
		return "", uuid.Nil, fmt.Errorf("invalid object name %q", name)
	}
	id, err := uuid.Parse(remainder[len(objects):])
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("invalid object_id: %w", err)
	}
	return objectKey, id, nil
}

// parseOperationName extracts operation_id from "operations/{operation_id}".
func parseOperationName(name string) (uuid.UUID, error) {
	const prefix = "operations/"
	if len(name) > len(prefix) && name[:len(prefix)] == prefix {
		return uuid.Parse(name[len(prefix):])
	}
	return uuid.Parse(name)
}

func checksumAlgoStr(a pb.ChecksumAlgorithm) string {
	switch a {
	case pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C:
		return "CRC32C"
	case pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256:
		return "SHA256"
	case pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_MD5:
		return "MD5"
	}
	return ""
}

func checksumAlgoProto(name string) pb.ChecksumAlgorithm {
	switch name {
	case "CRC32C":
		return pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C
	case "SHA256":
		return pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256
	case "MD5":
		return pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_MD5
	}
	return pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_UNSPECIFIED
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

func completionModeProto(m object.CompletionMode) pb.CompletionMode {
	switch m {
	case object.CompletionModeImplicit:
		return pb.CompletionMode_COMPLETION_MODE_IMPLICIT
	case object.CompletionModeExplicit:
		return pb.CompletionMode_COMPLETION_MODE_EXPLICIT
	}
	return pb.CompletionMode_COMPLETION_MODE_UNSPECIFIED
}

func objectToProto(o *object.Object) *pb.Object {
	if o == nil {
		return nil
	}
	return &pb.Object{
		Name:              fmt.Sprintf("object_keys/%s/objects/%s", o.ObjectKey, o.ObjectID),
		ObjectId:          o.ObjectID.String(),
		ObjectKey:         o.ObjectKey,
		Key:               o.Key,
		State:             objectStateProto(o.State),
		ContentType:       o.ContentType,
		SizeBytes:         o.SizeBytes,
		Etag:              o.ETag,
		ChecksumAlgorithm: pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_UNSPECIFIED,
		Checksum:          o.Checksum,
		Sequencer:         o.Sequencer,
		Metadata:          o.Metadata,
		Tags:              o.Tags,
		ExternalRef:       o.ExternalRef,
		ResourceVersion:   resourceVersion(o.ResourceVersion),
		CreatedAt:         tsProto(o.CreatedAt),
		UpdatedAt:         tsProto(o.UpdatedAt),
		CommittedAt:       tsProtoPtr(o.CommittedAt),
		TerminatedAt:      tsProtoPtr(o.TerminatedAt),
		PresignExpiresAt:  tsProtoPtr(o.PresignExpiresAt),
	}
}

func tenantToProto(t *tenant.Tenant) *pb.Tenant {
	if t == nil {
		return nil
	}
	return &pb.Tenant{
		Name:                 fmt.Sprintf("tenants/%s", t.TenantID),
		TenantId:             t.TenantID.String(),
		DisplayName:          t.DisplayName,
		Labels:               nil, // Labels stored as JSONB bytes; decode is caller's job.
		InheritedCedarPolicy: t.InheritedCedarPolicy,
		ResourceVersion:      resourceVersion(t.ResourceVersion),
		CreatedAt:            tsProto(t.CreatedAt),
		UpdatedAt:            tsProto(t.UpdatedAt),
	}
}

// parseResourceVersion decodes a proto string resource_version into int64.
// Empty is treated as 0 (no OCC guard).
func parseResourceVersion(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil {
		return 0, fmt.Errorf("invalid resource_version %q: %w", s, err)
	}
	return n, nil
}
