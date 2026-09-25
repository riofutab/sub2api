//go:build unit

package service

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// unfilteredOpsSink 隐藏 AcceptsLogEntry，让 logger 走改动前的完整编码路径，作为对照。
type unfilteredOpsSink struct {
	inner *OpsSystemLogSink
}

func (s unfilteredOpsSink) WriteLogEvent(event *logger.LogEvent) { s.inner.WriteLogEvent(event) }

type stringerComponent string

func (c stringerComponent) String() string { return string(c) }

func emitSinkFilterCases() {
	base := logger.L().With(zap.String("component", "http"), zap.String("request_id", "req-1"))
	reqLog := base.With(zap.String("component", "handler.gateway.messages"), zap.Int64("user_id", 42))
	auditLog := base.With(zap.String("component", "handler.batch_image.security_audit"), zap.Int64("api_key_id", 7))

	reqLog.Info("sticky.account_selected", zap.Int64("selected_account_id", 1001))
	reqLog.Warn("gateway.select_account_no_available", zap.String("model", "m"))
	reqLog.Error("gateway.forward_failed", zap.String("error", "boom"))
	auditLog.Info("security_audit.gateway_check_start", zap.String("stage", "http"))
	auditLog.Info("security_audit.gateway_check_done", zap.Bool(logger.OpsSystemLogSkipField, true))
	base.Info("http request completed", zap.String("component", "http.access"), zap.Int("status_code", 200))
	base.Warn("http request contains gin errors", zap.String("component", "http.access"), zap.Bool(logger.OpsSystemLogSkipField, true))
	logger.L().Named("audit.worker").Info("named logger without component field")
	logger.L().Named("audit.worker").Info("blank component falls back to name", zap.String("component", "  "))
	logger.L().Info("stringer component", zap.Stringer("component", stringerComponent("svc.audit")))
	logger.L().Info("namespace before component", zap.Namespace("nested"), zap.String("component", "x.audit"))
	auditLog.Info("namespace shadows component", zap.Namespace("nested"), zap.String("component", "plain"))
	logger.LegacyPrintf("service.gateway", "[Forward] Using account: ID=%d", 1001)
	logger.LegacyPrintf("service.audit", "audit event: ID=%d", 1001)
}

func drainSinkQueue(s *OpsSystemLogSink) []*logger.LogEvent {
	var out []*logger.LogEvent
	for {
		select {
		case ev := <-s.queue:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestOpsSystemLogSink_PrefilterKeepsIndexedEventsIdentical(t *testing.T) {
	tmpDir := t.TempDir()
	if err := logger.Init(logger.InitOptions{
		Level:  "debug",
		Format: "json",
		Output: logger.OutputOptions{ToFile: true, FilePath: filepath.Join(tmpDir, "sub2api.log")},
	}); err != nil {
		t.Fatalf("init logger: %v", err)
	}
	t.Cleanup(func() {
		logger.SetSink(nil)
		logger.Sync()
		_ = os.RemoveAll(tmpDir)
	})

	for _, persistAccessLogs := range []bool{false, true} {
		filtered := NewOpsSystemLogSink(nil)
		filtered.SetPersistAccessLogs(persistAccessLogs)
		logger.SetSink(filtered)
		emitSinkFilterCases()
		got := drainSinkQueue(filtered)

		reference := NewOpsSystemLogSink(nil)
		reference.SetPersistAccessLogs(persistAccessLogs)
		logger.SetSink(unfilteredOpsSink{inner: reference})
		emitSinkFilterCases()
		want := drainSinkQueue(reference)

		if len(want) == 0 {
			t.Fatalf("persist=%v: reference sink indexed nothing, test cases are ineffective", persistAccessLogs)
		}
		if len(got) != len(want) {
			t.Fatalf("persist=%v: indexed %d events, want %d", persistAccessLogs, len(got), len(want))
		}
		for i := range want {
			g, w := *got[i], *want[i]
			g.Time = w.Time
			if !reflect.DeepEqual(g, w) {
				t.Fatalf("persist=%v: event %d differs\n got: %+v\nwant: %+v", persistAccessLogs, i, g, w)
			}
		}
	}
}
