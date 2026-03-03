package httpapi

import (
	"net/http"
	"sync/atomic"

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
	sysSvc    domain.SystemService
	started   *atomic.Bool
	metadata  domain.AppMetadata
}

// NewOpenAPIAdapter creates a new OpenAPIAdapter
func NewOpenAPIAdapter(cfg *config.Config, svc domain.ObjectsService, catSvc domain.CategoryService, auditRepo domain.AuditLogRepository, hs *service.HealthService, sysSvc domain.SystemService, started *atomic.Bool, metadata domain.AppMetadata) *OpenAPIAdapter {
	return &OpenAPIAdapter{
		cfg:       cfg,
		svc:       svc,
		catSvc:    catSvc,
		auditRepo: auditRepo,
		hs:        hs,
		sysSvc:    sysSvc,
		started:   started,
		metadata:  metadata,
	}
}

// Ensure OpenAPIAdapter implements api.ServerInterface
var _ api.ServerInterface = (*OpenAPIAdapter)(nil)

func (s *OpenAPIAdapter) Healthz(c *gin.Context, params api.HealthLivezParams) {
	c.Status(http.StatusOK)
}

func (s *OpenAPIAdapter) HealthLivez(c *gin.Context, params api.HealthLivezParams) {
	c.JSON(http.StatusOK, api.HealthResponse{Status: "alive"})
}

func (s *OpenAPIAdapter) HealthReadyz(c *gin.Context, params api.HealthReadyzParams) {
	s.getHealthReadyz(c)
}

