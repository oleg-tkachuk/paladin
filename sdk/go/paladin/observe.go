package paladin

import (
	"context"
	"log/slog"
	"time"
)

// Hooks are called as calls are retried and bytes move, for metrics or
// logs of your own. Each is optional; they run on the calling goroutine
// and must not block. Tracing needs none of this: see the README's
// OpenTelemetry section.
type Hooks struct {
	// OnRetry runs before each retry of a failed call.
	OnRetry func(RetryEvent)
	// OnTransfer runs when a presigned upload or download ends.
	OnTransfer func(TransferEvent)
}

// RetryEvent is one retry about to be made.
type RetryEvent struct {
	Procedure string
	// Attempt is the attempt that failed, from 1.
	Attempt int
	// Wait is the pause before the next one.
	Wait time.Duration
	Err  error
}

// TransferEvent is one presigned request that ended.
type TransferEvent struct {
	Method string
	// Host is the host the request went to; the URL's query is the
	// signature and is not kept.
	Host     string
	Bytes    int64
	Duration time.Duration
	// Err is why it failed; nil for a success.
	Err error
}

// WithHooks calls h as the client's calls are retried. For transfers, give
// the Transfer WithTransferHooks.
func WithHooks(h Hooks) Option {
	return func(cfg *config) { cfg.hooks = h }
}

// WithTransferHooks calls h as the Transfer's requests end.
func WithTransferHooks(h Hooks) TransferOption {
	return func(cfg *transferConfig) error {
		cfg.hooks = h
		return nil
	}
}

// WithLogger logs the client's retries to l at debug level, as structured
// records; nil, the default, logs nothing.
func WithLogger(l *slog.Logger) Option {
	return func(cfg *config) { cfg.logger = l }
}

// WithTransferLogger logs the Transfer's requests to l at debug level, and
// their failures at warn.
func WithTransferLogger(l *slog.Logger) TransferOption {
	return func(cfg *transferConfig) error {
		cfg.logger = l
		return nil
	}
}

// WithUserAgentSuffix appends suffix — "gateway/1.4" — to the SDK's
// User-Agent, so the server's logs name the application as well.
func WithUserAgentSuffix(suffix string) Option {
	return func(cfg *config) { cfg.userAgentSuffix = suffix }
}

// Structured log keys.
const (
	logProcedure = "procedure"
	logAttempt   = "attempt"
	logWait      = "wait"
	logError     = "error"
	logMethod    = "method"
	logHost      = "host"
	logBytes     = "bytes"
	logDuration  = "duration"
)

// observer reports what happened to hooks and a logger, either or both nil.
type observer struct {
	hooks  Hooks
	logger *slog.Logger
}

func (o observer) retry(ctx context.Context, e RetryEvent) {
	if o.hooks.OnRetry != nil {
		o.hooks.OnRetry(e)
	}
	if o.logger != nil {
		o.logger.DebugContext(ctx, "paladin: retrying",
			slog.String(logProcedure, e.Procedure), slog.Int(logAttempt, e.Attempt),
			slog.Duration(logWait, e.Wait), slog.Any(logError, e.Err))
	}
}

func (o observer) transfer(ctx context.Context, e TransferEvent) {
	if o.hooks.OnTransfer != nil {
		o.hooks.OnTransfer(e)
	}
	if o.logger == nil {
		return
	}
	attrs := []any{
		slog.String(logMethod, e.Method), slog.String(logHost, e.Host),
		slog.Int64(logBytes, e.Bytes), slog.Duration(logDuration, e.Duration),
	}
	if e.Err != nil {
		o.logger.WarnContext(ctx, "paladin: transfer failed", append(attrs, slog.Any(logError, e.Err))...)
		return
	}
	o.logger.DebugContext(ctx, "paladin: transfer", attrs...)
}
