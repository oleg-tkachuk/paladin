package httpapi

import (
	"net/http"
	"strconv"

	"paladin/internal/middleware"
	"paladin/internal/service"
	"paladin/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func tenantID(c *gin.Context) string {
	return utils.TenantIDFromContext(c.Request.Context(), "default")
}

func createObjectHandler(svc service.ObjectsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateObjectRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "bad_request", Details: err.Error()})

			return
		}

		id, key, p, err := svc.CreateSingle(c.Request.Context(), tenantID(c), req.ContentType, req.SizeBytes, nil)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "validation_error", Details: err.Error()})

			return
		}

		c.JSON(http.StatusOK, CreateObjectResponse{
			ObjectID:  id.String(),
			ObjectKey: key,
			UploadURL: p.URL,
			Method:    p.Method,
			Headers:   p.Headers,
			ExpiresAt: p.ExpiresAt,
		})
	}
}

func getObjectHandler(svc service.ObjectsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "bad_request", Details: "invalid object id"})

			return
		}

		rec, p, err := svc.Get(c.Request.Context(), tenantID(c), id)
		if err != nil {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "not_found", Details: err.Error()})

			return
		}

		c.JSON(http.StatusOK, GetObjectResponse{
			ObjectID:    id.String(),
			ObjectKey:   rec.ObjectKey,
			Bucket:      rec.Bucket,
			ContentType: rec.ContentType,
			SizeBytes:   rec.SizeBytes,
			Status:      string(rec.Status),
			DownloadURL: p.URL,
			ExpiresAt:   p.ExpiresAt,
		})
	}
}

func completeObjectHandler(svc service.ObjectsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "bad_request", Details: "invalid object id"})

			return
		}

		if err := svc.MarkComplete(c.Request.Context(), tenantID(c), id); err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "internal_error", Details: err.Error()})

			return
		}

		c.JSON(http.StatusOK, CompleteObjectResponse{Status: "active"})
	}
}

func initiateMultipartHandler(svc service.ObjectsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req InitiateMultipartRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "bad_request", Details: err.Error()})

			return
		}

		out, err := svc.InitiateMultipart(c.Request.Context(), tenantID(c), req.ContentType, req.SizeBytes)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "validation_error", Details: err.Error()})

			return
		}

		c.JSON(http.StatusOK, InitiateMultipartResponse{
			ObjectID:  out.ObjectID.String(),
			ObjectKey: out.ObjectKey,
			UploadID:  out.UploadID,
			PartSize:  out.PartSize,
			ExpiresAt: out.ExpiresAt,
		})
	}
}

func signPartHandler(svc service.ObjectsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		uploadID := c.Param("upload_id")

		pn, err := strconv.Atoi(c.Param("part_number"))
		if err != nil || pn <= 0 {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "bad_request", Details: "invalid part number"})

			return
		}

		p, err := svc.SignPart(c.Request.Context(), tenantID(c), uploadID, int32(pn))
		if err != nil {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "not_found", Details: err.Error()})

			return
		}

		c.JSON(http.StatusOK, SignPartResponse{
			UploadURL: p.URL,
			Method:    p.Method,
			ExpiresAt: p.ExpiresAt,
		})
	}
}

func completeMultipartHandler(svc service.ObjectsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		uploadID := c.Param("upload_id")

		var req CompleteMultipartRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "bad_request", Details: err.Error()})

			return
		}

		parts := make([]service.CompletePart, 0, len(req.Parts))
		for _, p := range req.Parts {
			parts = append(parts, service.CompletePart{PartNumber: p.PartNumber, ETag: p.ETag})
		}

		objID, err := svc.CompleteMultipart(c.Request.Context(), tenantID(c), uploadID, parts)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "operation_failed", Details: err.Error()})

			return
		}

		c.JSON(http.StatusOK, CompleteMultipartResponse{ObjectID: objID.String(), Status: "active"})
	}
}

func abortMultipartHandler(svc service.ObjectsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		uploadID := c.Param("upload_id")
		if err := svc.AbortMultipart(c.Request.Context(), tenantID(c), uploadID); err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "operation_failed", Details: err.Error()})

			return
		}

		c.JSON(http.StatusOK, AbortMultipartResponse{Status: "aborted"})
	}
}

// Ensure headers are referenced so linters don't flag them as unused if you later expand.
var _ = middleware.HeaderRequestID
