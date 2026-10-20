package paladin

// The data plane's own capability operations, as a capability names them in
// CapabilityCaveats.ops. The others are limes's: get, put, list, delete,
// share and manage. The server imports these, so the two cannot spell them
// differently.
const (
	// OpObjectPresign mints a presigned URL for an object; the URL's own
	// method needs get or put beside it.
	OpObjectPresign = "object:presign"
	// OpObjectTag reads or changes an object's tags.
	OpObjectTag = "object:tag"
)
