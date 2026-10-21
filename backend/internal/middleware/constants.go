package middleware

const (
	// PathMetrics is the Prometheus scrape path.
	PathMetrics = "/metrics"

	// HeaderRequestID is the inbound correlation id. Configurable per
	// deployment as server.request_id_header; this is the default and what
	// the audit interceptor already reads.
	HeaderRequestID = "X-Request-Id"

	// HeaderUserAgent names the calling client: the SDK, its version and the
	// application's suffix. The audit log records it.
	HeaderUserAgent = "User-Agent"

	// MaxAuditUserAgentBytes is as much of a User-Agent as an audit entry
	// keeps; AuditLogEntry.user_agent documents the same bound.
	MaxAuditUserAgentBytes = 256
)
