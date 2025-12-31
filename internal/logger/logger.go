package logger

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New creates a zap.Logger with sampling and environment fields.
func New(level, format string, baseFields map[string]string) (*zap.Logger, error) {
	var lvl zapcore.Level

	switch strings.ToLower(level) {
	case "debug":
		lvl = zapcore.DebugLevel
	case "info":
		lvl = zapcore.InfoLevel
	case "warn", "warning":
		lvl = zapcore.WarnLevel
	case "error":
		lvl = zapcore.ErrorLevel
	default:
		return nil, fmt.Errorf("unsupported log level: %s", level)
	}

	var enc string

	switch strings.ToLower(format) {
	case "json", "":
		enc = "json"
	case "console":
		enc = "console"
	default:
		return nil, fmt.Errorf("unsupported log format: %s", format)
	}

	cfg := zap.Config{
		Level:            zap.NewAtomicLevelAt(lvl),
		Development:      false,
		Encoding:         enc,
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "time",
			LevelKey:       "level",
			NameKey:        "logger",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
			LineEnding:     zapcore.DefaultLineEnding,
		},
		Sampling: &zap.SamplingConfig{
			Initial:    100,
			Thereafter: 100,
		},
	}

	log, err := cfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		return nil, err
	}

	// Attach environment fields
	fields := []zap.Field{
		zap.String("service", baseFields["service"]),
		zap.String("pod", baseFields["pod"]),
		zap.String("env", baseFields["env"]),
		zap.String("version", baseFields["version"]),
		zap.String("commit", baseFields["commit"]),
		zap.String("build_time", baseFields["build_time"]),
	}

	// Optional: include additional env fields without leaking secrets
	if v := os.Getenv("K8S_NAMESPACE"); v != "" {
		fields = append(fields, zap.String("k8s_namespace", v))
	}

	return log.With(fields...), nil
}

// NewBootstrapLogger returns a simple logger for early startup.
func NewBootstrapLogger() *zap.Logger {
	l, _ := zap.NewDevelopment()

	return l
}
