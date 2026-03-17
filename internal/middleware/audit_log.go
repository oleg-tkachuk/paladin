package middleware

import (
	"context"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/safecast"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"go.uber.org/zap"
)

// AuditBatchWriter collects audit log entries and flushes them in batches.
type AuditBatchWriter struct {
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

// NewAuditBatchWriter starts a background goroutine that batches audit writes.
func NewAuditBatchWriter(repo domain.AuditLogRepository, log *zap.Logger) *AuditBatchWriter {
	w := &AuditBatchWriter{
		ch:   make(chan domain.AuditLog, auditChannelSize),
		repo: repo,
		log:  log,
	}
	w.wg.Add(1)
	go w.run()

	return w
}

func (w *AuditBatchWriter) send(entry domain.AuditLog) {
	select {
	case w.ch <- entry:
	default:
		w.log.Warn("audit log channel full, dropping entry")
	}
}

func (w *AuditBatchWriter) Close() {
	close(w.ch)
	w.wg.Wait()
}

func (w *AuditBatchWriter) run() {
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

func (w *AuditBatchWriter) flush(batch []domain.AuditLog) {
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

// ConnectAuditLogInterceptor creates an interceptor that logs RPC requests to the audit writer.
func ConnectAuditLogInterceptor(writer *AuditBatchWriter) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()

			// Filter sensitive headers
			safeHeaders := make(map[string]any)
			allowedHeaders := map[string]bool{
				"content-type":    true,
				"user-agent":      true,
				"accept":          true,
				"x-request-id":    true,
				"x-tenant-id":     true,
				"idempotency-key": true,
			}
			for k, v := range req.Header() {
				if allowedHeaders[strings.ToLower(k)] && len(v) > 0 {
					safeHeaders[k] = v[0]
				}
			}

			// Execute handler
			res, err := next(ctx, req)

			duration := safecast.IntFrom64(time.Since(start).Milliseconds())

			var responseCode *string
			var responseStatus *string
			httpStatus := 200

			if err != nil {
				responseStatus = ptr("error")
				connectErr := connect.CodeOf(err)
				strCode := connectErr.String()
				responseCode = &strCode
				httpStatus = 500 // roughly, or map codes
			} else {
				responseStatus = ptr("success")
			}

			rid := utils.RequestIDFromContext(ctx, "")
			tenant := utils.TenantIDFromContext(ctx, "")

			id, _ := uuid.NewV7()
			if id == uuid.Nil {
				id = uuid.New()
			}

			auditLog := domain.AuditLog{
				ID:             id,
				TenantID:       tenant,
				RequestID:      &rid,
				Method:         "POST", // Connect RPCs are always POST
				Path:           req.Spec().Procedure,
				QueryParams:    map[string]any{},
				RequestHeaders: safeHeaders,
				HTTPStatus:     &httpStatus,
				ResponseCode:   responseCode,
				ResponseStatus: responseStatus,
				ResponseTimeMS: &duration,
				ActorType:      domain.ActorTypeUser,
				ClientIP:       nil, // Not easily available in basic net/http wrapped Connect without extra ctx
				UserAgent:      ptr(req.Header().Get("User-Agent")),
				CreatedAt:      start,
			}

			// Idempotency key
			if iKey := req.Header().Get("Idempotency-Key"); iKey != "" {
				auditLog.IdempotencyKey = &iKey
			}

			writer.send(auditLog)

			return res, err
		}
	})
}

func ptr[T any](v T) *T { return &v }

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
