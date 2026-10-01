package middleware

const (
	// PathMetrics is the Prometheus scrape path.
	PathMetrics = "/metrics"

	// HeaderRequestID is the inbound correlation id. Configurable per
	// deployment as server.request_id_header; this is the default and what
	// the audit interceptor already reads.
	HeaderRequestID = "X-Request-Id"
)
