package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"go.uber.org/zap"
)

type bodyLogWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w bodyLogWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

const maxBodySize = 1024 * 1024 // 1MB

func AuditLogMiddleware(repo domain.AuditLogRepository, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// 1. Capture request metadata
		var bodySHA *string
		var bodySize *int64

		if c.Request.Body != nil && c.Request.ContentLength > 0 {
			size := c.Request.ContentLength
			bodySize = &size

			if size <= maxBodySize {
				bodyBytes, _ := io.ReadAll(c.Request.Body)
				c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

				hash := sha256.Sum256(bodyBytes)
				h := hex.EncodeToString(hash[:])
				bodySHA = &h
			}
		}

		// Filter sensitive headers
		safeHeaders := make(map[string]any)
		allowedHeaders := map[string]bool{
			"Content-Type":    true,
			"User-Agent":      true,
			"Accept":          true,
			"X-Request-ID":    true,
			"X-Tenant-ID":     true,
			"Idempotency-Key": true,
		}
		for k, v := range c.Request.Header {
			if allowedHeaders[k] {
				safeHeaders[k] = v
			}
		}

		// Use custom writer to capture response body for error mapping
		blw := &bodyLogWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
		c.Writer = blw

		c.Next()

		// 2. Capture response metadata
		duration := int(time.Since(start).Milliseconds())
		status := c.Writer.Status()

		var responseCode *string
		var responseStatus *string

		if status >= 400 {
			responseStatus = ptr("error")
			// Try to parse response body to find internal error code
			var errorBody struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(blw.body.Bytes(), &errorBody); err == nil && errorBody.Error.Code != "" {
				responseCode = &errorBody.Error.Code
			}
		} else {
			responseStatus = ptr("success")
		}

		// 3. Assemble Audit Log
		rid := utils.RequestIDFromContext(c.Request.Context(), "")
		tenant := utils.TenantIDFromContext(c.Request.Context(), "")

		actorType := domain.ActorTypeUser
		// In a real system, we might extract this from identity context
		// For now, we use user as default.

		id, _ := uuid.NewV7()
		if id == uuid.Nil {
			id = uuid.New()
		}

		auditLog := domain.AuditLog{
			ID:                id,
			TenantID:          tenant,
			RequestID:         &rid,
			Method:            c.Request.Method,
			Path:              c.Request.URL.Path,
			QueryParams:       map[string]any{}, // Can be enriched from c.Request.URL.Query()
			RequestHeaders:    safeHeaders,
			RequestBodySHA256: bodySHA,
			RequestSizeBytes:  bodySize,
			HTTPStatus:        &status,
			ResponseCode:      responseCode,
			ResponseStatus:    responseStatus,
			ResponseTimeMS:    &duration,
			ActorType:         actorType,
			ClientIP:          ptr(c.ClientIP()),
			UserAgent:         ptr(c.Request.UserAgent()),
			CreatedAt:         start,
		}

		// Enrich query params
		for k, v := range c.Request.URL.Query() {
			auditLog.QueryParams[k] = v
		}

		// Idempotency key
		if iKey := c.GetHeader("Idempotency-Key"); iKey != "" {
			auditLog.IdempotencyKey = &iKey
		}

		// 4. Save Audit Log (Async or fire-and-forget)
		go func(l domain.AuditLog) {
			// Create a new context for base background operation
			if err := repo.Create(context.Background(), l); err != nil {
				log.Warn("failed to save audit log", zap.Error(err), zap.String("request_id", rid))
			}
		}(auditLog)
	}
}

func ptr[T any](v T) *T { return &v }
