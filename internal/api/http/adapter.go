package httpapi

import (
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/errors"
	api "github.com/oleg-tkachuk/paladin/internal/generated/api"
	"github.com/oleg-tkachuk/paladin/internal/service"

	"github.com/gin-gonic/gin"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type OpenAPIAdapter struct {
	cfg       *config.Config
	svc       domain.ObjectsService
	catSvc    domain.CategoryService
	auditRepo domain.AuditLogRepository
	hs        *service.HealthService
	started   *atomic.Bool
	version   string
	commit    string
	buildTime string
}

// NewOpenAPIAdapter creates a new OpenAPIAdapter
func NewOpenAPIAdapter(cfg *config.Config, svc domain.ObjectsService, catSvc domain.CategoryService, auditRepo domain.AuditLogRepository, hs *service.HealthService, started *atomic.Bool, version, commit, buildTime string) *OpenAPIAdapter {
	return &OpenAPIAdapter{
		cfg:       cfg,
		svc:       svc,
		catSvc:    catSvc,
		auditRepo: auditRepo,
		hs:        hs,
		started:   started,
		version:   version,
		commit:    commit,
		buildTime: buildTime,
	}
}

// Ensure OpenAPIAdapter implements api.ServerInterface
var _ api.ServerInterface = (*OpenAPIAdapter)(nil)

func (s *OpenAPIAdapter) Healthz(c *gin.Context) {
	c.Status(http.StatusOK)
}

func (s *OpenAPIAdapter) HealthLivez(c *gin.Context) {
	c.JSON(http.StatusOK, api.HealthResponse{Status: "alive"})
}

// HealthReadyz implements the generated ServerInterface.
func (s *OpenAPIAdapter) HealthReadyz(c *gin.Context) {
	s.getHealthReadyz(c)
}

// Readyz implements the generated ServerInterface (compatibility).
func (s *OpenAPIAdapter) Readyz(c *gin.Context) {
	s.getHealthReadyz(c)
}

// getHealthReadyz contains the common logic for HealthReadyz and Readyz
func (s *OpenAPIAdapter) getHealthReadyz(c *gin.Context) {
	ready, status := s.hs.CheckReady(c.Request.Context())
	healthResp := api.HealthResponse{
		Dependencies: mapDependencyStatus(status),
	}

	if !ready {
		healthResp.Status = "not_ready"
		healthResp.Reason = ptr("dependency_unavailable")
		c.JSON(http.StatusServiceUnavailable, healthResp)
		return
	}

	healthResp.Status = "ready"
	c.JSON(http.StatusOK, healthResp)
}

func (s *OpenAPIAdapter) HealthStartupz(c *gin.Context) {
	if !s.started.Load() {
		c.JSON(http.StatusServiceUnavailable, api.HealthResponse{
			Status: "starting",
			Reason: ptr("initialization_in_progress"),
		})
		return
	}
	c.JSON(http.StatusOK, api.HealthResponse{Status: "started"})
}

func mapDetailedStatus(s service.DetailedDependencyStatus) *api.DetailedDependencyStatus {
	var msg *string
	if s.Message != "" {
		msg = &s.Message
	}
	var s3Ping *api.S3PingResponse
	if s.S3Ping != nil {
		s3Ping = &api.S3PingResponse{
			Status:     api.S3PingResponseStatus(s.S3Ping.Status),
			HttpStatus: s.S3Ping.HttpStatus,
			Message:    &s.S3Ping.Message,
			Bucket:     s.S3Ping.Bucket,
			Region:     &s.S3Ping.Region,
		}
	}

	return &api.DetailedDependencyStatus{
		Status:    api.DetailedDependencyStatusStatus(s.Status),
		LatencyMs: s.LatencyMs,
		Message:   msg,
		S3Ping:    s3Ping,
	}
}

func mapDependencyStatus(s service.DependencyStatus) *api.DependencyStatus {
	poolStats := make(map[string]map[string]interface{})

	// Check if s.PoolStats is already a map of maps or a flat map
	isFlat := true
	for _, v := range s.PoolStats {
		if _, ok := v.(map[string]interface{}); ok {
			isFlat = false
			break
		}
	}

	if isFlat && len(s.PoolStats) > 0 {
		// Wrap flat stats in "primary" to match OpenAPI schema expectations
		poolStats["primary"] = s.PoolStats
	} else {
		// Already nested or mixed (fallback to existing logic)
		for k, v := range s.PoolStats {
			if m, ok := v.(map[string]interface{}); ok {
				poolStats[k] = m
			}
		}
	}

	return &api.DependencyStatus{
		Postgresql: mapDetailedStatus(s.PostgreSQL),
		Seaweedfs:  mapDetailedStatus(s.SeaweedFS),
		Breakers:   &s.Breakers,
		PoolStats:  &poolStats,
	}
}

func ptr[T any](v T) *T { return &v }

func (s *OpenAPIAdapter) Version(c *gin.Context) {
	c.JSON(http.StatusOK, api.VersionResponse{
		Service:   "paladin",
		Version:   s.version,
		GitSha:    &s.commit,
		BuildTime: &s.buildTime,
	})
}

func (s *OpenAPIAdapter) PingS3(c *gin.Context) {
	res, err := s.hs.PingS3(c.Request.Context())
	if err != nil {
		respondWithError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, api.S3PingResponse{
		Status:     api.S3PingResponseStatus(res.Status),
		HttpStatus: res.HttpStatus,
		Message:    &res.Message,
		Bucket:     res.Bucket,
		Region:     ptr(s.cfg.Datastores.S3.Region),
	})
}

func (s *OpenAPIAdapter) CreateObject(c *gin.Context, params api.CreateObjectParams) {
	var req api.CreateObjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	ttl := 0
	if req.UploadExpiresInSeconds != nil {
		ttl = *req.UploadExpiresInSeconds
	}

	var category string
	if req.Category != nil {
		category = *req.Category
	}

	out, err := s.svc.CreateSingle(c.Request.Context(), tenantID(c), category, string(req.ContentType), req.SizeBytes, mapLabels(req.Labels), req.ExternalRef, ttl, params.IdempotencyKey)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusCreated, api.CreateObjectResponse{
		ObjectId:  out.ID,
		ObjectKey: out.Key,
		Bucket:    out.Bucket,
		Status:    api.Pending,
		Upload:    mapSignedAction(out.Upload),
	})
}

