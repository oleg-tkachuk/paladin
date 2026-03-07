package httpapi

const (
	// RouteHealthLivez is the path for liveness check.
	RouteHealthLivez = "/health/livez"
	// RouteHealthStartupz is the path for startup check.
	RouteHealthStartupz = "/health/startupz"
	// RouteHealthReadyz is the path for readiness check.
	RouteHealthReadyz = "/health/readyz"
	// RouteMetrics is the path for Prometheus metrics.
	RouteMetrics = "/metrics"
	// RouteVersion is the path for version information.
	RouteVersion = "/version"
	// RouteV1 is the base path for API v1.
	RouteV1 = "/v1"
	// RouteAdmin is the path for admin endpoints.
	RouteAdmin = "/admin"
)
