package logger

import (
	"context"
	"log"
	"net"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// HTTPServerErrorLog adapts l for http.Server.ErrorLog at warn level, tagged
// with the listener the message came from: TLS handshake failures, malformed
// requests, a handler's panic — anything the handler never answers.
//
// Without it they go to Go's default logger, raw lines on stderr with the
// client's address but not the listener they arrived on. NewStdLogAt fails
// only on an unknown level, a constant here; nil falls back to the stdlib
// default, which loses the tag and nothing else.
func HTTPServerErrorLog(l *zap.Logger, addr string) *log.Logger {
	std, err := zap.NewStdLogAt(
		l.Named("http").With(zap.String("listen_addr", addr)),
		zapcore.WarnLevel,
	)
	if err != nil {
		return nil
	}
	return std
}

// HTTPBaseContext is an http.Server.BaseContext that puts l on every
// request's context, where FromContext finds it.
func HTTPBaseContext(l *zap.Logger) func(net.Listener) context.Context {
	return func(net.Listener) context.Context { return WithContext(context.Background(), l) }
}
