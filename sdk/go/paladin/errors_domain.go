package paladin

// ErrorDomain is the google.rpc.ErrorInfo domain of every error the server
// attaches a reason to; the reasons are commonv1.ErrorReason value names.
// The server imports it from here, so the two cannot drift.
const ErrorDomain = "paladin"

// ErrorMetadataLimesCode is the ErrorInfo metadata key naming the limes code
// of an error that came from the capability library — budget_exceeded,
// not_found, store_unavailable, … — whatever Connect code it was answered
// with. sdk/testdata/error_info.json holds the server and both SDKs to it.
const ErrorMetadataLimesCode = "limes_code"
