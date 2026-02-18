package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"go.uber.org/zap"
)

// auditBatchWriter collects audit log entries and flushes them in batches.
type auditBatchWriter struct {
	ch   chan domain.AuditLog
	repo domain.AuditLogRepository
	log  *zap.Logger
	wg   sync.WaitGroup
}

const (
	auditChannelSize = 4096
	auditBatchSize   = 100
	auditFlushDelay  = 500 * time.Millisecond
)

// newAuditBatchWriter starts a background goroutine that batches audit writes.
func newAuditBatchWriter(repo domain.AuditLogRepository, log *zap.Logger) *auditBatchWriter {
	w := &auditBatchWriter{
		ch:   make(chan domain.AuditLog, auditChannelSize),
		repo: repo,
		log:  log,
	}
	w.wg.Add(1)
	go w.run()
	return w
}

func (w *auditBatchWriter) send(entry domain.AuditLog) {
	select {
	case w.ch <- entry:
	default:
		w.log.Warn("audit log channel full, dropping entry")
	}
}

func (w *auditBatchWriter) run() {
	defer w.wg.Done()
	batch := make([]domain.AuditLog, 0, auditBatchSize)
	timer := time.NewTimer(auditFlushDelay)
	defer timer.Stop()

	for {
		select {
		case entry, ok := <-w.ch:
			if !ok {
				// Channel closed, flush remaining
				if len(batch) > 0 {
					w.flush(batch)
				}
				return
			}
			batch = append(batch, entry)
			if len(batch) >= auditBatchSize {
				w.flush(batch)
				batch = batch[:0]
				timer.Reset(auditFlushDelay)
			}
		case <-timer.C:
			if len(batch) > 0 {
				w.flush(batch)
				batch = batch[:0]
			}
			timer.Reset(auditFlushDelay)
		}
	}
}

func (w *auditBatchWriter) flush(batch []domain.AuditLog) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, entry := range batch {
		if err := w.repo.Create(ctx, entry); err != nil {
			w.log.Warn("failed to save audit log",
				zap.Error(err),
				zap.String("request_id", ptrStr(entry.RequestID)),
			)
		}
	}
}

func (w *auditBatchWriter) stop() {
	close(w.ch)
	w.wg.Wait()
}

// errorBodyWriter captures response body only for error responses (status >= 400).
type errorBodyWriter struct {
	gin.ResponseWriter
	body       *bytes.Buffer
	statusCode int
}

func (w *errorBodyWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *errorBodyWriter) Write(b []byte) (int, error) {
	// Only capture body for error responses
	if w.statusCode >= 400 || w.statusCode == 0 {
		w.body.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// AuditLogMiddleware creates audit logging middleware with batched writes and
// streaming body hashing (no pre-read delay).
func AuditLogMiddleware(repo domain.AuditLogRepository, log *zap.Logger) gin.HandlerFunc {
	writer := newAuditBatchWriter(repo, log)

	return func(c *gin.Context) {
		start := time.Now()

		// 1. Stream-hash request body with TeeReader (no pre-read copy)
		var bodySize *int64
		var bodyHash hash.Hash

		if c.Request.Body != nil && c.Request.ContentLength > 0 {
			size := c.Request.ContentLength
			bodySize = &size

			bodyHash = sha256.New()
			c.Request.Body = io.NopCloser(io.TeeReader(c.Request.Body, bodyHash))
		}

		// Filter sensitive headers
		safeHeaders := make(map[string]any, 6) // pre-size for known headers
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

		// Use error-only body writer (don't capture success response bodies)
		ebw := &errorBodyWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
		c.Writer = ebw

		c.Next()

		// 2. Capture response metadata
		duration := int(time.Since(start).Milliseconds())
		status := c.Writer.Status()

		// Compute body SHA after handler has consumed the body
		var bodySHA *string
		if bodyHash != nil {
			h := hex.EncodeToString(bodyHash.Sum(nil))
			bodySHA = &h
		}

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
			if err := json.Unmarshal(ebw.body.Bytes(), &errorBody); err == nil && errorBody.Error.Code != "" {
				responseCode = &errorBody.Error.Code
			}
		} else {
			responseStatus = ptr("success")
		}

		// 3. Assemble Audit Log
		rid := utils.RequestIDFromContext(c.Request.Context(), "")
		tenant := utils.TenantIDFromContext(c.Request.Context(), "")

		actorType := domain.ActorTypeUser

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
			QueryParams:       make(map[string]any, len(c.Request.URL.Query())),
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

		// 4. Send to batch writer (non-blocking)
		writer.send(auditLog)
	}
}

func ptr[T any](v T) *T { return &v }

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
