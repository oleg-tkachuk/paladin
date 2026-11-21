package objecth

import (
	"context"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// The RPCs as the outcome log names them.
const (
	opUpload         = "upload"
	opComplete       = "complete"
	opList           = "list"
	opListFolders    = "list folders"
	opCount          = "count"
	opCountFacets    = "count facets"
	opListTags       = "list tags"
	opGet            = "get"
	opLookup         = "lookup"
	opDownload       = "download"
	opUpdate         = "update"
	opDelete         = "delete"
	opRestore        = "restore"
	opCopy           = "copy"
	opSetTaint       = "set taint"
	opSetRetention   = "set retention"
	opSetLegalHold   = "set legal hold"
	opGetLock        = "get lock"
	opListVersions   = "list versions"
	opGetVersion     = "get version"
	opRestoreVersion = "restore version"
)

// The log fields an outcome names what it acted on by.
const (
	fieldCollection = "collection"
	fieldObjectID   = "object_id"
)

// objectFields names the collection and the object an RPC was about, as the
// caller named them.
func objectFields(collection, objectID string) []zap.Field {
	return []zap.Field{zap.String(fieldCollection, collection), zap.String(fieldObjectID, objectID)}
}

// logOutcome records how op ended on l: done at success (info for a change,
// debug for a read), refused at warn, failed at error. fields name what it
// acted on; never a URL, a signed header or an object's contents. A nil l
// logs nowhere.
func logOutcome(ctx context.Context, l *zap.Logger, op string, success zapcore.Level, err error, fields ...zap.Field) {
	if l == nil {
		return
	}
	fields = append(fields, zap.String("op", op))
	if tenant, terr := auth.EffectiveTenant(ctx); terr == nil {
		fields = append(fields, zap.String("tenant_id", tenant.String()))
	}
	switch code := connect.CodeOf(err); {
	case err == nil:
		l.Log(success, "object "+op, fields...)
	case serverFault(code):
		l.Error("object rpc failed", append(fields, zap.Error(err))...)
	default:
		l.Warn("object rpc refused", append(fields, zap.String("code", code.String()), zap.Error(err))...)
	}
}

// serverFault reports whether code is the server's failure rather than a
// refusal of the request.
func serverFault(code connect.Code) bool {
	return code == connect.CodeInternal || code == connect.CodeUnknown || code == connect.CodeUnavailable
}
