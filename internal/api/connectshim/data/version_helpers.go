package data

import (
	"context"
	"fmt"
	"strings"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
)

// parentObjectLegacyName converts the AIP-122 parent
// "tenants/{t}/objectKeys/{ok}/objects/{id}" into the legacy form the
// VersionHandler expects ("object_keys/{ok}/objects/{id}"). Returns the
// raw input on parse failure — the handler will surface the error.
func parentObjectLegacyName(ctx context.Context, parent string) string {
	objectKey, objectID, _, err := objectNameParts(ctx, parent)
	if err != nil || objectID == "" {
		return parent
	}
	return fmt.Sprintf("object_keys/%s/objects/%s", objectKey, objectID)
}

// stripVersionSuffix removes "/versions/{ver}" tail. Returns the parent name.
func stripVersionSuffix(name string) (string, error) {
	const sep = "/versions/"
	idx := strings.LastIndex(name, sep)
	if idx <= 0 {
		return "", fmt.Errorf("invalid version name %q (missing /versions/{id})", name)
	}
	return name[:idx], nil
}

// versionToProto builds the pb.ObjectVersion envelope. parentName is the
// AIP-122 parent so the proto `name` can include the full path.
func versionToProto(parentName string, v *object.ObjectVersion) *pb.ObjectVersion {
	if v == nil {
		return nil
	}
	out := &pb.ObjectVersion{
		Name:           parentName + "/versions/" + v.VersionID.String(),
		VersionId:      v.VersionID.String(),
		ObjectId:       v.ObjectID.String(),
		IsDeleteMarker: v.IsDeleteMarker,
		S3Key:          v.S3Key,
		SizeBytes:      v.SizeBytes,
		Etag:           v.ETag,
		ContentType:    v.ContentType,
		Metadata:       v.Metadata,
		Tags:           v.Tags,
		CreatedAt:      tsProto(v.CreatedAt),
		IsCurrent:      v.IsCurrent,
	}
	if v.ChecksumAlgo != "" || v.Checksum != "" {
		out.Checksum = &pb.ChecksumDigest{Algorithm: v.ChecksumAlgo, Value: v.Checksum}
	}
	if v.LockMode != "" || v.LockRetainUntil != nil || v.LegalHold {
		out.Lock = &pb.ObjectLockState{
			Mode:        v.LockMode,
			RetainUntil: tsPtrProto(v.LockRetainUntil),
			LegalHold:   v.LegalHold,
		}
	}
	return out
}