func (s *OpenAPIAdapter) GetObject(c *gin.Context, id openapi_types.UUID) {
	rec, err := s.svc.Get(c.Request.Context(), tenantID(c), id)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, mapObjectCommon(rec))
}

func (s *OpenAPIAdapter) HeadObject(c *gin.Context, id openapi_types.UUID) {
	_, err := s.svc.GetMeta(c.Request.Context(), tenantID(c), id)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Status(http.StatusOK)
}

func (s *OpenAPIAdapter) DeleteObject(c *gin.Context, id openapi_types.UUID, params api.DeleteObjectParams) {
	if err := s.svc.Delete(c.Request.Context(), tenantID(c), id); err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *OpenAPIAdapter) PurgeObject(c *gin.Context, id openapi_types.UUID, params api.PurgeObjectParams) {
	var idempotencyKey *string
	if params.IdempotencyKey != nil {
		k := string(*params.IdempotencyKey)
		idempotencyKey = &k
	}

	if err := s.svc.Purge(c.Request.Context(), tenantID(c), id, idempotencyKey); err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *OpenAPIAdapter) RestoreObject(c *gin.Context, id openapi_types.UUID, params api.RestoreObjectParams) {
	if err := s.svc.Restore(c.Request.Context(), tenantID(c), id); err != nil {
		respondWithError(c, 0, err)
		return
	}

	obj, err := s.svc.Get(c.Request.Context(), tenantID(c), id)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, mapObjectCommon(obj))
}

func (s *OpenAPIAdapter) UpdateObject(c *gin.Context, id openapi_types.UUID, params api.UpdateObjectParams) {
	var req api.PatchObjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	var idempotencyKey *string
	if params.IdempotencyKey != nil {
		k := string(*params.IdempotencyKey)
		idempotencyKey = &k
	}

	if err := s.svc.UpdateStatus(c.Request.Context(), tenantID(c), id, string(req.Status), idempotencyKey); err != nil {
		respondWithError(c, 0, err)
		return
	}

	// Fetch updated object to return
	rec, err := s.svc.Get(c.Request.Context(), tenantID(c), id)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, mapObjectCommon(rec))
}

func (s *OpenAPIAdapter) BulkDeleteObjects(c *gin.Context, params api.BulkDeleteObjectsParams) {
	var req api.BulkActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	count, err := s.svc.BulkDelete(c.Request.Context(), tenantID(c), req.Ids)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.BulkActionResponse{AffectedCount: ptr(int(count))})
}

