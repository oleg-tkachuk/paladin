package logger

import (
	"log/slog"

	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"
)

// Slog is l named name as a *slog.Logger, for a library that logs through
// slog (limes, the MCP SDK), so its lines join the service's own under that
// name. zapslog's handler takes a core, which carries no logger name, so it
// is given the name explicitly; built from l.Named(name).Core() alone, every
// line would come out unnamed.
func Slog(l *zap.Logger, name string) *slog.Logger {
	return slog.New(zapslog.NewHandler(l.Core(), zapslog.WithName(l.Named(name).Name())))
}
