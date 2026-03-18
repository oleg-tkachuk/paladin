package middleware

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/safecast"
	"go.uber.org/zap"
)

var mutatingProcedures = map[string]struct{}{
	"/paladin.v1.ObjectService/UploadObject":         {},
	"/paladin.v1.ObjectService/DeleteObject":         {},
	"/paladin.v1.ObjectService/UpdateObjectMetadata": {},
	"/paladin.v1.ObjectService/CopyObject":           {},
	"/paladin.v1.ObjectService/MoveObject":           {},
	"/paladin.v1.ObjectService/CompleteObject":       {},
	"/paladin.v1.ObjectService/RestoreObject":        {},

	"/paladin.v1.BucketService/CreateBucket":              {},
	"/paladin.v1.BucketService/DeleteBucket":              {},
	"/paladin.v1.BucketService/UpdateBucketConfiguration": {},

	"/paladin.v1.CategoryService/CreateCategory": {},
	"/paladin.v1.CategoryService/UpdateCategory": {},
	"/paladin.v1.CategoryService/DeleteCategory": {},

	"/paladin.v1.TenantService/CreateTenant":         {},
	"/paladin.v1.TenantService/DeleteTenant":         {},
	"/paladin.v1.TenantService/UpdateTenantMetadata": {},

	"/paladin.v1.MultipartUploadService/InitiateMultipartUpload": {},
	"/paladin.v1.MultipartUploadService/CompleteMultipartUpload": {},
	"/paladin.v1.MultipartUploadService/AbortMultipartUpload":    {},

	"/paladin.v1.BulkService/BatchDeleteObjects":  {},
	"/paladin.v1.BulkService/BatchCopyObjects":    {},
	"/paladin.v1.BulkService/BatchRestoreObjects": {},
}

// isMutatingProcedure determines if a Connect RPC procedure is mutating
// and should generate an audit log.
func isMutatingProcedure(procedure string) bool {
	_, ok := mutatingProcedures[procedure]
	return ok
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

// ConnectAuditLogInterceptor creates an interceptor that logs mutating RPC requests
// directly to stdout using the centralized audit logger.
func ConnectAuditLogInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			procedure := req.Spec().Procedure

			// If it's a non-mutating request, skip audit logging entirely
			if !isMutatingProcedure(procedure) {
				return next(ctx, req)
			}

			// Execute handler
			res, err := next(ctx, req)

			duration := safecast.IntFrom64(time.Since(start).Milliseconds())

			responseStatus := "success"
			responseCode := "OK"
			httpStatus := 200

			if err != nil {
				responseStatus = "error"
				connectErr := connect.CodeOf(err)
				responseCode = connectErr.String()
				httpStatus = 500
			}

			// Parse action and resource from the procedure e.g. /paladin.v1.ObjectService/DeleteObject
			action := "unknown"
			resource := "unknown"
			resourceID := "unknown"

			parts := strings.Split(procedure, "/")
			if len(parts) >= 3 {
				// E.g. parts[2] = "DeleteObject"
				methodName := strings.ToLower(parts[2])
				if strings.HasPrefix(methodName, "create") || strings.HasPrefix(methodName, "upload") || strings.HasPrefix(methodName, "initiate") || strings.HasPrefix(methodName, "complete") {
					action = "create"
				} else if strings.HasPrefix(methodName, "update") {
					action = "update"
				} else if strings.HasPrefix(methodName, "delete") {
					action = "delete"
				} else {
					action = "update" // fallback for other mutating actions
				}

				// E.g. parts[1] = "paladin.v1.ObjectService"
				svcParts := strings.Split(parts[1], ".")
				resource = strings.ToLower(svcParts[len(svcParts)-1])
			}

			// Best-effort extraction of Resource ID from standard request fields
			if msg, ok := req.Any().(interface{ GetId() string }); ok {
				resourceID = msg.GetId()
			} else if msg, ok := req.Any().(interface{ GetKey() string }); ok {
				resourceID = msg.GetKey()
			} else if msg, ok := req.Any().(interface{ GetName() string }); ok {
				resourceID = msg.GetName()
			}

			// Base fields specifically required by the specification.
			// log_type, trace_id, and actor are populated intrinsically by AuditFromContext.
			// timestamp is intrinsically populated by zap's TimeEncoder.
			fields := []zap.Field{
				zap.String("action", action),
				zap.String("resource", resource),
				zap.String("resource_id", resourceID),
				zap.String("status", responseStatus),
				zap.String("response_code", responseCode),
				zap.Int("http_status", httpStatus),
				zap.Int("duration_ms", duration),
				zap.String("method", procedure),
			}

			if iKey := req.Header().Get("Idempotency-Key"); iKey != "" {
				fields = append(fields, zap.String("idempotency_key", iKey))
			}
			if ua := req.Header().Get("User-Agent"); ua != "" {
				fields = append(fields, zap.String("user_agent", ua))
			}

			// Emit structured JSON directly to stdout
			logger.AuditFromContext(ctx).Info("audit event", fields...)

			return res, err
		}
	})
}