func (s *OpenAPIAdapter) BulkRestoreObjects(c *gin.Context, params api.BulkRestoreObjectsParams) {
	var req api.BulkActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	count, err := s.svc.BulkRestore(c.Request.Context(), tenantID(c), req.Ids)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.BulkActionResponse{AffectedCount: ptr(int(count))})
}

func (s *OpenAPIAdapter) BulkPurgeObjects(c *gin.Context, params api.BulkPurgeObjectsParams) {
	var req api.BulkActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	var idempotencyKey *string
	if params.IdempotencyKey != nil {
		k := string(*params.IdempotencyKey)
		idempotencyKey = &k
	}

	count, err := s.svc.BulkPurge(c.Request.Context(), tenantID(c), req.Ids, idempotencyKey)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.BulkActionResponse{AffectedCount: ptr(int(count))})
}

func (s *OpenAPIAdapter) GetObjectStats(c *gin.Context) {
	stats, err := s.svc.GetStats(c.Request.Context(), tenantID(c))
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.ObjectStats{
		TotalCount:       int(stats.TotalCount),
		TotalSize:        stats.TotalSize,
		PendingCount:     int(stats.PendingCount),
		UploadingCount:   int(stats.UploadingCount),
		UploadedCount:    int(stats.UploadedCount),
		CompleteCount:    int(stats.CompleteCount),
		SoftDeletedCount: int(stats.SoftDeletedCount),
	})
}

func (s *OpenAPIAdapter) CompleteObject(c *gin.Context, id openapi_types.UUID) {
	var req api.CompleteObjectRequest
	_ = c.ShouldBindJSON(&req) // Optional body

	rec, err := s.svc.CompleteObject(c.Request.Context(), tenantID(c), id, req.Etag, req.SizeBytes)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.CompleteObjectResponse{
		Status:          mapStatus(rec.Status),
		StoredEtag:      rec.StoredETag,
		StoredSizeBytes: rec.StoredSizeBytes,
		CompletedAt:     rec.CompletedAt,
	})
}

func (s *OpenAPIAdapter) GetObjectMeta(c *gin.Context, id openapi_types.UUID) {
	rec, err := s.svc.GetMeta(c.Request.Context(), tenantID(c), id)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, mapObjectCommon(rec))
}

func (s *OpenAPIAdapter) PatchObjectMeta(c *gin.Context, id openapi_types.UUID) {
	var req api.PatchObjectMetaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	rec, err := s.svc.PatchMeta(c.Request.Context(), tenantID(c), id, mapLabels(req.Labels), req.ExternalRef)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, mapObjectCommon(rec))
}

func (s *OpenAPIAdapter) ListObjects(c *gin.Context, params api.ListObjectsParams) {
	limit := 100
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}

	filter := domain.ListObjectsFilter{
		Status:        (*domain.ObjectStatus)(params.Status),
		ExternalRef:   params.ExternalRef,
		Category:      params.Category,
		KeyPrefix:     params.Prefix,
		CreatedAfter:  params.CreatedAfter,
		CreatedBefore: params.CreatedBefore,
	}

	items, next, totalCount, err := s.svc.List(c.Request.Context(), tenantID(c), filter, limit, cursor)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	out := make([]api.ObjectCommon, len(items))
	for i, item := range items {
		out[i] = mapObjectCommon(&item)
	}

	// Determine if there are more results
	hasMore := next != ""
	var nextCursor *string
	if hasMore {
		nextCursor = &next
	}

	resp := api.ListObjectsResponse{
		Items: out,
		Pagination: api.Pagination{
			NextCursor: nextCursor,
			HasMore:    hasMore,
			TotalCount: totalCount,
		},
	}

	c.JSON(http.StatusOK, resp)
}

func (s *OpenAPIAdapter) SignObjectDownload(c *gin.Context, id openapi_types.UUID) {
	var req api.SignObjectDownloadRequest
	_ = c.ShouldBindJSON(&req)

	ttl := 0
	if req.DownloadExpiresInSeconds != nil {
		ttl = *req.DownloadExpiresInSeconds
	}

	p, err := s.svc.SignDownload(c.Request.Context(), tenantID(c), id, ttl)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.SignObjectDownloadResponse{
		ObjectId: id,
		Download: mapSignedAction(p),
	})
}

func (s *OpenAPIAdapter) SignObjectUpload(c *gin.Context, id openapi_types.UUID) {
	var req api.SignObjectUploadRequest
	_ = c.ShouldBindJSON(&req)

	ttl := 0
	if req.UploadExpiresInSeconds != nil {
		ttl = *req.UploadExpiresInSeconds
	}

	p, err := s.svc.SignUpload(c.Request.Context(), tenantID(c), id, ttl)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.SignObjectUploadResponse{
		ObjectId: id,
		Upload:   mapSignedAction(p),
	})
}

