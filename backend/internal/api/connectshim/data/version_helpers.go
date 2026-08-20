package data

import (
	"fmt"
	"strings"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
)

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
		StoragePath:    v.StoragePath,
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
