package utils

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var Logger *zap.Logger

var (
	querySecretPattern = regexp.MustCompile(`(?i)((?:signature|token|api[_-]?hash|api[_-]?key|secret|signing[_-]?key|authorization|session)=)[^&\s"'<>]+`)
	bearerPattern      = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	botTokenPattern    = regexp.MustCompile(`\b[0-9]{5,}:[A-Za-z0-9_-]{20,}\b`)
)

const redactedValue = "[REDACTED]"

type redactingCore struct {
	zapcore.Core
}

func (c redactingCore) With(fields []zapcore.Field) zapcore.Core {
	return redactingCore{Core: c.Core.With(redactFields(fields))}
}

func (c redactingCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if !c.Enabled(entry.Level) {
		return checked
	}
	return checked.AddCore(entry, c)
}

func (c redactingCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	entry.Message = RedactSensitiveText(entry.Message)
	return c.Core.Write(entry, redactFields(fields))
}

func RedactSensitiveText(value string) string {
	value = querySecretPattern.ReplaceAllString(value, `${1}`+redactedValue)
	value = bearerPattern.ReplaceAllString(value, "Bearer "+redactedValue)
	return botTokenPattern.ReplaceAllString(value, redactedValue)
}

func redactFields(fields []zapcore.Field) []zapcore.Field {
	redacted := make([]zapcore.Field, len(fields))
	for i, field := range fields {
		redacted[i] = redactField(field)
	}
	return redacted
}

func redactField(field zapcore.Field) zapcore.Field {
	key := strings.ToLower(field.Key)
	if isSecretField(key) {
		return zap.String(field.Key, redactedValue)
	}

	switch field.Type {
	case zapcore.StringType:
		field.String = RedactSensitiveText(field.String)
	case zapcore.ByteStringType:
		if value, ok := field.Interface.([]byte); ok {
			field.Interface = []byte(RedactSensitiveText(string(value)))
		}
	case zapcore.ErrorType:
		if err, ok := field.Interface.(error); ok {
			field.Interface = errors.New(RedactSensitiveText(err.Error()))
		}
	}
	return field
}

func isSecretField(key string) bool {
	sensitiveParts := []string{
		"signature",
		"token",
		"secret",
		"password",
		"authorization",
		"api_hash",
		"api-hash",
		"api_key",
		"api-key",
		"signing_key",
		"signing-key",
		"session",
	}
	for _, part := range sensitiveParts {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}

func InitLogger(debugMode bool) {
	customTimeEncoder := func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		layout := "02/01/2006 03:04:05 PM"
		if debugMode {
			layout = "02/01/2006 03:04:05.000 PM"
		}
		enc.AppendString(t.Format(layout))
	}
	consoleConfig := zap.NewDevelopmentEncoderConfig()
	consoleConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	consoleConfig.EncodeTime = customTimeEncoder
	consoleEncoder := zapcore.NewConsoleEncoder(consoleConfig)

	fileEncoderConfig := zap.NewProductionEncoderConfig()
	fileEncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	fileEncoder := zapcore.NewJSONEncoder(fileEncoderConfig)

	fileWriter := zapcore.AddSync(&lumberjack.Logger{
		Filename:   "logs/app.log",
		MaxSize:    10,
		MaxBackups: 3,
		MaxAge:     7,
		Compress:   true,
	})

	var consoleLevel zapcore.Level
	if debugMode {
		consoleLevel = zapcore.DebugLevel
	} else {
		consoleLevel = zapcore.InfoLevel
	}

	core := redactingCore{Core: zapcore.NewTee(
		zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), consoleLevel),
		zapcore.NewCore(fileEncoder, fileWriter, zapcore.DebugLevel),
	)}

	Logger = zap.New(core, zap.AddStacktrace(zapcore.FatalLevel))
}