func (s *OpenAPIAdapter) InitiateMultipart(c *gin.Context, params api.InitiateMultipartParams) {
	var req api.InitiateMultipartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	ttl := 0
	if req.UploadExpiresInSeconds != nil {
		ttl = *req.UploadExpiresInSeconds
	}

	var category string
	if req.Category != nil {
		category = *req.Category
	}

	out, err := s.svc.InitiateMultipart(c.Request.Context(), tenantID(c), category, string(req.ContentType), req.SizeBytes, mapLabels(req.Labels), req.ExternalRef, ttl, params.IdempotencyKey)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.InitiateMultipartResponse{
		ObjectId:  out.ObjectID,
		ObjectKey: out.ObjectKey,
		UploadId:  out.UploadID,
		PartSize:  out.PartSize,
		ExpiresAt: out.ExpiresAt,
		Bucket:    out.Bucket,
		Status:    api.Uploading,
	})
}

func (s *OpenAPIAdapter) GetMultipart(c *gin.Context, uploadId string) {
	multi, err := s.svc.GetMultipart(c.Request.Context(), tenantID(c), uploadId)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.GetMultipartResponse{
		ObjectId:  multi.ObjectID,
		ObjectKey: multi.ObjectKey,
		UploadId:  multi.UploadID,
		PartSize:  multi.PartSize,
		Bucket:    multi.Bucket,
		Status:    mapStatus(domain.ObjectStatus(multi.Status)),
		CreatedAt: multi.CreatedAt,
		UpdatedAt: multi.UpdatedAt,
	})
}

func (s *OpenAPIAdapter) AbortMultipart(c *gin.Context, uploadId api.UploadID) {
	if err := s.svc.AbortMultipart(c.Request.Context(), tenantID(c), uploadId); err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.AbortMultipartResponse{Status: api.Aborted})
}

func (s *OpenAPIAdapter) CompleteMultipart(c *gin.Context, uploadId string) {
	var req api.CompleteMultipartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	parts := make([]domain.CompletePart, len(req.Parts))
	for i, p := range req.Parts {
		parts[i] = domain.CompletePart{PartNumber: p.PartNumber, ETag: p.Etag}
	}

	rec, err := s.svc.CompleteMultipart(c.Request.Context(), tenantID(c), uploadId, parts)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.CompleteMultipartResponse{
		ObjectId:        rec.ID,
		Status:          mapStatus(rec.Status),
		StoredEtag:      rec.StoredETag,
		StoredSizeBytes: rec.StoredSizeBytes,
		CompletedAt:     rec.CompletedAt,
	})
}

func (s *OpenAPIAdapter) SignPartsBatch(c *gin.Context, uploadId string) {
	var req api.SignPartsBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	batch, err := s.svc.SignPartsBatch(c.Request.Context(), tenantID(c), uploadId, req.PartNumbers)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	out := make([]api.SignPartResponse, len(batch))
	for i, b := range batch {
		out[i] = api.SignPartResponse{
			PartNumber: b.PartNumber,
			Upload:     mapSignedAction(b.Upload),
		}
	}

	c.JSON(http.StatusOK, api.SignPartsBatchResponse{
		UploadId: uploadId,
		Parts:    out,
	})
}

func (s *OpenAPIAdapter) SignPart(c *gin.Context, uploadId string, partNumber int32) {
	p, err := s.svc.SignPart(c.Request.Context(), tenantID(c), uploadId, partNumber)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.SignPartResponse{
		PartNumber: partNumber,
		Upload:     mapSignedAction(p),
	})
}

