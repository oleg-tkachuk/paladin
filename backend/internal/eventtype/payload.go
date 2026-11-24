package eventtype

// The keys of an object event's payload. Subscribers read them by name, so
// every producer of an object event spells them from here: two spellings of
// one fact is a payload a consumer has to read two ways.
const (
	PayloadTenantID   = "tenant_id"
	PayloadCollection = "collection"
	PayloadKey        = "key"
	PayloadObjectID   = "object_id"
	PayloadBackendID  = "backend_id"
	PayloadBucket     = "bucket"
	PayloadReclaimed  = "reclaimed"
	PayloadSource     = "source"
	// What paladin.object.uploaded says of the bytes, from CompleteObject,
	// a copy and the reconciler alike.
	PayloadSizeBytes   = "size_bytes"
	PayloadETag        = "etag"
	PayloadContentType = "content_type"
)
