package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// ObjectOperationDuration tracks duration of object operations
	ObjectOperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "paladin_object_operation_duration_seconds",
			Help:    "Duration of object operations",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"operation", "status"},
	)

	// S3OperationDuration tracks duration of S3 operations
	S3OperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "paladin_s3_operation_duration_seconds",
			Help:    "Duration of S3 operations",
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		},
		[]string{"operation", "status"},
	)

	// DatabaseQueryDuration tracks duration of database queries
	DatabaseQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "paladin_db_query_duration_seconds",
			Help:    "Duration of database queries",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
		},
		[]string{"query", "status"},
	)

	// DatabaseConnectionPool tracks database connection pool metrics
	DatabaseConnectionPool = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "paladin_db_connections",
			Help: "Number of database connections by state",
		},
		[]string{"state"}, // acquired, idle, max, total
	)

	// CacheOperations tracks cache hit/miss rates
	CacheOperations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "paladin_cache_operations_total",
			Help: "Total number of cache operations",
		},
		[]string{"operation", "result"}, // operation: get/set/delete, result: hit/miss/success/error
	)

	// RateLimiterState tracks rate limiter state
	RateLimiterState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "paladin_rate_limiter_tenants",
			Help: "Number of active tenant rate limiters",
		},
		[]string{"state"}, // active, max
	)

	// HTTPRequestsTotal tracks HTTP requests by method, path, and status
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "paladin_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	// HTTPRequestDuration tracks HTTP request duration
	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "paladin_http_request_duration_seconds",
			Help:    "Duration of HTTP requests",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		},
		[]string{"method", "path", "status"},
	)

	// ObjectsTotal tracks total number of objects by status
	ObjectsTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "paladin_objects",
			Help: "Total number of objects by status",
		},
		[]string{"status"},
	)

	// MultipartUploadsTotal tracks total number of multipart uploads by status
	MultipartUploadsTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "paladin_multipart_uploads",
			Help: "Total number of multipart uploads by status",
		},
		[]string{"status"},
	)
)

// RecordObjectOperation records an object operation with duration and status
func RecordObjectOperation(operation, status string, durationSeconds float64) {
	ObjectOperationDuration.WithLabelValues(operation, status).Observe(durationSeconds)
}

// RecordS3Operation records an S3 operation with duration and status
func RecordS3Operation(operation, status string, durationSeconds float64) {
	S3OperationDuration.WithLabelValues(operation, status).Observe(durationSeconds)
}

// RecordDatabaseQuery records a database query with duration and status
func RecordDatabaseQuery(query, status string, durationSeconds float64) {
	DatabaseQueryDuration.WithLabelValues(query, status).Observe(durationSeconds)
}

// UpdateConnectionPoolMetrics updates database connection pool metrics
func UpdateConnectionPoolMetrics(acquired, idle, maxConns, total int32) {
	DatabaseConnectionPool.WithLabelValues("acquired").Set(float64(acquired))
	DatabaseConnectionPool.WithLabelValues("idle").Set(float64(idle))
	DatabaseConnectionPool.WithLabelValues("max").Set(float64(maxConns))
	DatabaseConnectionPool.WithLabelValues("total").Set(float64(total))
}

// RecordCacheOperation records a cache operation
func RecordCacheOperation(operation, result string) {
	CacheOperations.WithLabelValues(operation, result).Inc()
}

// UpdateRateLimiterMetrics updates rate limiter metrics
func UpdateRateLimiterMetrics(active, maxLimit int) {
	RateLimiterState.WithLabelValues("active").Set(float64(active))
	RateLimiterState.WithLabelValues("max").Set(float64(maxLimit))
}

// RecordHTTPRequest records an HTTP request
func RecordHTTPRequest(method, path, status string, durationSeconds float64) {
	HTTPRequestsTotal.WithLabelValues(method, path, status).Inc()
	HTTPRequestDuration.WithLabelValues(method, path, status).Observe(durationSeconds)
}