func (s *OpenAPIAdapter) GetAdminConfig(c *gin.Context) {
	// Parse Postgres DSN to extract connectivity details
	pgConfig, err := pgx.ParseConfig(s.cfg.Datastores.Postgres.DSN)
	var pgHost, pgPort, pgUser, pgDB, pgSSLMode string
	if err == nil {
		pgHost = pgConfig.Host
		pgPort = fmt.Sprintf("%d", pgConfig.Port)
		pgUser = pgConfig.User
		pgDB = pgConfig.Database
		// Basic extraction for SSL Mode (it might be in RuntimeParams)
		if val, ok := pgConfig.RuntimeParams["sslmode"]; ok {
			pgSSLMode = val
		} else if pgConfig.TLSConfig == nil {
			pgSSLMode = "disable"
		} else {
			pgSSLMode = "enable" // Simplified, actual mode (require, verify-full) lost in tls.Config
		}
	} else {
		// Fallback for logging or partial info if needed, but for now just leave empty
		// or log error
	}

	// Redact sensitive fields
	resp := api.ConfigResponse{
		App: &struct {
			Env  *string `json:"env,omitempty"`
			Name *string `json:"name,omitempty"`
		}{
			Name: ptr(s.cfg.App.Name),
			Env:  ptr(s.cfg.App.Env),
		},
		Server: &struct {
			Http *struct {
				Addr               *string   `json:"addr,omitempty"`
				CorsAllowedOrigins *[]string `json:"cors_allowed_origins,omitempty"`
				ReadTimeout        *string   `json:"read_timeout,omitempty"`
				RequestIdHeader    *string   `json:"request_id_header,omitempty"`
				WriteTimeout       *string   `json:"write_timeout,omitempty"`
			} `json:"http,omitempty"`
			Mode *string `json:"mode,omitempty"`
			Name *string `json:"name,omitempty"`
		}{
			Mode: ptr(s.cfg.Server.Mode),
			Name: ptr(s.cfg.Server.Name),
			Http: &struct {
				Addr               *string   `json:"addr,omitempty"`
				CorsAllowedOrigins *[]string `json:"cors_allowed_origins,omitempty"`
				ReadTimeout        *string   `json:"read_timeout,omitempty"`
				RequestIdHeader    *string   `json:"request_id_header,omitempty"`
				WriteTimeout       *string   `json:"write_timeout,omitempty"`
			}{
				Addr:               ptr(s.cfg.Server.HTTP.Addr),
				ReadTimeout:        ptr(s.cfg.Server.HTTP.ReadTimeout.String()),
				WriteTimeout:       ptr(s.cfg.Server.HTTP.WriteTimeout.String()),
				RequestIdHeader:    ptr(s.cfg.Server.HTTP.RequestIDHeader),
				CorsAllowedOrigins: &s.cfg.Server.HTTP.CORSAllowedOrigins,
			},
		},
		Datastores: &struct {
			Postgres *struct {
				Dbname *string `json:"dbname,omitempty"`
				Host   *string `json:"host,omitempty"`
				Pool   *struct {
					MaxConnIdleTime *string `json:"max_conn_idle_time,omitempty"`
					MaxConnLifetime *string `json:"max_conn_lifetime,omitempty"`
					MaxConns        *int    `json:"max_conns,omitempty"`
					MinConns        *int    `json:"min_conns,omitempty"`
				} `json:"pool,omitempty"`
				Port     *string `json:"port,omitempty"`
				SslMode  *string `json:"ssl_mode,omitempty"`
				Timeouts *struct {
					Connect   *string `json:"connect,omitempty"`
					Statement *string `json:"statement,omitempty"`
				} `json:"timeouts,omitempty"`
				User *string `json:"user,omitempty"`
			} `json:"postgres,omitempty"`
			S3 *struct {
				Bucket         *string `json:"bucket,omitempty"`
				Endpoint       *string `json:"endpoint,omitempty"`
				ForcePathStyle *bool   `json:"force_path_style,omitempty"`
				PartSize       *string `json:"part_size,omitempty"`
				PresignTtl     *string `json:"presign_ttl,omitempty"`
				PublicEndpoint *string `json:"public_endpoint,omitempty"`
				Region         *string `json:"region,omitempty"`
				SseType        *string `json:"sse_type,omitempty"`
			} `json:"s3,omitempty"`
		}{
			Postgres: &struct {
				Dbname *string `json:"dbname,omitempty"`
				Host   *string `json:"host,omitempty"`
				Pool   *struct {
					MaxConnIdleTime *string `json:"max_conn_idle_time,omitempty"`
					MaxConnLifetime *string `json:"max_conn_lifetime,omitempty"`
					MaxConns        *int    `json:"max_conns,omitempty"`
					MinConns        *int    `json:"min_conns,omitempty"`
				} `json:"pool,omitempty"`
				Port     *string `json:"port,omitempty"`
				SslMode  *string `json:"ssl_mode,omitempty"`
				Timeouts *struct {
					Connect   *string `json:"connect,omitempty"`
					Statement *string `json:"statement,omitempty"`
				} `json:"timeouts,omitempty"`
				User *string `json:"user,omitempty"`
			}{
				Dbname:  ptr(pgDB),
				Host:    ptr(pgHost),
				Port:    ptr(pgPort),
				SslMode: ptr(pgSSLMode),
				User:    ptr(pgUser),
				Pool: &struct {
					MaxConnIdleTime *string `json:"max_conn_idle_time,omitempty"`
					MaxConnLifetime *string `json:"max_conn_lifetime,omitempty"`
					MaxConns        *int    `json:"max_conns,omitempty"`
					MinConns        *int    `json:"min_conns,omitempty"`
				}{
					MaxConns:        ptr(int(s.cfg.Datastores.Postgres.Pool.MaxConns)),
					MinConns:        ptr(int(s.cfg.Datastores.Postgres.Pool.MinConns)),
					MaxConnLifetime: ptr(s.cfg.Datastores.Postgres.Pool.MaxConnLifetime.String()),
					MaxConnIdleTime: ptr(s.cfg.Datastores.Postgres.Pool.MaxConnIdleTime.String()),
				},
				Timeouts: &struct {
					Connect   *string `json:"connect,omitempty"`
					Statement *string `json:"statement,omitempty"`
				}{
					Connect:   ptr(s.cfg.Datastores.Postgres.Timeouts.Connect.String()),
					Statement: ptr(s.cfg.Datastores.Postgres.Timeouts.Statement.String()),
				},
			},
			S3: &struct {
				Bucket         *string `json:"bucket,omitempty"`
				Endpoint       *string `json:"endpoint,omitempty"`
				ForcePathStyle *bool   `json:"force_path_style,omitempty"`
				PartSize       *string `json:"part_size,omitempty"`
				PresignTtl     *string `json:"presign_ttl,omitempty"`
				PublicEndpoint *string `json:"public_endpoint,omitempty"`
				Region         *string `json:"region,omitempty"`
				SseType        *string `json:"sse_type,omitempty"`
			}{
				Bucket:         ptr(s.cfg.Datastores.S3.Bucket),
				Region:         ptr(s.cfg.Datastores.S3.Region),
				Endpoint:       ptr(s.cfg.Datastores.S3.Endpoint),
				PublicEndpoint: ptr(s.cfg.Datastores.S3.PublicEndpoint),
				ForcePathStyle: ptr(s.cfg.Datastores.S3.ForcePathStyle),
				PresignTtl:     ptr(s.cfg.Datastores.S3.PresignTTL.String()),
				PartSize:       ptr(s.cfg.Datastores.S3.PartSizeRaw),
				SseType:        ptr(s.cfg.Datastores.S3.SSEType),
			},
		},
		Policy: &struct {
			AllowedContentTypes *[]string `json:"allowed_content_types,omitempty"`
			MaxMultipartSize    *string   `json:"max_multipart_size,omitempty"`
			MaxObjectSize       *string   `json:"max_object_size,omitempty"`
			MaxPartSize         *string   `json:"max_part_size,omitempty"`
			MinPartSize         *string   `json:"min_part_size,omitempty"`
			PresignGetTtl       *string   `json:"presign_get_ttl,omitempty"`
			PresignPutTtl       *string   `json:"presign_put_ttl,omitempty"`
		}{
			MaxObjectSize:       ptr(s.cfg.Policy.MaxObjectSizeRaw),
			MaxMultipartSize:    ptr(s.cfg.Policy.MaxMultipartSizeRaw),
			MinPartSize:         ptr(s.cfg.Policy.MinPartSizeRaw),
			MaxPartSize:         ptr(s.cfg.Policy.MaxPartSizeRaw),
			PresignPutTtl:       ptr(s.cfg.Policy.PresignPutTTL.String()),
			PresignGetTtl:       ptr(s.cfg.Policy.PresignGetTTL.String()),
			AllowedContentTypes: &s.cfg.Policy.AllowedContentTypes,
		},
		Security: &struct {
			EnableRls                *bool `json:"enable_rls,omitempty"`
			RejectTenantMismatch     *bool `json:"reject_tenant_mismatch,omitempty"`
			TrustTenantIdFromRequest *bool `json:"trust_tenant_id_from_request,omitempty"`
		}{
			TrustTenantIdFromRequest: ptr(s.cfg.Security.TrustTenantIDFromRequest),
			RejectTenantMismatch:     ptr(s.cfg.Security.RejectTenantMismatch),
			EnableRls:                ptr(s.cfg.Security.EnableRLS),
		},
		Housekeeping: &struct {
			EnableReaper *bool   `json:"enable_reaper,omitempty"`
			GcInterval   *string `json:"gc_interval,omitempty"`
			MultipartTtl *string `json:"multipart_ttl,omitempty"`
			PendingTtl   *string `json:"pending_ttl,omitempty"`
		}{
			EnableReaper: ptr(s.cfg.Housekeeping.EnableReaper),
			PendingTtl:   ptr(s.cfg.Housekeeping.PendingTTL.String()),
			MultipartTtl: ptr(s.cfg.Housekeeping.MultipartTTL.String()),
			GcInterval:   ptr(s.cfg.Housekeeping.GCInterval.String()),
		},
		RateLimit: &struct {
			Burst             *int     `json:"burst,omitempty"`
			MaxTenants        *int     `json:"max_tenants,omitempty"`
			RequestsPerSecond *float32 `json:"requests_per_second,omitempty"`
		}{
			RequestsPerSecond: ptr(float32(s.cfg.RateLimit.RequestsPerSecond)),
			Burst:             ptr(s.cfg.RateLimit.Burst),
			MaxTenants:        ptr(s.cfg.RateLimit.MaxTenants),
		},
		Cache: &struct {
			Enabled *bool   `json:"enabled,omitempty"`
			MaxSize *int    `json:"max_size,omitempty"`
			Ttl     *string `json:"ttl,omitempty"`
		}{
			Enabled: ptr(s.cfg.Cache.Enabled),
			MaxSize: ptr(s.cfg.Cache.MaxSize),
			Ttl:     ptr(s.cfg.Cache.TTL.String()),
		},
		Timeouts: &struct {
			DefaultOperation *string `json:"default_operation,omitempty"`
			FastOperation    *string `json:"fast_operation,omitempty"`
			LongOperation    *string `json:"long_operation,omitempty"`
			S3Operation      *string `json:"s3_operation,omitempty"`
		}{
			FastOperation:    ptr(s.cfg.Timeouts.FastOperation.String()),
			DefaultOperation: ptr(s.cfg.Timeouts.DefaultOperation.String()),
			S3Operation:      ptr(s.cfg.Timeouts.S3Operation.String()),
			LongOperation:    ptr(s.cfg.Timeouts.LongOperation.String()),
		},
		Idempotency: &struct {
			Enabled *bool   `json:"enabled,omitempty"`
			Ttl     *string `json:"ttl,omitempty"`
		}{
			Enabled: ptr(s.cfg.Idempotency.Enabled),
			Ttl:     ptr(s.cfg.Idempotency.TTL.String()),
		},
		Otel: &struct {
			Enabled  *bool   `json:"enabled,omitempty"`
			Endpoint *string `json:"endpoint,omitempty"`
			Insecure *bool   `json:"insecure,omitempty"`
			Protocol *string `json:"protocol,omitempty"`
		}{
			Enabled:  ptr(s.cfg.OTel.Enabled),
			Endpoint: ptr(s.cfg.OTel.Endpoint),
			Protocol: ptr(s.cfg.OTel.Protocol),
			Insecure: ptr(s.cfg.OTel.Insecure),
		},
	}

	c.JSON(http.StatusOK, resp)
}

