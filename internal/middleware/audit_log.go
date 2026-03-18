package middleware

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/safecast"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"go.uber.org/zap"
)

type AuditInfo struct {
	Action   string
	Resource string
}

var mutatingProcedures = map[string]AuditInfo{
	"/paladin.v1.ObjectService/UploadObject":         {"create", "object"},
	"/paladin.v1.ObjectService/DeleteObject":         {"delete", "object"},
	"/paladin.v1.ObjectService/UpdateObjectMetadata": {"update", "object"},
	"/paladin.v1.ObjectService/CopyObject":           {"create", "object"},
	"/paladin.v1.ObjectService/MoveObject":           {"update", "object"},
	"/paladin.v1.ObjectService/CompleteObject":       {"create", "object"},
	"/paladin.v1.ObjectService/RestoreObject":        {"update", "object"},

	"/paladin.v1.BucketService/CreateBucket":              {"create", "bucket"},
	"/paladin.v1.BucketService/DeleteBucket":              {"delete", "bucket"},
	"/paladin.v1.BucketService/UpdateBucketConfiguration": {"update", "bucket"},

	"/paladin.v1.CategoryService/CreateCategory": {"create", "category"},
	"/paladin.v1.CategoryService/UpdateCategory": {"update", "category"},
	"/paladin.v1.CategoryService/DeleteCategory": {"delete", "category"},

	"/paladin.v1.TenantService/CreateTenant":         {"create", "tenant"},
	"/paladin.v1.TenantService/DeleteTenant":         {"delete", "tenant"},
	"/paladin.v1.TenantService/UpdateTenantMetadata": {"update", "tenant"},

	"/paladin.v1.MultipartUploadService/InitiateMultipartUpload": {"create", "multipart_upload"},
	"/paladin.v1.MultipartUploadService/CompleteMultipartUpload": {"create", "multipart_upload"},
	"/paladin.v1.MultipartUploadService/AbortMultipartUpload":    {"delete", "multipart_upload"},

	"/paladin.v1.BulkService/BatchDeleteObjects":  {"delete", "objects"},
	"/paladin.v1.BulkService/BatchCopyObjects":    {"create", "objects"},
	"/paladin.v1.BulkService/BatchRestoreObjects": {"update", "objects"},
}

// Example of how to attach actor and trace ID via context.Context:
//
// 1. Attaching Actor (usually in an AuthInterceptor):
//    ctx = logger.WithActor(ctx, "user-123")
//
// 2. Attaching Trace ID (usually done automatically by OpenTelemetry interceptors):
//    ctx, span := tracer.Start(ctx, "operation")
//    defer span.End()
//    // logger.AuditFromContext(ctx) will intrinsically extract the Trace ID from the span context.

const unknownValue = "unknown"

// ConnectAuditLogInterceptor creates an interceptor that logs mutating RPC requests
// directly to stdout using the centralized audit logger, and saves them to the database.
func ConnectAuditLogInterceptor(auditRepo domain.AuditLogRepository) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			procedure := req.Spec().Procedure

			// If it's a non-mutating request, skip audit logging entirely
			auditInfo, isMutating := mutatingProcedures[procedure]
			if !isMutating {
				return next(ctx, req)
			}

			start := time.Now()

			// Execute handler
			res, err := next(ctx, req)

			duration := safecast.IntFrom64(time.Since(start).Milliseconds())

			responseStatus := "success"
			responseCode := "OK"
			httpStatus := 200
			var connectErrCode *string

			if err != nil {
				responseStatus = "error"
				connectErr := connect.CodeOf(err)
				strCode := connectErr.String()
				responseCode = strCode
				connectErrCode = &strCode
				httpStatus = 500
			}

			resourceID := extractResourceID(req.Any())

			// Base fields specifically required by the specification.
			// log_type, trace_id, and actor are populated intrinsically by AuditFromContext.
			// timestamp is intrinsically populated by zap's TimeEncoder.
			fields := []zap.Field{
				zap.String("action", auditInfo.Action),
				zap.String("resource", auditInfo.Resource),
				zap.String("resource_id", resourceID),
				zap.String("status", responseStatus),
				zap.String("response_code", responseCode),
				zap.Int("http_status", httpStatus),
				zap.Int("duration_ms", duration),
				zap.String("method", procedure),
			}

			iKey := req.Header().Get("Idempotency-Key")
			if iKey != "" {
				fields = append(fields, zap.String("idempotency_key", iKey))
			}
			ua := req.Header().Get("User-Agent")
			if ua != "" {
				fields = append(fields, zap.String("user_agent", ua))
			}

			// Emit structured JSON directly to stdout
			logger.AuditFromContext(ctx).Info("audit event", fields...)

			// Construct and save domain.AuditLog to Postgres
			tenant := utils.TenantIDFromContext(ctx, "")
			rid := utils.RequestIDFromContext(ctx, "")
			id, _ := uuid.NewV7()
			if id == uuid.Nil {
				id = uuid.New()
			}

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
				if allowedHeaders[k] && len(v) > 0 {
					safeHeaders[k] = v[0]
				}
			}

			var reqIdPtr *string
			if rid != "" {
				reqIdPtr = &rid
			}

			var iKeyPtr *string
			if iKey != "" {
				iKeyPtr = &iKey
			}

			var uaPtr *string
			if ua != "" {
				uaPtr = &ua
			}

			go func(ctx context.Context, l domain.AuditLog) {
				_ = auditRepo.Create(ctx, l)
			}(context.WithoutCancel(ctx), domain.AuditLog{
				ID:             id,
				TenantID:       tenant,
				RequestID:      reqIdPtr,
				Method:         "POST", // Connect RPCs are usually POST
				Path:           procedure,
				QueryParams:    map[string]any{},
				RequestHeaders: safeHeaders,
				HTTPStatus:     &httpStatus,
				ResponseCode:   connectErrCode,
				ResponseStatus: &responseStatus,
				ResponseTimeMS: &duration,
				ActorType:      domain.ActorTypeUser,
				UserAgent:      uaPtr,
				CreatedAt:      start,
				LogType:        "audit",
				IdempotencyKey: iKeyPtr,
			})

			return res, err
		}
	})
}

// extractResourceID makes a best-effort attempt to extract a resource identifier
// from standard protobuf request message structures.
func extractResourceID(msg any) string {
	if m, ok := msg.(interface{ GetId() string }); ok {
		return m.GetId()
	}
	if m, ok := msg.(interface{ GetKey() string }); ok {
		return m.GetKey()
	}
	if m, ok := msg.(interface{ GetName() string }); ok {
		return m.GetName()
	}

	return unknownValue
}
