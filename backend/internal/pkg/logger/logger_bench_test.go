//go:build unit

package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// countingWriteSyncer 统计落到底层 writer 的 Write 调用次数；
// 对 stdout 与 lumberjack 而言，每次 Write 就是一次 write 系统调用。
type countingWriteSyncer struct {
	inner  zapcore.WriteSyncer
	writes atomic.Int64
}

func (c *countingWriteSyncer) Write(p []byte) (int, error) {
	c.writes.Add(1)
	return c.inner.Write(p)
}

func (c *countingWriteSyncer) Sync() error { return c.inner.Sync() }

// opsLikeSink 复刻 service.OpsSystemLogSink 的过滤规则（logger 包不能 import service）。
type opsLikeSink struct {
	persistAccessLogs bool
	indexed           atomic.Int64
}

func (s *opsLikeSink) AcceptsLogEntry(level, component string, skip bool) bool {
	if skip {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "warn", "warning", "error", "fatal", "panic", "dpanic":
		return true
	}
	component = strings.ToLower(strings.TrimSpace(component))
	if strings.Contains(component, "http.access") {
		return s.persistAccessLogs
	}
	return strings.Contains(component, "audit")
}

func (s *opsLikeSink) WriteLogEvent(event *LogEvent) {
	skip, _ := event.Fields[OpsSystemLogSkipField].(bool)
	component := event.Component
	if fc, _ := event.Fields["component"].(string); strings.TrimSpace(fc) != "" {
		component = fc
	}
	if s.AcceptsLogEntry(event.Level, component, skip) {
		s.indexed.Add(1)
	}
}

type benchOutputs struct {
	stdout *countingWriteSyncer
	stderr *countingWriteSyncer
	file   *countingWriteSyncer
}

// initBenchLogger 按 config.go 的默认日志配置初始化（info、console、caller、stdout+文件），
// stdout/stderr 落到 io.Discard，文件真实写临时目录。
func initBenchLogger(b *testing.B) *benchOutputs {
	b.Helper()
	tmpDir, err := os.MkdirTemp("", "logger-bench-*")
	if err != nil {
		b.Fatalf("create temp dir: %v", err)
	}
	out := &benchOutputs{
		stdout: &countingWriteSyncer{inner: zapcore.AddSync(io.Discard)},
		stderr: &countingWriteSyncer{inner: zapcore.AddSync(io.Discard)},
	}
	origStd, origFile := stdWriteSyncers, wrapFileWriteSyncer
	stdWriteSyncers = func() (zapcore.WriteSyncer, zapcore.WriteSyncer) { return out.stdout, out.stderr }
	wrapFileWriteSyncer = func(ws zapcore.WriteSyncer) zapcore.WriteSyncer {
		out.file = &countingWriteSyncer{inner: ws}
		return out.file
	}
	b.Cleanup(func() {
		Sync()
		stdWriteSyncers, wrapFileWriteSyncer = origStd, origFile
		SetSink(nil)
		_ = os.RemoveAll(tmpDir)
	})

	if err := Init(InitOptions{
		Level:           "info",
		Format:          "console",
		ServiceName:     "sub2api",
		Environment:     "production",
		Caller:          true,
		StacktraceLevel: "error",
		Output: OutputOptions{
			ToStdout: true,
			ToFile:   true,
			FilePath: filepath.Join(tmpDir, "logs", "sub2api.log"),
		},
		Rotation: RotationOptions{MaxSizeMB: 100, MaxBackups: 10, MaxAgeDays: 7, Compress: true, LocalTime: true},
	}); err != nil {
		b.Fatalf("init logger: %v", err)
	}
	if out.file == nil {
		b.Fatalf("file output was not initialized")
	}
	SetSink(&opsLikeSink{})
	return out
}

type benchTLSProfile struct{ Name string }