func (s *OpenAPIAdapter) ListTenants(c *gin.Context, params api.ListTenantsParams) {
	limit := 100
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}

	tenants, next, totalCount, err := s.catSvc.ListTenants(c.Request.Context(), limit, cursor)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	hasMore := next != ""
	var nextCursor *string
	if hasMore {
		nextCursor = &next
	}

	c.JSON(http.StatusOK, api.ListTenantsResponse{
		Items: tenants,
		Pagination: api.Pagination{
			NextCursor: nextCursor,
			HasMore:    hasMore,
			TotalCount: totalCount,
		},
	})
}

func mapSignedAction(p domain.Presigned) api.SignedAction {
	return api.SignedAction{
		Url:       p.URL,
		Method:    api.SignedActionMethod(p.Method),
		Headers:   &p.Headers,
		ExpiresAt: p.ExpiresAt,
	}
}

func mapStatus(s domain.ObjectStatus) api.ObjectStatus {
	return api.ObjectStatus(s)
}

func mapLabels(l *api.Labels) map[string]string {
	if l == nil {
		return nil
	}
	return (map[string]string)(*l)
}

func (s *OpenAPIAdapter) ListAuditLogs(c *gin.Context, params api.ListAuditLogsParams) {
	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}

	filter := domain.ListAuditLogsFilter{
		From:           params.From,
		To:             params.To,
		Path:           params.Path,
		PathPrefix:     params.PathPrefix,
		Method:         params.Method,
		HTTPStatus:     params.HttpStatus,
		RequestID:      params.RequestId,
		IdempotencyKey: params.IdempotencyKey,
	}

	logs, next, totalCount, err := s.auditRepo.List(c.Request.Context(), tenantID(c), filter, limit, cursor)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	out := make([]api.AuditLog, len(logs))
	for i, l := range logs {
		out[i] = mapAuditLog(l)
	}

	hasMore := next != ""
	var nextCursor *string
	if hasMore {
		nextCursor = &next
	}

	resp := api.ListAuditLogsResponse{
		Items: out,
		Pagination: api.Pagination{
			NextCursor: nextCursor,
			HasMore:    hasMore,
			TotalCount: totalCount,
		},
	}

	c.JSON(http.StatusOK, resp)
}

