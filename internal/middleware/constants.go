package middleware

const (
	// Health check paths
	PathLivez    = "/health/livez"
	PathReadyz   = "/health/readyz"
	PathStartupz = "/health/startupz"

	// V1 health check paths
	PathV1Livez    = "/v1/health/livez"
	PathV1Readyz   = "/v1/health/readyz"
	PathV1Startupz = "/v1/health/startupz"

	// Technical paths
	PathMetrics = "/metrics"
	PathVersion = "/version"

	// V1 technical paths
	PathV1Metrics = "/v1/metrics"
	PathV1Version = "/v1/version"

	// OTel handler name
	OTelHandlerName = "http"
)