func (s *OpenAPIAdapter) Readyz(c *gin.Context, params api.HealthReadyzParams) {
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

func (s *OpenAPIAdapter) HealthStartupz(c *gin.Context, params api.HealthStartupzParams) {
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

func (s *OpenAPIAdapter) Version(c *gin.Context, params api.VersionParams) {
	c.JSON(http.StatusOK, api.VersionResponse{
		Service:   "paladin",
		Version:   s.metadata.Version,
		GitSha:    &s.metadata.Commit,
		BuildTime: &s.metadata.BuildTime,
	})
}

func (s *OpenAPIAdapter) PingS3(c *gin.Context, params api.PingS3Params) {
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

func (s *OpenAPIAdapter) GetObject(c *gin.Context, id api.ObjectID, params api.GetObjectParams) {
	rec, err := s.svc.Get(c.Request.Context(), tenantID(c), id)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, mapObjectCommon(rec))
}

func (s *OpenAPIAdapter) HeadObject(c *gin.Context, id api.ObjectID, params api.HeadObjectParams) {
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

func (s *OpenAPIAdapter) GetObjectStats(c *gin.Context, params api.GetObjectStatsParams) {
	stats, err := s.svc.GetStats(c.Request.Context(), tenantID(c))
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	resp := api.ObjectStats{
		TotalCount:       int(stats.TotalCount),
		TotalSize:        stats.TotalSize,
		PendingCount:     int(stats.PendingCount),
		UploadingCount:   int(stats.UploadingCount),
		UploadedCount:    int(stats.UploadedCount),
		CompleteCount:    int(stats.CompleteCount),
		SoftDeletedCount: int(stats.SoftDeletedCount),
	}
	c.JSON(http.StatusOK, resp)
}

func (s *OpenAPIAdapter) GetCategoryStats(c *gin.Context, slug string, params api.GetCategoryStatsParams) {
	stats, err := s.catSvc.GetStats(c.Request.Context(), tenantID(c), slug)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.CategoryStats{
		TotalCount: int(stats.TotalCount),
		TotalSize:  stats.TotalSize,
	})
}

func (s *OpenAPIAdapter) CompleteObject(c *gin.Context, id api.ObjectID, params api.CompleteObjectParams) {
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

func (s *OpenAPIAdapter) GetObjectMeta(c *gin.Context, id api.ObjectID, params api.GetObjectMetaParams) {
	rec, err := s.svc.GetMeta(c.Request.Context(), tenantID(c), id)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, mapObjectCommon(rec))
}

func (s *OpenAPIAdapter) PatchObjectMeta(c *gin.Context, id api.ObjectID, params api.PatchObjectMetaParams) {
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
		SortBy:        string(safeDeref(params.Sort, api.CreatedAt)),
		SortOrder:     string(safeDeref(params.Order, api.Desc)),
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

func (s *OpenAPIAdapter) SignObjectDownload(c *gin.Context, id api.ObjectID, params api.SignObjectDownloadParams) {
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

func (s *OpenAPIAdapter) SignObjectUpload(c *gin.Context, id api.ObjectID, params api.SignObjectUploadParams) {
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
		ObjectId:  &out.ObjectID,
		ObjectKey: out.ObjectKey,
		UploadId:  out.UploadID,
		PartSize:  out.PartSize,
		ExpiresAt: out.ExpiresAt,
		Bucket:    out.Bucket,
		Status:    api.Uploading,
	})
}

func (s *OpenAPIAdapter) GetMultipart(c *gin.Context, uploadId api.UploadID, params api.GetMultipartParams) {
	multi, err := s.svc.GetMultipart(c.Request.Context(), tenantID(c), uploadId)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.GetMultipartResponse{
		ObjectId:  &multi.ObjectID,
		ObjectKey: multi.ObjectKey,
		UploadId:  multi.UploadID,
		PartSize:  multi.PartSize,
		Bucket:    multi.Bucket,
		Status:    mapStatus(domain.ObjectStatus(multi.Status)),
		CreatedAt: &multi.CreatedAt,
		UpdatedAt: &multi.UpdatedAt,
	})
}

func (s *OpenAPIAdapter) AbortMultipart(c *gin.Context, uploadId api.UploadID, params api.AbortMultipartParams) {
	if err := s.svc.AbortMultipart(c.Request.Context(), tenantID(c), uploadId); err != nil {
		respondWithError(c, 0, err)
		return
	}

	c.JSON(http.StatusOK, api.AbortMultipartResponse{Status: api.Aborted})
}

func (s *OpenAPIAdapter) CompleteMultipart(c *gin.Context, uploadId api.UploadID, params api.CompleteMultipartParams) {
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

func (s *OpenAPIAdapter) SignPartsBatch(c *gin.Context, uploadId api.UploadID, params api.SignPartsBatchParams) {
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

func (s *OpenAPIAdapter) SignPart(c *gin.Context, uploadId api.UploadID, partNumber api.PartNumber, params api.SignPartParams) {
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

func (s *OpenAPIAdapter) GetAdminConfig(c *gin.Context, params api.GetAdminConfigParams) {
	cfg, err := s.sysSvc.GetConfig(c.Request.Context())
	if err != nil {
		respondWithError(c, http.StatusInternalServerError, err)
		return
	}

	// Redact sensitive fields
	resp := api.ConfigResponse{
		App: &struct {
			Env  *string `json:"env,omitempty"`
			Name *string `json:"name,omitempty"`
		}{
			Name: ptr(cfg.App.Name),
			Env:  ptr(cfg.App.Env),
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
			Mode: ptr(cfg.Server.Mode),
			Name: ptr(cfg.Server.Name),
			Http: &struct {
				Addr               *string   `json:"addr,omitempty"`
				CorsAllowedOrigins *[]string `json:"cors_allowed_origins,omitempty"`
				ReadTimeout        *string   `json:"read_timeout,omitempty"`
				RequestIdHeader    *string   `json:"request_id_header,omitempty"`
				WriteTimeout       *string   `json:"write_timeout,omitempty"`
			}{
				Addr:               ptr(cfg.Server.HTTP.Addr),
				ReadTimeout:        ptr(cfg.Server.HTTP.ReadTimeout),
				WriteTimeout:       ptr(cfg.Server.HTTP.WriteTimeout),
				RequestIdHeader:    ptr(cfg.Server.HTTP.RequestIDHeader),
				CorsAllowedOrigins: &cfg.Server.HTTP.CORSAllowedOrigins,
			},
		},
		Datastores: &struct {
			Postgres *struct {
				Dbname  *string `json:"dbname,omitempty"`
				Host    *string `json:"host,omitempty"`
				Port    *string `json:"port,omitempty"`
				SslMode *string `json:"ssl_mode,omitempty"`
				User    *string `json:"user,omitempty"`
			} `json:"postgres,omitempty"`
			S3 *struct {
				Bucket         *string `json:"bucket,omitempty"`
				Endpoint       *string `json:"endpoint,omitempty"`
				ForcePathStyle *bool   `json:"force_path_style,omitempty"`
				PartSize       *string `json:"part_size,omitempty"`
				PresignTtl     *string `json:"presign_ttl,omitempty"`
				PublicEndpoint *string `json:"public_endpoint,omitempty"`
				SseType        *string `json:"sse_type,omitempty"`
			} `json:"s3,omitempty"`
		}{
			Postgres: &struct {
				Dbname  *string `json:"dbname,omitempty"`
				Host    *string `json:"host,omitempty"`
				Port    *string `json:"port,omitempty"`
				SslMode *string `json:"ssl_mode,omitempty"`
				User    *string `json:"user,omitempty"`
			}{
				Dbname:  ptr(cfg.Datastores.Postgres.Dbname),
				Host:    ptr(cfg.Datastores.Postgres.Host),
				Port:    ptr(cfg.Datastores.Postgres.Port),
				SslMode: ptr(cfg.Datastores.Postgres.SslMode),
				User:    ptr(cfg.Datastores.Postgres.User),
			},
			S3: &struct {
				Bucket         *string `json:"bucket,omitempty"`
				Endpoint       *string `json:"endpoint,omitempty"`
				ForcePathStyle *bool   `json:"force_path_style,omitempty"`
				PartSize       *string `json:"part_size,omitempty"`
				PresignTtl     *string `json:"presign_ttl,omitempty"`
				PublicEndpoint *string `json:"public_endpoint,omitempty"`
				SseType        *string `json:"sse_type,omitempty"`
			}{
				Bucket:         ptr(cfg.Datastores.S3.Bucket),
				Endpoint:       ptr(cfg.Datastores.S3.Endpoint),
				PublicEndpoint: ptr(cfg.Datastores.S3.PublicEndpoint),
				ForcePathStyle: ptr(cfg.Datastores.S3.ForcePathStyle),
				PresignTtl:     ptr(cfg.Datastores.S3.PresignTTL),
				PartSize:       ptr(cfg.Datastores.S3.PartSize),
				SseType:        ptr(cfg.Datastores.S3.SSEType),
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
			MaxObjectSize:       ptr(cfg.Policy.MaxObjectSize),
			MaxMultipartSize:    ptr(cfg.Policy.MaxMultipartSize),
			MinPartSize:         ptr(cfg.Policy.MinPartSize),
			MaxPartSize:         ptr(cfg.Policy.MaxPartSize),
			PresignPutTtl:       ptr(cfg.Policy.PresignPutTTL),
			PresignGetTtl:       ptr(cfg.Policy.PresignGetTTL),
			AllowedContentTypes: &cfg.Policy.AllowedContentTypes,
		},
		Security: &struct {
			EnableRls                *bool `json:"enable_rls,omitempty"`
			RejectTenantMismatch     *bool `json:"reject_tenant_mismatch,omitempty"`
			TrustTenantIdFromRequest *bool `json:"trust_tenant_id_from_request,omitempty"`
		}{
			TrustTenantIdFromRequest: ptr(cfg.Security.TrustTenantIDFromRequest),
			RejectTenantMismatch:     ptr(cfg.Security.RejectTenantMismatch),
			EnableRls:                ptr(cfg.Security.EnableRLS),
		},
		Housekeeping: &struct {
			EnableReaper *bool   `json:"enable_reaper,omitempty"`
			GcInterval   *string `json:"gc_interval,omitempty"`
			MultipartTtl *string `json:"multipart_ttl,omitempty"`
			PendingTtl   *string `json:"pending_ttl,omitempty"`
		}{
			EnableReaper: ptr(cfg.Housekeeping.EnableReaper),
			PendingTtl:   ptr(cfg.Housekeeping.PendingTTL),
			MultipartTtl: ptr(cfg.Housekeeping.MultipartTTL),
			GcInterval:   ptr(cfg.Housekeeping.GCInterval),
		},
		RateLimit: &struct {
			Burst             *int     `json:"burst,omitempty"`
			MaxTenants        *int     `json:"max_tenants,omitempty"`
			RequestsPerSecond *float32 `json:"requests_per_second,omitempty"`
		}{
			RequestsPerSecond: ptr(cfg.RateLimit.RequestsPerSecond),
			Burst:             ptr(cfg.RateLimit.Burst),
			MaxTenants:        ptr(cfg.RateLimit.MaxTenants),
		},
		Cache: &struct {
			Enabled *bool   `json:"enabled,omitempty"`
			MaxSize *int    `json:"max_size,omitempty"`
			Ttl     *string `json:"ttl,omitempty"`
		}{
			Enabled: ptr(cfg.Cache.Enabled),
			MaxSize: ptr(cfg.Cache.MaxSize),
			Ttl:     ptr(cfg.Cache.TTL),
		},
		Timeouts: &struct {
			DefaultOperation *string `json:"default_operation,omitempty"`
			FastOperation    *string `json:"fast_operation,omitempty"`
			LongOperation    *string `json:"long_operation,omitempty"`
			S3Operation      *string `json:"s3_operation,omitempty"`
		}{
			FastOperation:    ptr(cfg.Timeouts.FastOperation),
			DefaultOperation: ptr(cfg.Timeouts.DefaultOperation),
			S3Operation:      ptr(cfg.Timeouts.S3Operation),
			LongOperation:    ptr(cfg.Timeouts.LongOperation),
		},
		Idempotency: &struct {
			Enabled *bool   `json:"enabled,omitempty"`
			Ttl     *string `json:"ttl,omitempty"`
		}{
			Enabled: ptr(cfg.Idempotency.Enabled),
			Ttl:     ptr(cfg.Idempotency.TTL),
		},
		Otel: &struct {
			Enabled  *bool   `json:"enabled,omitempty"`
			Endpoint *string `json:"endpoint,omitempty"`
			Insecure *bool   `json:"insecure,omitempty"`
			Protocol *string `json:"protocol,omitempty"`
		}{
			Enabled:  ptr(cfg.OTel.Enabled),
			Endpoint: ptr(cfg.OTel.Endpoint),
			Protocol: ptr(cfg.OTel.Protocol),
			Insecure: ptr(cfg.OTel.Insecure),
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

func (s *OpenAPIAdapter) GetAuditLog(c *gin.Context, id openapi_types.UUID, params api.GetAuditLogParams) {
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
		CreatedAt:         &l.CreatedAt,
	}
}

func mapObjectCommon(o *domain.Object) api.ObjectCommon {
	return api.ObjectCommon{
		ObjectId:        &o.ID,
		ObjectKey:       o.ObjectKey,
		Bucket:          o.Bucket,
		ContentType:     o.ContentType,
		SizeBytes:       o.SizeBytes,
		Status:          mapStatus(o.Status),
		Category:        o.Category,
		Labels:          (*api.Labels)(&o.Labels),
		ExternalRef:     o.ExternalRef,
		StoredEtag:      o.StoredETag,
		StoredSizeBytes: o.StoredSizeBytes,
		CreatedAt:       &o.CreatedAt,
		UpdatedAt:       &o.UpdatedAt,
		CompletedAt:     o.CompletedAt,
		DeletedAt:       o.DeletedAt,
	}
}

func safeDeref[T any](ptr *T, def T) T {
	if ptr == nil {
		return def
	}
	return *ptr
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

func (s *OpenAPIAdapter) CreateCategory(c *gin.Context, params api.CreateCategoryParams) {
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

func (s *OpenAPIAdapter) DeleteCategory(c *gin.Context, slug string, params api.DeleteCategoryParams) {
	err := s.catSvc.Delete(c.Request.Context(), tenantID(c), slug)
	if err != nil {
		respondWithError(c, 0, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func mapCategory(c domain.Category) api.Category {
	return api.Category{
		Id:          &c.ID,
		TenantId:    c.TenantID,
		Slug:        c.Slug,
		Name:        c.Name,
		Description: c.Description,
		CreatedAt:   &c.CreatedAt,
		UpdatedAt:   &c.UpdatedAt,
	}
}