// newBenchRequestLogger 复刻 RequestLogger 中间件与 handler.requestLogger 的 With 链。
func newBenchRequestLogger() (ctxLog, reqLog *zap.Logger) {
	groupID := int64(3)
	ctxLog = L().With(
		zap.String("component", "http"),
		zap.String("request_id", "6f1c2b1e-8a4d-4f7e-9f60-2d4c6b8e1a90"),
		zap.String("client_request_id", ""),
		zap.String("path", "/v1/messages"),
		zap.String("method", "POST"),
	)
	reqLog = ctxLog.With(
		zap.String("component", "handler.gateway.messages"),
		zap.Int64("user_id", 42),
		zap.Int64("api_key_id", 7),
		zap.Any("group_id", &groupID),
	).With(zap.String("model", "claude-sonnet-4-5"), zap.Bool("stream", true))
	return ctxLog, reqLog
}

// emitGatewayRequestLogsLegacy 是改动前一次 /v1/messages 请求在 info 级别打出的日志调用，
// 调用点、级别与字段按改动前代码逐条构造，保留作对照基线。
func emitGatewayRequestLogsLegacy() {
	groupID := int64(3)
	ctxLog, reqLog := newBenchRequestLogger()

	// handler/security_audit_helper.go logSecurityAuditStart
	reqLog.Info("security_audit.gateway_check_start",
		zap.String("request_id", "6f1c2b1e-8a4d-4f7e-9f60-2d4c6b8e1a90"), zap.Int64("user_id", 42),
		zap.Int64("api_key_id", 7), zap.Int64p("group_id", &groupID),
		zap.String("endpoint", "/v1/messages"), zap.String("provider", "anthropic"),
		zap.String("protocol", "anthropic_messages"), zap.String("model", "claude-sonnet-4-5"), zap.String("stage", "http"),
		zap.Int("body_bytes", 18432), zap.Bool("cached", false))
	// service/content_moderation.go skip_feature_disabled
	slog.Info("content_moderation.skip_feature_disabled",
		"user_id", int64(42), "api_key_id", int64(7), "group_id", int64(3),
		"endpoint", "/v1/messages", "protocol", "anthropic_messages")
	// handler/security_audit_helper.go logSecurityAuditDone
	reqLog.Info("security_audit.gateway_check_done",
		zap.String("request_id", "6f1c2b1e-8a4d-4f7e-9f60-2d4c6b8e1a90"), zap.String("decision", "allow"),
		zap.String("error_code", ""), zap.Bool("allow_next_stage", true),
		zap.String("stage", "http"), zap.Bool("cached", false))
	// service/gateway_service.go GenerateSessionHash
	slog.Info("sticky.hash_source",
		"source", "metadata_user_id", "session_id", "0f5e7c1a-2b3d-4e5f-8a9b-0c1d2e3f4a5b",
		"device_id", "a1b2c3d4e5f6", "is_new_format", true)
	// handler/gateway_handler.go
	reqLog.Info("sticky.session_hash_generated",
		zap.String("session_hash", "0f5e7c1a-2b3d-4e5f-8a9b-0c1d2e3f4a5b"),
		zap.String("metadata_user_id_raw", `{"device_id":"a1b2c3d4e5f6","session_id":"0f5e7c1a-2b3d-4e5f-8a9b-0c1d2e3f4a5b"}`))
	reqLog.Info("sticky.cache_lookup",
		zap.String("session_key", "0f5e7c1a-2b3d-4e5f-8a9b-0c1d2e3f4a5b"), zap.Int64("bound_account_id", 1001))
	reqLog.Info("sticky.selecting_account",
		zap.String("session_key", "0f5e7c1a-2b3d-4e5f-8a9b-0c1d2e3f4a5b"), zap.Int64("sticky_bound_account_id", 1001),
		zap.Bool("has_bound_session", true), zap.Int("failed_account_count", 0))
	// service/gateway_scheduling.go
	slog.Info("sticky.scheduler_entry",
		"group_id", int64(3), "session_hash", "0f5e7c1a", "sticky_account_id", int64(1001),
		"sticky_source", "prefetch", "model", "claude-sonnet-4-5", "load_batch", true,
		"has_concurrency_svc", true, "excluded_count", 0)
	reqLog.Info("sticky.account_selected",
		zap.Int64("selected_account_id", 1001), zap.String("account_name", "claude-max-01"),
		zap.Bool("slot_acquired", true), zap.Bool("has_wait_plan", false),
		zap.Int64("sticky_bound_account_id", 1001), zap.Bool("sticky_honored", true))
	// service/gateway_forward.go
	var tlsProfile *benchTLSProfile
	LegacyPrintf("service.gateway", "[Forward] Using account: ID=%d Name=%s Platform=%s Type=%s TLSFingerprint=%v Proxy=%s",
		int64(1001), "claude-max-01", "anthropic", "oauth", tlsProfile, "")
	// server/middleware/logger.go access log
	accessLog := ctxLog.With(
		zap.String("component", "http.access"),
		zap.Int("status_code", 200),
		zap.Int64("latency_ms", 1834),
		zap.String("client_ip", "203.0.113.42"),
		zap.String("protocol", "HTTP/1.1"),
		zap.String("method", "POST"),
		zap.String("path", "/v1/messages"),
		zap.Int64("account_id", 1001),
		zap.String("platform", "anthropic"),
		zap.String("model", "claude-sonnet-4-5"),
	)
	accessLog.Info("http request completed", zap.Time("completed_at", time.Now()))
}

