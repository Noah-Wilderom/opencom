package log

import (
	"io"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// String is a re-export of zap.String so callers don't need to import zap
// directly for the simple case.
var String = zap.String

// Int is a re-export of zap.Int.
var Int = zap.Int

// Error is a re-export of zap.Error.
var Error = zap.Error

// New returns a zap.Logger writing JSON entries to w at the given level.
// Unknown levels default to info and emit a one-time warning so a typo in
// config doesn't fail startup but is still visible.
func New(level string, w io.Writer) *zap.Logger {
	lvl, ok := parseLevel(level)
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		MessageKey:     "msg",
		StacktraceKey:  "stack",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
	}
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(w),
		lvl,
	)
	logger := zap.New(core)
	if !ok {
		logger.Warn("unknown log level, defaulting to info", zap.String("requested", level))
	}
	return logger
}

// parseLevel maps a configured level string to a zapcore.Level. The bool is
// false when the input did not match a known level (caller fell back to info).
func parseLevel(s string) (zapcore.Level, bool) {
	switch strings.ToLower(s) {
	case "debug":
		return zapcore.DebugLevel, true
	case "info":
		return zapcore.InfoLevel, true
	case "warn", "warning":
		return zapcore.WarnLevel, true
	case "error":
		return zapcore.ErrorLevel, true
	default:
		return zapcore.InfoLevel, false
	}
}
