package httpapi

import (
	"net/http"
	"strconv"

	"paladin/internal/logger"
	"paladin/internal/middleware"
	"paladin/internal/service"
	"paladin/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func respondWithError(c *gin.Context, code int, err error) {
	log := logger.FromContext(c.Request.Context())
	if code >= 500 {
		log.Error("Request failed", zap.Error(err), zap.Int("status", code), zap.String("path", c.Request.URL.Path))
	} else if code >= 400 {
		log.Warn("Request failed", zap.Error(err), zap.Int("status", code), zap.String("path", c.Request.URL.Path))
	}
	c.JSON(code, ErrorResponse{
		Error:   http.StatusText(code),
		Details: err.Error(),
		TraceID: utils.RequestIDFromContext(c.Request.Context(), ""),
	})
}

func tenantID(c *gin.Context) string {
	return utils.TenantIDFromContext(c.Request.Context(), "default")
}

func createObjectHandler(svc service.ObjectsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateObjectRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			respondWithError(c, http.StatusBadRequest, err)

			return
		}

		id, key, p, err := svc.CreateSingle(c.Request.Context(), tenantID(c), req.ContentType, req.SizeBytes, nil, req.Labels, req.ExternalRef)
		if err != nil {
			respondWithError(c, http.StatusBadRequest, err)

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
			respondWithError(c, http.StatusBadRequest, err)

			return
		}

		rec, p, err := svc.Get(c.Request.Context(), tenantID(c), id)
		if err != nil {
			respondWithError(c, http.StatusNotFound, err)

			return
		}

		c.JSON(http.StatusOK, GetObjectResponse{
			ObjectID:    id.String(),
			ObjectKey:   rec.ObjectKey,
			Bucket:      rec.Bucket,
			ContentType: rec.ContentType,
			SizeBytes:   rec.SizeBytes,
			Status:      string(rec.Status),
			Labels:      rec.Labels,
			ExternalRef: rec.ExternalRef,
			DownloadURL: p.URL,
			ExpiresAt:   p.ExpiresAt,
		})
	}
}

func completeObjectHandler(svc service.ObjectsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			respondWithError(c, http.StatusBadRequest, err)

			return
		}

		if err := svc.MarkComplete(c.Request.Context(), tenantID(c), id); err != nil {
			respondWithError(c, http.StatusInternalServerError, err)

			return
		}

		c.JSON(http.StatusOK, CompleteObjectResponse{Status: "active"})
	}
}

func deleteObjectHandler(svc service.ObjectsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			respondWithError(c, http.StatusBadRequest, err)

			return
		}

		if err := svc.Delete(c.Request.Context(), tenantID(c), id); err != nil {
			respondWithError(c, http.StatusInternalServerError, err)

			return
		}

		c.JSON(http.StatusOK, DeleteObjectResponse{Status: "deleted"})
	}
}

func initiateMultipartHandler(svc service.ObjectsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req InitiateMultipartRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			respondWithError(c, http.StatusBadRequest, err)

			return
		}

		out, err := svc.InitiateMultipart(c.Request.Context(), tenantID(c), req.ContentType, req.SizeBytes, req.Labels, req.ExternalRef)
		if err != nil {
			respondWithError(c, http.StatusBadRequest, err)

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
			respondWithError(c, http.StatusBadRequest, err)

			return
		}

		p, err := svc.SignPart(c.Request.Context(), tenantID(c), uploadID, int32(pn))
		if err != nil {
			respondWithError(c, http.StatusNotFound, err)

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
			respondWithError(c, http.StatusBadRequest, err)

			return
		}

		parts := make([]service.CompletePart, 0, len(req.Parts))
		for _, p := range req.Parts {
			parts = append(parts, service.CompletePart{PartNumber: p.PartNumber, ETag: p.ETag})
		}

		objID, err := svc.CompleteMultipart(c.Request.Context(), tenantID(c), uploadID, parts)
		if err != nil {
			respondWithError(c, http.StatusBadRequest, err)

			return
		}

		c.JSON(http.StatusOK, CompleteMultipartResponse{ObjectID: objID.String(), Status: "active"})
	}
}

func abortMultipartHandler(svc service.ObjectsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		uploadID := c.Param("upload_id")
		if err := svc.AbortMultipart(c.Request.Context(), tenantID(c), uploadID); err != nil {
			respondWithError(c, http.StatusBadRequest, err)

			return
		}

		c.JSON(http.StatusOK, AbortMultipartResponse{Status: "aborted"})
	}
}

func getObjectMetaHandler(svc service.ObjectsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			respondWithError(c, http.StatusBadRequest, err)
			return
		}

		rec, err := svc.GetMeta(c.Request.Context(), tenantID(c), id)
		if err != nil {
			respondWithError(c, http.StatusNotFound, err)
			return
		}

		c.JSON(http.StatusOK, GetObjectMetaResponse{
			ObjectID:    id.String(),
			ObjectKey:   rec.ObjectKey,
			Bucket:      rec.Bucket,
			ContentType: rec.ContentType,
			SizeBytes:   rec.SizeBytes,
			Status:      string(rec.Status),
			Labels:      rec.Labels,
			ExternalRef: rec.ExternalRef,
			ExpiresAt:   rec.ExpiresAt,
		})
	}
}

// Ensure headers are referenced so linters don't flag them as unused if you later expand.
var _ = middleware.HeaderRequestID