// emitGatewayRequestLogs 是改动后同一请求的日志调用：sticky 系列、hash_source、
// skip_feature_disabled、Using account 降为 Debug；审计日志去掉重复的 request_id；
// access log 不再 With。
func emitGatewayRequestLogs() {
	groupID := int64(3)
	ctxLog, reqLog := newBenchRequestLogger()

	reqLog.Info("security_audit.gateway_check_start",
		zap.Int64("user_id", 42),
		zap.Int64("api_key_id", 7), zap.Int64p("group_id", &groupID),
		zap.String("endpoint", "/v1/messages"), zap.String("provider", "anthropic"),
		zap.String("protocol", "anthropic_messages"), zap.String("model", "claude-sonnet-4-5"), zap.String("stage", "http"),
		zap.Int("body_bytes", 18432), zap.Bool("cached", false))
	slog.Debug("content_moderation.skip_feature_disabled",
		"user_id", int64(42), "api_key_id", int64(7), "group_id", int64(3),
		"endpoint", "/v1/messages", "protocol", "anthropic_messages")
	reqLog.Info("security_audit.gateway_check_done",
		zap.String("decision", "allow"),
		zap.String("error_code", ""), zap.Bool("allow_next_stage", true),
		zap.String("stage", "http"), zap.Bool("cached", false))
	slog.Debug("sticky.hash_source",
		"source", "metadata_user_id", "session_id", "0f5e7c1a-2b3d-4e5f-8a9b-0c1d2e3f4a5b",
		"device_id", "a1b2c3d4e5f6", "is_new_format", true)
	reqLog.Debug("sticky.session_hash_generated",
		zap.String("session_hash", "0f5e7c1a-2b3d-4e5f-8a9b-0c1d2e3f4a5b"),
		zap.String("metadata_user_id_raw", `{"device_id":"a1b2c3d4e5f6","session_id":"0f5e7c1a-2b3d-4e5f-8a9b-0c1d2e3f4a5b"}`))
	reqLog.Debug("sticky.cache_lookup",
		zap.String("session_key", "0f5e7c1a-2b3d-4e5f-8a9b-0c1d2e3f4a5b"), zap.Int64("bound_account_id", 1001))
	reqLog.Debug("sticky.selecting_account",
		zap.String("session_key", "0f5e7c1a-2b3d-4e5f-8a9b-0c1d2e3f4a5b"), zap.Int64("sticky_bound_account_id", 1001),
		zap.Bool("has_bound_session", true), zap.Int("failed_account_count", 0))
	slog.Debug("sticky.scheduler_entry",
		"group_id", int64(3), "session_hash", "0f5e7c1a", "sticky_account_id", int64(1001),
		"sticky_source", "prefetch", "model", "claude-sonnet-4-5", "load_batch", true,
		"has_concurrency_svc", true, "excluded_count", 0)
	reqLog.Debug("sticky.account_selected",
		zap.Int64("selected_account_id", 1001), zap.String("account_name", "claude-max-01"),
		zap.Bool("slot_acquired", true), zap.Bool("has_wait_plan", false),
		zap.Int64("sticky_bound_account_id", 1001), zap.Bool("sticky_honored", true))
	var tlsProfile *benchTLSProfile
	if ce := L().Check(zap.DebugLevel, "[Forward] Using account"); ce != nil {
		tlsProfileName := ""
		if tlsProfile != nil {
			tlsProfileName = tlsProfile.Name
		}
		ce.Write(zap.String("component", "service.gateway"), zap.Int64("account_id", 1001),
			zap.String("account_name", "claude-max-01"), zap.String("platform", "anthropic"),
			zap.String("account_type", "oauth"), zap.String("tls_profile", tlsProfileName),
			zap.Int64p("proxy_id", nil), zap.Bool("proxy_enabled", false))
	}
	fields := []zap.Field{
		zap.String("component", "http.access"),
		zap.Int("status_code", 200),
		zap.Int64("latency_ms", 1834),
		zap.String("client_ip", "203.0.113.42"),
		zap.String("protocol", "HTTP/1.1"),
		zap.String("method", "POST"),
		zap.String("path", "/v1/messages"),
	}
	fields = append(fields,
		zap.Int64("account_id", 1001),
		zap.String("platform", "anthropic"),
		zap.String("model", "claude-sonnet-4-5"),
	)
	ctxLog.Info("http request completed", append(fields, zap.Time("completed_at", time.Now()))...)
}

