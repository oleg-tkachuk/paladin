package data

import (
	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// versionParent parses a version name (conv.go: the SDK's parser) and
// returns its parent, the object's name.
func versionParent(name string) (paladin.ObjectName, error) {
	n, err := paladin.ParseObjectVersionName(name)
	if err != nil {
		return paladin.ObjectName{}, err
	}
	return n.ObjectName, nil
}

// versionToProto builds the pb.ObjectVersion envelope, named under parent.
func versionToProto(parent paladin.ObjectName, v *objecth.ObjectVersion) *pb.ObjectVersion {
	if v == nil {
		return nil
	}
	out := &pb.ObjectVersion{
		Name:           paladin.ObjectVersionName{ObjectName: parent, Version: v.VersionID.String()}.String(),
		VersionId:      v.VersionID.String(),
		ObjectId:       v.ObjectID.String(),
		IsDeleteMarker: v.IsDeleteMarker,
		StoragePath:    v.StoragePath,
		SizeBytes:      v.SizeBytes,
		Etag:           v.ETag,
		ContentType:    v.ContentType,
		Metadata:       v.Metadata,
		Tags:           v.Tags,
		CreatedAt:      convx.TsProto(v.CreatedAt),
		IsCurrent:      v.IsCurrent,
	}
	if v.ChecksumAlgo != "" || v.Checksum != "" {
		out.Checksum = &pb.ChecksumDigest{Algorithm: v.ChecksumAlgo, Value: v.Checksum}
	}
	if v.LockMode != "" || v.LockRetainUntil != nil || v.LegalHold {
		out.Lock = &pb.ObjectLockState{
			Mode:        v.LockMode,
			RetainUntil: convx.TsPtrProto(v.LockRetainUntil),
			LegalHold:   v.LegalHold,
		}
	}
	return out
}

// lockStateToProto renders an object-lock state. An unlocked version is an
// ObjectLockState with everything zero rather than a nil message: the RPC
// answers "what is the lock here", and "none" is an answer.
func lockStateToProto(l objecth.ObjectLock) *pb.ObjectLockState {
	return &pb.ObjectLockState{
		Mode:        l.Mode,
		RetainUntil: convx.TsPtrProto(l.RetainUntil),
		LegalHold:   l.LegalHold,
	}
}
