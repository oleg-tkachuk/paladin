package paladin

// The operations a capability names in CapabilityCaveats.ops. The server
// imports these, so the two cannot spell them differently, and
// sdk/testdata/capability_ops.json holds both SDKs to the same names.
const (
	// OpGet reads one resource.
	OpGet = "get"
	// OpPut writes one resource.
	OpPut = "put"
	// OpList lists resources under a prefix.
	OpList = "list"
	// OpDelete deletes a resource.
	OpDelete = "delete"
	// OpShare delegates a narrower capability, and lets the capability be
	// sealed as a Biscuit, which is handed on offline.
	OpShare = "share"
	// OpManage administers: tenants, policy.
	OpManage = "manage"
	// OpObjectPresign mints a presigned URL for an object; the URL's own
	// method needs OpGet or OpPut beside it.
	OpObjectPresign = "object:presign"
	// OpObjectTag reads or changes an object's tags.
	OpObjectTag = "object:tag"
)