func benchmarkGatewayRequestLogs(b *testing.B, emit func()) {
	out := initBenchLogger(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		emit()
	}
	b.StopTimer()
	n := float64(b.N)
	b.ReportMetric(float64(out.stdout.writes.Load()+out.stderr.writes.Load())/n, "std_writes/op")
	b.ReportMetric(float64(out.file.writes.Load())/n, "file_writes/op")
}

func BenchmarkGatewayRequestLogs(b *testing.B) {
	b.Run("legacy_calls", func(b *testing.B) { benchmarkGatewayRequestLogs(b, emitGatewayRequestLogsLegacy) })
	b.Run("current_calls", func(b *testing.B) { benchmarkGatewayRequestLogs(b, emitGatewayRequestLogs) })
}

// BenchmarkSinkCoreWrite 直接测 sinkCore.Write：discarded 为不会被 ops 索引的 info 日志，
// indexed 为会被索引的 audit 组件日志。
func BenchmarkSinkCoreWrite(b *testing.B) {
	groupID := int64(3)
	ctxFields := []zapcore.Field{
		zap.String("service", "sub2api"), zap.String("env", "production"),
		zap.String("component", "http"),
		zap.String("request_id", "6f1c2b1e-8a4d-4f7e-9f60-2d4c6b8e1a90"),
		zap.String("client_request_id", ""),
		zap.String("path", "/v1/messages"), zap.String("method", "POST"),
		zap.Int64("user_id", 42), zap.Int64("api_key_id", 7), zap.Any("group_id", &groupID),
		zap.String("model", "claude-sonnet-4-5"), zap.Bool("stream", true),
	}
	entryFields := []zapcore.Field{
		zap.Int64("selected_account_id", 1001), zap.String("account_name", "claude-max-01"),
		zap.Bool("slot_acquired", true), zap.Bool("has_wait_plan", false),
		zap.Int64("sticky_bound_account_id", 1001), zap.Bool("sticky_honored", true),
	}
	cases := []struct {
		name      string
		component string
	}{
		{name: "discarded", component: "handler.gateway.messages"},
		{name: "indexed", component: "handler.batch_image.security_audit"},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			sink := &opsLikeSink{}
			SetSink(sink)
			b.Cleanup(func() { SetSink(nil) })
			fields := append(append([]zapcore.Field{}, ctxFields...), zap.String("component", tc.component))
			core := newSinkCore().Wrap(zapcore.NewNopCore()).With(fields)
			entry := zapcore.Entry{Level: zapcore.InfoLevel, Time: time.Now(), Message: "sticky.account_selected"}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := core.Write(entry, entryFields); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if tc.name == "indexed" && sink.indexed.Load() != int64(b.N) {
				b.Fatalf("indexed=%d, want %d", sink.indexed.Load(), b.N)
			}
		})
	}
}