func (s *OpenAPIAdapter) GetAuditLog(c *gin.Context, id openapi_types.UUID) {
	l, err := s.auditRepo.Get(c.Request.Context(), tenantID(c), id)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}
	if l == nil {
		respondWithError(c, 0, errors.NotFound("audit log not found", nil))
		return
	}

	c.JSON(http.StatusOK, mapAuditLog(*l))
}

func mapAuditLog(l domain.AuditLog) api.AuditLog {
	actorType := api.AuditLogActorType(l.ActorType)
	return api.AuditLog{
		Id:                l.ID,
		TenantId:          l.TenantID,
		RequestId:         l.RequestID,
		IdempotencyKey:    l.IdempotencyKey,
		ActorSubject:      l.ActorSubject,
		ActorType:         actorType,
		ClientIp:          l.ClientIP,
		UserAgent:         l.UserAgent,
		Method:            l.Method,
		Path:              l.Path,
		QueryParams:       &l.QueryParams,
		RequestHeaders:    &l.RequestHeaders,
		RequestBodySha256: l.RequestBodySHA256,
		RequestSizeBytes:  l.RequestSizeBytes,
		HttpStatus:        l.HTTPStatus,
		ResponseCode:      l.ResponseCode,
		ResponseStatus:    l.ResponseStatus,
		ResponseTimeMs:    l.ResponseTimeMS,
		CreatedAt:         l.CreatedAt,
	}
}

