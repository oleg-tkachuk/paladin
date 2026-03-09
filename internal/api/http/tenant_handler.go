package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/errors"
)

const (
	defaultTenantListLimit = 20
	maxTenantListLimit     = 100
)

// TenantHandler handles admin tenant lifecycle endpoints.
type TenantHandler struct {
	svc domain.TenantService
}

// NewTenantHandler creates a new TenantHandler.
func NewTenantHandler(svc domain.TenantService) *TenantHandler {
	return &TenantHandler{svc: svc}
}

// createTenantRequest is the JSON body for POST /v1/admin/tenants.
type createTenantRequest struct {
	// TenantID is the canonical identifier used in all tenant-scoped APIs.
	TenantID    string            `json:"tenant_id"    binding:"required"`
	DisplayName *string           `json:"display_name"`
	Labels      map[string]string `json:"labels"`
	Tags        []string          `json:"tags"`
}

// patchTenantMetadataRequest is the JSON body for PATCH /v1/admin/tenants/:tenant_id/metadata.
// Label values may be set to null (JSON null) to remove a key.
type patchTenantMetadataRequest struct {
	// Labels is a partial update map. Provided keys overwrite existing keys.
	// Set a value to JSON null to delete the key.
	Labels map[string]interface{} `json:"labels"`
	// Tags is the full replacement set.
	Tags []string `json:"tags"`
}

// tenantResponse is the JSON body returned by all tenant endpoints.
type tenantResponse struct {
	TenantID    string            `json:"tenant_id"`
	DisplayName *string           `json:"display_name,omitempty"`
	Labels      map[string]string `json:"labels"`
	Tags        []string          `json:"tags"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// listTenantsResponse wraps a paginated list of tenants.
type listTenantsResponse struct {
	Items      []tenantResponse `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
	Total      int64            `json:"total"`
}

func mapTenantToResponse(t *domain.Tenant) tenantResponse {
	labels := t.Labels
	if labels == nil {
		labels = map[string]string{}
	}

	tags := t.Tags
	if tags == nil {
		tags = []string{}
	}

	return tenantResponse{
		TenantID:    t.TenantID,
		DisplayName: t.DisplayName,
		Labels:      labels,
		Tags:        tags,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
	}
}

// CreateTenant handles POST /v1/admin/tenants.
// Idempotent: re-submitting the same tenant_id returns 201 with the updated record.
func (h *TenantHandler) CreateTenant(c *gin.Context) {
	var req createTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(),
			errors.BadRequest("invalid request body: "+err.Error(), err)))

		return
	}

	tenant, err := h.svc.Create(c.Request.Context(), req.TenantID, req.DisplayName, req.Labels, req.Tags)
	if err != nil {
		c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(), err))

		return
	}

	c.JSON(http.StatusCreated, mapTenantToResponse(tenant))
}

// GetTenant handles GET /v1/admin/tenants/:tenant_id.
func (h *TenantHandler) GetTenant(c *gin.Context) {
	tenantID := c.Param("tenant_id")
	if tenantID == "" {
		c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(),
			errors.BadRequest("tenant_id path parameter is required", nil)))

		return
	}

	tenant, err := h.svc.Get(c.Request.Context(), tenantID)
	if err != nil {
		c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(), err))

		return
	}

	c.JSON(http.StatusOK, mapTenantToResponse(tenant))
}

// DeleteTenant handles DELETE /v1/admin/tenants/:tenant_id.
// Returns 409 Conflict when the tenant still has active objects.
func (h *TenantHandler) DeleteTenant(c *gin.Context) {
	tenantID := c.Param("tenant_id")
	if tenantID == "" {
		c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(),
			errors.BadRequest("tenant_id path parameter is required", nil)))

		return
	}

	if err := h.svc.Delete(c.Request.Context(), tenantID); err != nil {
		c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(), err))

		return
	}

	c.Status(http.StatusNoContent)
}

// PatchTenantMetadata handles PATCH /v1/admin/tenants/:tenant_id/metadata.
// Labels are merged (existing keys preserved; send null per key to delete).
// Tags are replaced in full.
func (h *TenantHandler) PatchTenantMetadata(c *gin.Context) {
	tenantID := c.Param("tenant_id")
	if tenantID == "" {
		c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(),
			errors.BadRequest("tenant_id path parameter is required", nil)))

		return
	}

	var req patchTenantMetadataRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(),
			errors.BadRequest("invalid request body: "+err.Error(), err)))

		return
	}

	tenant, err := h.svc.PatchMetadata(c.Request.Context(), tenantID, req.Labels, req.Tags, nil)
	if err != nil {
		c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(), err))

		return
	}

	c.JSON(http.StatusOK, mapTenantToResponse(tenant))
}

// ListTenants handles GET /v1/admin/tenants.
//
// Query parameters:
//   - limit        int     (default 20, max 100)
//   - cursor       string  (opaque page token from previous response)
//   - label_selector  string  (URL-encoded JSON: {"key":"value",...})
//   - tag_selector    string  (repeated; matches tenants with at least one of these tags)
func (h *TenantHandler) ListTenants(c *gin.Context) {
	limit := defaultTenantListLimit
	if lStr := c.Query("limit"); lStr != "" {
		n, err := strconv.Atoi(lStr)
		if err != nil || n <= 0 {
			c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(),
				errors.BadRequest("limit must be a positive integer", nil)))

			return
		}

		if n > maxTenantListLimit {
			n = maxTenantListLimit
		}

		limit = n
	}

	cursor := c.Query("cursor")

	// label_selector is a URL-encoded JSON object, e.g. {"env":"prod"}
	var labelSelector map[string]string
	if raw := c.Query("label_selector"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &labelSelector); err != nil {
			c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(),
				errors.BadRequest("label_selector must be a valid JSON object", err)))

			return
		}
	}

	// tag_selector is a repeated query param: ?tag_selector=enterprise&tag_selector=eu
	tagSelector := c.QueryArray("tag_selector")

	filter := domain.ListTenantsFilter{
		LabelSelector: labelSelector,
		TagSelector:   tagSelector,
	}

	filter.Limit = limit
	filter.Cursor = cursor
	ctx := c.Request.Context()
	tenants, nextCursor, total, err := h.svc.List(ctx, filter)
	if err != nil {
		c.AbortWithStatusJSON(errors.MapToHTTP(c.Request.Context(), err))

		return
	}

	items := make([]tenantResponse, 0, len(tenants))
	for i := range tenants {
		items = append(items, mapTenantToResponse(&tenants[i]))
	}

	c.JSON(http.StatusOK, listTenantsResponse{
		Items:      items,
		NextCursor: nextCursor,
		Total:      total,
	})
}
