package paladin

// ErrorDomain is the google.rpc.ErrorInfo domain of every error the server
// attaches a reason to; the reasons are commonv1.ErrorReason value names.
// The server imports it from here, so the two cannot drift.
const ErrorDomain = "paladin"
