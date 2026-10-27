package batchh

import (
	"encoding/json"
	"fmt"
	"uuid"
)

// The operation types the batch RPCs submit, and the worker runs by name.
const (
	OpBatchDelete         = "BatchDelete"
	OpBatchCopy           = "BatchCopy"
	OpBatchUpdateTags     = "BatchUpdateTags"
	OpBatchRestoreObjects = "BatchRestoreObjects"
)

// Target is what an operation acts on, as its request named it: the part of
// the worker's arguments a reader of the operation needs to see it and to
// start it again for the objects it could not act on, and no more — the
// object ids stay with the worker.
type Target struct {
	// Collection and Destination are resource names,
	// "tenants/{tenant_id}/collections/{name}"; Destination only for a copy.
	Collection  string
	Destination string
	ObjectCount int
	Permanent   bool
	Replace     bool
	// KeyPrefix is a copy's destination key prefix; Tags a tag update's tags.
	KeyPrefix string
	Tags      map[string]string
}

// CollectionName is a collection's resource name.
func CollectionName(tenantID uuid.UUID, collection string) string {
	return fmt.Sprintf("tenants/%s/collections/%s", tenantID, collection)
}

// DescribeOperation reads the target of an operation of opType from its
// arguments. It reports false for a type it does not know and for arguments
// it cannot read: a listing shows such an operation without a target rather
// than failing.
func DescribeOperation(opType string, metadata []byte) (Target, bool) {
	switch opType {
	case OpBatchDelete:
		var a BatchDeleteArgs
		if json.Unmarshal(metadata, &a) != nil {
			return Target{}, false
		}
		return Target{Collection: CollectionName(a.TenantID, a.Collection), ObjectCount: len(a.ObjectIDs), Permanent: a.Permanent}, true
	case OpBatchCopy:
		var a BatchCopyArgs
		if json.Unmarshal(metadata, &a) != nil {
			return Target{}, false
		}
		return Target{
			Collection:  CollectionName(a.TenantID, a.SrcCollection),
			Destination: CollectionName(a.TenantID, a.DstCollection),
			ObjectCount: len(a.ObjectIDs),
			KeyPrefix:   a.KeyPrefix,
		}, true
	case OpBatchUpdateTags:
		var a BatchUpdateTagsArgs
		if json.Unmarshal(metadata, &a) != nil {
			return Target{}, false
		}
		return Target{Collection: CollectionName(a.TenantID, a.Collection), ObjectCount: len(a.ObjectIDs), Replace: a.Replace, Tags: a.Tags}, true
	case OpBatchRestoreObjects:
		var a BatchRestoreObjectsArgs
		if json.Unmarshal(metadata, &a) != nil {
			return Target{}, false
		}
		return Target{Collection: CollectionName(a.TenantID, a.Collection), ObjectCount: len(a.ObjectIDs)}, true
	default:
		return Target{}, false
	}
}
