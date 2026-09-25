//go:build unit

package logger

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func initFileOnlyLogger(t *testing.T) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "logs", "sub2api.log")
	if err := Init(InitOptions{
		Level:           "info",
		Format:          "json",
		StacktraceLevel: "none",
		Output:          OutputOptions{ToFile: true, FilePath: logPath},
	}); err != nil {
		t.Fatalf("init logger: %v", err)
	}
	t.Cleanup(func() {
		// 换回无文件输出的 logger，停掉缓冲协程并释放临时目录里的文件。
		_ = Init(InitOptions{Level: "info", Output: OutputOptions{ToStdout: true}})
	})
	return logPath
}

func readLogFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read log file: %v", err)
	}
	return string(data)
}

func TestFileOutput_InfoBufferedUntilSync(t *testing.T) {
	logPath := initFileOnlyLogger(t)

	L().Info("buffered-info")
	if strings.Contains(readLogFile(t, logPath), "buffered-info") {
		t.Fatalf("info log should stay in buffer before Sync")
	}

	Sync()
	if !strings.Contains(readLogFile(t, logPath), "buffered-info") {
		t.Fatalf("Sync should flush buffered info log to file")
	}
}

func TestFileOutput_ErrorFlushedImmediately(t *testing.T) {
	logPath := initFileOnlyLogger(t)

	L().Info("info-before-error")
	L().With(zap.String("component", "test")).Error("error-flushes-now")

	content := readLogFile(t, logPath)
	if !strings.Contains(content, "error-flushes-now") || !strings.Contains(content, "info-before-error") {
		t.Fatalf("error log should flush buffer immediately, file=%q", content)
	}
}

func TestReconfigure_FlushesPreviousFileBuffer(t *testing.T) {
	logPath := initFileOnlyLogger(t)
	L().Info("before-reconfigure")

	if err := Reconfigure(func(o *InitOptions) error {
		o.Output.FilePath = filepath.Join(filepath.Dir(logPath), "next.log")
		return nil
	}); err != nil {
		t.Fatalf("reconfigure: %v", err)
	}
	if !strings.Contains(readLogFile(t, logPath), "before-reconfigure") {
		t.Fatalf("reconfigure should flush the previous file buffer")
	}
}

type recordingFilterSink struct {
	events []*LogEvent
}

func (s *recordingFilterSink) AcceptsLogEntry(level, component string, skip bool) bool {
	return !skip && (level != "info" || strings.Contains(component, "audit"))
}

func (s *recordingFilterSink) WriteLogEvent(event *LogEvent) { s.events = append(s.events, event) }

func TestSinkCore_SkipsEncodingWhenFilterRejects(t *testing.T) {
	initFileOnlyLogger(t)
	sink := &recordingFilterSink{}
	SetSink(sink)
	t.Cleanup(func() { SetSink(nil) })

	reqLog := L().With(zap.String("component", "handler.gateway.messages"))
	reqLog.Info("rejected-info")
	reqLog.Info("rejected-by-skip", zap.String("component", "x.audit"), zap.Bool(OpsSystemLogSkipField, true))
	reqLog.Info("accepted-audit", zap.String("component", "x.audit"), zap.Int64("user_id", 42))
	reqLog.Warn("accepted-warn", zap.String("model", "m"))
	L().Info("non-string component encodes fully", zap.Int("component", 1))

	var messages []string
	for _, ev := range sink.events {
		messages = append(messages, ev.Message)
	}
	want := []string{"accepted-audit", "accepted-warn", "non-string component encodes fully"}
	if strings.Join(messages, "|") != strings.Join(want, "|") {
		t.Fatalf("delivered messages=%v, want %v", messages, want)
	}
	audit := sink.events[0]
	if audit.Fields["component"] != "x.audit" || audit.Fields["user_id"] != int64(42) || audit.Fields["service"] != "sub2api" {
		t.Fatalf("accepted event fields incomplete: %+v", audit.Fields)
	}
}

func TestStdLogBridge_FlushesFileBuffer(t *testing.T) {
	logPath := initFileOnlyLogger(t)

	L().Info("info-before-stdlog")
	log.Print("stdlog-flushes-now")

	content := readLogFile(t, logPath)
	if !strings.Contains(content, "stdlog-flushes-now") || !strings.Contains(content, "info-before-stdlog") {
		t.Fatalf("stdlog write should flush file buffer (log.Fatal skips deferred Sync), file=%q", content)
	}
}
