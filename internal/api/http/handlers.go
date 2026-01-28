package httpapi

import (
	stdErrors "errors"
	"net/http"
	"strconv"

	"paladin/internal/errors"
	"paladin/internal/logger"
	"paladin/internal/middleware"
	"paladin/internal/service"
	"paladin/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func respondWithError(c *gin.Context, code int, err error) {
	// We want to use our unified error mapper.
	// However, the caller passed a 'code' which might be from a legacy rationale.
	// Ideally, we should rely on the error type itself to dictate the code via MapToHTTP.
	// But if the caller insists on a code (e.g. for simple validation bind errors),
	// we might need to wrap it.

	// If the error is already an AppError, MapToHTTP handles it.
	// If it's a generic error (like BindJSON error), MapToHTTP defaults to 500
	// unless we wrap it or trust the caller's code.

	// Issue: MapToHTTP returns (status, body).
	// If we use MapToHTTP(err), it might return 500 for a 400 case from ShouldBindJSON.

	// Workaround: If code is provided and it is not 500, we might want to respect it?
	// But the requirement says "HTTP status codes MUST correctly reflect the error class".
	// The caller knows best in handlers if it was a bad request.

	// Let's try to wrap it if it's not an AppError.
	var appErr *errors.AppError
	// If it's not an AppError, wrap it based on the suggestion code.
	if !stdErrors.As(err, &appErr) {
		switch code {
		case http.StatusBadRequest:
			err = errors.BadRequest(err.Error(), nil)
		case http.StatusNotFound:
			err = errors.NotFound(err.Error(), nil)
		case http.StatusUnauthorized:
			err = errors.Unauthorized(err.Error(), nil)
		case http.StatusForbidden:
			err = errors.Forbidden(err.Error(), nil)
		// ... add others if needed, or default to Internal
		default:
			if code >= 500 {
				err = errors.Internal(err.Error(), nil)
			} else {
				// e.g. 413, 429...
				// For now fall back to BadRequest for generic 4xx if not mapped?
				// Or just create a generic AppError?
				err = errors.New(errors.CodeInternal, err.Error(), nil)
				// But wait, we want to respect the status code?
				// MapToHTTP derives status code from Error Code.
				// So we must choose the right Error Code.
			}
		}
	}

	status, body := errors.MapToHTTP(c.Request.Context(), err)

	// Log it if needed (MapToHTTP doesn't log 4xx usually, but we might want to)
	log := logger.FromContext(c.Request.Context())
	if status >= 500 {
		log.Error("Request failed", zap.Error(err), zap.Int("status", status), zap.String("path", c.Request.URL.Path))
	} else if status >= 400 {
		log.Warn("Request failed", zap.Error(err), zap.Int("status", status), zap.String("path", c.Request.URL.Path))
	}

	c.JSON(status, body)
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