func mapObjectCommon(rec *domain.Object) api.ObjectCommon {
	labels := api.Labels(rec.Labels)
	return api.ObjectCommon{
		ObjectId:    rec.ID,
		ObjectKey:   rec.ObjectKey,
		Bucket:      rec.Bucket,
		ContentType: rec.ContentType,
		SizeBytes:   rec.SizeBytes,
		Status:      mapStatus(rec.Status),
		Category:    rec.Category,
		Labels:      &labels,
		ExternalRef: rec.ExternalRef,
		CreatedAt:   rec.CreatedAt,
		UpdatedAt:   rec.UpdatedAt,
		CompletedAt: rec.CompletedAt,
		DeletedAt:   rec.DeletedAt,
		StoredEtag:  rec.StoredETag,
	}
}

func (s *OpenAPIAdapter) ListCategories(c *gin.Context, params api.ListCategoriesParams) {
	limit := 100
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}

	items, next, totalCount, err := s.catSvc.List(c.Request.Context(), tenantID(c), limit, cursor)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	out := make([]api.Category, len(items))
	for i, item := range items {
		out[i] = mapCategory(item)
	}

	hasMore := next != ""
	var nextCursor *string
	if hasMore {
		nextCursor = &next
	}

	resp := api.ListCategoriesResponse{
		Items: out,
		Pagination: api.Pagination{
			NextCursor: nextCursor,
			HasMore:    hasMore,
			TotalCount: totalCount,
		},
	}

	c.JSON(http.StatusOK, resp)
}

func (s *OpenAPIAdapter) CreateCategory(c *gin.Context) {
	var req api.CreateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithError(c, http.StatusBadRequest, err)
		return
	}

	cat, err := s.catSvc.Create(c.Request.Context(), tenantID(c), req.Slug, req.Name, req.Description)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusCreated, mapCategory(*cat))
}

func (s *OpenAPIAdapter) DeleteCategory(c *gin.Context, slug string) {
	err := s.catSvc.Delete(c.Request.Context(), tenantID(c), slug)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func mapCategory(c domain.Category) api.Category {
	return api.Category{
		Id:          c.ID,
		TenantId:    c.TenantID,
		Slug:        c.Slug,
		Name:        c.Name,
		Description: c.Description,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}
