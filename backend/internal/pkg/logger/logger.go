package logger

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Level = zapcore.Level

const (
	// 文件输出缓冲：攒满 fileBufferSize 或每 fileFlushInterval 刷一次盘，
	// Error 及以上级别写完立即刷（见 syncOnErrorCore）。
	fileBufferSize    = 64 * 1024
	fileFlushInterval = time.Second
)

const (
	LevelDebug = zapcore.DebugLevel
	LevelInfo  = zapcore.InfoLevel
	LevelWarn  = zapcore.WarnLevel
	LevelError = zapcore.ErrorLevel
	LevelFatal = zapcore.FatalLevel

	// OpsSystemLogSkipField keeps an event in the standard logger while
	// preventing the database-backed Ops system-log sink from indexing it.
	OpsSystemLogSkipField = "ops_system_log_skip"
)

type Sink interface {
	WriteLogEvent(event *LogEvent)
}

// SinkFilter 可由 Sink 选择实现。sinkCore 在编码字段之前用它判断事件是否需要投递，
// 返回 false 时整条事件不再编码、不再投递。实现必须与 WriteLogEvent 内部的过滤结论一致：
// level 为小写级别名，component 为非空的 "component" 字段值（否则为 LoggerName），
// skip 为 OpsSystemLogSkipField 字段值。
type SinkFilter interface {
	AcceptsLogEntry(level, component string, skip bool) bool
}

type LogEvent struct {
	Time       time.Time
	Level      string
	Component  string
	Message    string
	LoggerName string
	Fields     map[string]any
}

var (
	mu            sync.RWMutex
	fileBuffer    atomic.Pointer[zapcore.BufferedWriteSyncer] // 当前文件输出缓冲，未开文件输出时为 nil
	global        atomic.Pointer[zap.Logger]
	sugar         atomic.Pointer[zap.SugaredLogger]
	atomicLevel   zap.AtomicLevel
	initOptions   InitOptions
	currentSink   atomic.Value // sinkState
	stdLogUndo    func()
	bootstrapOnce sync.Once
)

// 标准输出与文件输出的底层 WriteSyncer 构造点。生产路径保持默认值，
// 基准测试替换它们以统计真正落到 fd 上的 Write 次数。
var (
	stdWriteSyncers = func() (stdout, stderr zapcore.WriteSyncer) {
		return zapcore.Lock(os.Stdout), zapcore.Lock(os.Stderr)
	}
	wrapFileWriteSyncer = func(ws zapcore.WriteSyncer) zapcore.WriteSyncer { return ws }
)

type sinkState struct {
	sink Sink
}

func InitBootstrap() {
	bootstrapOnce.Do(func() {
		if err := Init(bootstrapOptions()); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "logger bootstrap init failed: %v\n", err)
		}
	})
}

func Init(options InitOptions) error {
	mu.Lock()
	defer mu.Unlock()
	return initLocked(options)
}

func initLocked(options InitOptions) error {
	normalized := options.normalized()
	zl, al, buf, err := buildLogger(normalized)
	if err != nil {
		return err
	}
	prevBuffer := fileBuffer.Swap(buf)

	prev := global.Load()
	global.Store(zl)
	sugar.Store(zl.Sugar())
	atomicLevel = al
	initOptions = normalized

	bridgeSlogLocked()
	bridgeStdLogLocked()

	if prev != nil {
		_ = prev.Sync()
	}
	if prevBuffer != nil {
		// 停掉旧缓冲的刷盘协程并刷出剩余内容，避免每次 Reconfigure 泄漏一个协程。
		_ = prevBuffer.Stop()
	}
	return nil
}

func Reconfigure(mutator func(*InitOptions) error) error {
	mu.Lock()
	defer mu.Unlock()
	next := initOptions
	if mutator != nil {
		if err := mutator(&next); err != nil {
			return err
		}
	}
	return initLocked(next)
}

func SetLevel(level string) error {
	lv, ok := parseLevel(level)
	if !ok {
		return fmt.Errorf("invalid log level: %s", level)
	}

	mu.Lock()
	defer mu.Unlock()
	atomicLevel.SetLevel(lv)
	initOptions.Level = strings.ToLower(strings.TrimSpace(level))
	return nil
}

func CurrentLevel() string {
	mu.RLock()
	defer mu.RUnlock()
	if global.Load() == nil {
		return "info"
	}
	return atomicLevel.Level().String()
}

func SetSink(sink Sink) {
	currentSink.Store(sinkState{sink: sink})
}

func loadSink() Sink {
	v := currentSink.Load()
	if v == nil {
		return nil
	}
	state, ok := v.(sinkState)
	if !ok {
		return nil
	}
	return state.sink
}

// WriteSinkEvent 直接写入日志 sink，不经过全局日志级别门控。
// 用于需要“可观测性入库”与“业务输出级别”解耦的场景（例如 ops 系统日志索引）。
func WriteSinkEvent(level, component, message string, fields map[string]any) {
	sink := loadSink()
	if sink == nil {
		return
	}

	level = strings.ToLower(strings.TrimSpace(level))
	if level == "" {
		level = "info"
	}
	component = strings.TrimSpace(component)
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}

	eventFields := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		eventFields[k] = v
	}
	if component != "" {
		if _, ok := eventFields["component"]; !ok {
			eventFields["component"] = component
		}
	}

	sink.WriteLogEvent(&LogEvent{
		Time:       time.Now(),
		Level:      level,
		Component:  component,
		Message:    message,
		LoggerName: component,
		Fields:     eventFields,
	})
}

func L() *zap.Logger {
	if l := global.Load(); l != nil {
		return l
	}
	return zap.NewNop()
}

func S() *zap.SugaredLogger {
	if s := sugar.Load(); s != nil {
		return s
	}
	return zap.NewNop().Sugar()
}

func With(fields ...zap.Field) *zap.Logger {
	return L().With(fields...)
}

func Sync() {
	l := global.Load()
	if l != nil {
		_ = l.Sync()
	}
}

func bridgeStdLogLocked() {
	if stdLogUndo != nil {
		stdLogUndo()
		stdLogUndo = nil
	}

	prevFlags := log.Flags()
	prevPrefix := log.Prefix()
	prevWriter := log.Writer()

	log.SetFlags(0)
	log.SetPrefix("")
	base := global.Load()
	if base == nil {
		base = zap.NewNop()
	}
	log.SetOutput(newStdLogBridge(base.Named("stdlog")))

	stdLogUndo = func() {
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
	}
}

func bridgeSlogLocked() {
	base := global.Load()
	if base == nil {
		base = zap.NewNop()
	}
	slog.SetDefault(slog.New(newSlogZapHandler(base.Named("slog"))))
}

func buildLogger(options InitOptions) (*zap.Logger, zap.AtomicLevel, *zapcore.BufferedWriteSyncer, error) {
	level, _ := parseLevel(options.Level)
	atomic := zap.NewAtomicLevelAt(level)

	encoderCfg := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	var enc zapcore.Encoder
	if options.Format == "console" {
		enc = zapcore.NewConsoleEncoder(encoderCfg)
	} else {
		enc = zapcore.NewJSONEncoder(encoderCfg)
	}

	sinkCore := newSinkCore()
	cores := make([]zapcore.Core, 0, 3)

	if options.Output.ToStdout {
		infoPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
			return lvl >= atomic.Level() && lvl < zapcore.WarnLevel
		})
		errPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
			return lvl >= atomic.Level() && lvl >= zapcore.WarnLevel
		})
		stdout, stderr := stdWriteSyncers()
		cores = append(cores, zapcore.NewCore(enc, stdout, infoPriority))
		cores = append(cores, zapcore.NewCore(enc, stderr, errPriority))
	}

	var buf *zapcore.BufferedWriteSyncer
	if options.Output.ToFile {
		fileCore, fileBuf, filePath, fileErr := buildFileCore(enc, atomic, options)
		if fileErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "time=%s level=WARN msg=\"日志文件输出初始化失败，降级为仅标准输出\" path=%s err=%v\n",
				time.Now().Format(time.RFC3339Nano),
				filePath,
				fileErr,
			)
		} else {
			cores = append(cores, fileCore)
			buf = fileBuf
		}
	}

	if len(cores) == 0 {
		cores = append(cores, zapcore.NewCore(enc, zapcore.Lock(os.Stdout), atomic))
	}

	core := zapcore.NewTee(cores...)
	if options.Sampling.Enabled {
		core = zapcore.NewSamplerWithOptions(core, samplingTick(), options.Sampling.Initial, options.Sampling.Thereafter)
	}
	core = sinkCore.Wrap(core)

	stacktraceLevel, _ := parseStacktraceLevel(options.StacktraceLevel)
	zapOpts := make([]zap.Option, 0, 5)
	if options.Caller {
		zapOpts = append(zapOpts, zap.AddCaller())
	}
	if stacktraceLevel <= zapcore.FatalLevel {
		zapOpts = append(zapOpts, zap.AddStacktrace(stacktraceLevel))
	}

	logger := zap.New(core, zapOpts...).With(
		zap.String("service", options.ServiceName),
		zap.String("env", options.Environment),
	)
	return logger, atomic, buf, nil
}

func buildFileCore(enc zapcore.Encoder, atomic zap.AtomicLevel, options InitOptions) (zapcore.Core, *zapcore.BufferedWriteSyncer, string, error) {
	filePath := options.Output.FilePath
	if strings.TrimSpace(filePath) == "" {
		filePath = resolveLogFilePath("")
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, filePath, err
	}
	lj := &lumberjack.Logger{
		Filename:   filePath,
		MaxSize:    options.Rotation.MaxSizeMB,
		MaxBackups: options.Rotation.MaxBackups,
		MaxAge:     options.Rotation.MaxAgeDays,
		Compress:   options.Rotation.Compress,
		LocalTime:  options.Rotation.LocalTime,
	}
	buf := &zapcore.BufferedWriteSyncer{
		WS:            wrapFileWriteSyncer(zapcore.AddSync(lj)),
		Size:          fileBufferSize,
		FlushInterval: fileFlushInterval,
	}
	return &syncOnErrorCore{Core: zapcore.NewCore(enc, buf, atomic)}, buf, filePath, nil
}

// syncOnErrorCore 在写完 Error 及以上级别的日志后立即 Sync，
// 让缓冲输出上的错误日志不必等到下一次定时刷盘。
type syncOnErrorCore struct {
	zapcore.Core
}

func (c *syncOnErrorCore) With(fields []zapcore.Field) zapcore.Core {
	return &syncOnErrorCore{Core: c.Core.With(fields)}
}

func (c *syncOnErrorCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return ce.AddCore(entry, c)
	}
	return ce
}

func (c *syncOnErrorCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	if err := c.Core.Write(entry, fields); err != nil {
		return err
	}
	if entry.Level >= zapcore.ErrorLevel {
		return c.Sync()
	}
	return nil
}

type sinkCore struct {
	core   zapcore.Core
	fields []zapcore.Field
}

func newSinkCore() *sinkCore {
	return &sinkCore{}
}

func (s *sinkCore) Wrap(core zapcore.Core) zapcore.Core {
	cp := *s
	cp.core = core
	return &cp
}

func (s *sinkCore) Enabled(level zapcore.Level) bool {
	return s.core.Enabled(level)
}

func (s *sinkCore) With(fields []zapcore.Field) zapcore.Core {
	nextFields := append([]zapcore.Field{}, s.fields...)
	nextFields = append(nextFields, fields...)
	return &sinkCore{
		core:   s.core.With(fields),
		fields: nextFields,
	}
}

func (s *sinkCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	// Delegate to inner core (tee) so each sub-core's level enabler is respected.
	// Then add ourselves for sink forwarding only.
	ce = s.core.Check(entry, ce)
	if ce != nil {
		ce = ce.AddCore(entry, s)
	}
	return ce
}

func (s *sinkCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// Only handle sink forwarding — the inner cores write via their own
	// Write methods (added to CheckedEntry by s.core.Check above).
	sink := loadSink()
	if sink == nil {
		return nil
	}
	level := strings.ToLower(entry.Level.String())
	if filter, ok := sink.(SinkFilter); ok {
		if component, skip, ok := sinkRoutingHints(entry.LoggerName, s.fields, fields); ok &&
			!filter.AcceptsLogEntry(level, component, skip) {
			return nil
		}
	}

	enc := zapcore.NewMapObjectEncoder()
	for _, f := range s.fields {
		f.AddTo(enc)
	}
	for _, f := range fields {
		f.AddTo(enc)
	}

	event := &LogEvent{
		Time:       entry.Time,
		Level:      level,
		Component:  entry.LoggerName,
		Message:    entry.Message,
		LoggerName: entry.LoggerName,
		Fields:     enc.Fields,
	}
	sink.WriteLogEvent(event)
	return nil
}

// sinkRoutingHints 不编码字段，直接从字段列表里取出 SinkFilter 需要的 component 与 skip，
// 结果与 MapObjectEncoder 编码后的顶层键值一致（同名键后者覆盖前者）。
// 遇到无法在不编码的前提下确定结果的字段（非字符串 component、非布尔 skip、
// Namespace/Inline 会改变顶层键），返回 ok=false，由调用方走完整编码路径。
func sinkRoutingHints(loggerName string, ctxFields, entryFields []zapcore.Field) (component string, skip bool, ok bool) {
	fieldComponent := ""
	for _, fields := range [2][]zapcore.Field{ctxFields, entryFields} {
		for i := range fields {
			f := &fields[i]
			switch {
			case f.Type == zapcore.NamespaceType || f.Type == zapcore.InlineMarshalerType:
				return "", false, false
			case f.Key == "component":
				if f.Type != zapcore.StringType {
					return "", false, false
				}
				fieldComponent = f.String
			case f.Key == OpsSystemLogSkipField:
				if f.Type != zapcore.BoolType {
					return "", false, false
				}
				skip = f.Integer == 1
			}
		}
	}
	if strings.TrimSpace(fieldComponent) != "" {
		return fieldComponent, skip, true
	}
	return loggerName, skip, true
}

func (s *sinkCore) Sync() error {
	return s.core.Sync()
}

type stdLogBridge struct {
	logger *zap.Logger
}

func newStdLogBridge(l *zap.Logger) io.Writer {
	if l == nil {
		l = zap.NewNop()
	}
	return &stdLogBridge{logger: l}
}

func (b *stdLogBridge) Write(p []byte) (int, error) {
	msg := normalizeStdLogMessage(string(p))
	if msg == "" {
		return len(p), nil
	}

	level := inferStdLogLevel(msg)
	entry := b.logger.WithOptions(zap.AddCallerSkip(4))

	switch level {
	case LevelDebug:
		entry.Debug(msg, zap.Bool("legacy_stdlog", true))
	case LevelWarn:
		entry.Warn(msg, zap.Bool("legacy_stdlog", true))
	case LevelError, LevelFatal:
		entry.Error(msg, zap.Bool("legacy_stdlog", true))
	default:
		entry.Info(msg, zap.Bool("legacy_stdlog", true))
	}
	// log.Fatal* 经由这里输出后直接 os.Exit，走不到 main 里的 defer logger.Sync()。
	// 标准库 log 只剩低频的历史调用，每条都刷一次文件缓冲，保证启动失败等信息落盘。
	if buf := fileBuffer.Load(); buf != nil {
		_ = buf.Sync()
	}
	return len(p), nil
}

func normalizeStdLogMessage(raw string) string {
	msg := strings.TrimSpace(strings.ReplaceAll(raw, "\n", " "))
	if msg == "" {
		return ""
	}
	return strings.Join(strings.Fields(msg), " ")
}

func inferStdLogLevel(msg string) Level {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if lower == "" {
		return LevelInfo
	}

	if strings.HasPrefix(lower, "[debug]") || strings.HasPrefix(lower, "debug:") {
		return LevelDebug
	}
	if strings.HasPrefix(lower, "[warn]") || strings.HasPrefix(lower, "[warning]") || strings.HasPrefix(lower, "warn:") || strings.HasPrefix(lower, "warning:") {
		return LevelWarn
	}
	if strings.HasPrefix(lower, "[error]") || strings.HasPrefix(lower, "error:") || strings.HasPrefix(lower, "fatal:") || strings.HasPrefix(lower, "panic:") {
		return LevelError
	}

	if strings.Contains(lower, " failed") || strings.Contains(lower, "error") || strings.Contains(lower, "panic") || strings.Contains(lower, "fatal") {
		return LevelError
	}
	if strings.Contains(lower, "warning") || strings.Contains(lower, "warn") || strings.Contains(lower, " queue full") || strings.Contains(lower, "fallback") {
		return LevelWarn
	}
	return LevelInfo
}

// LegacyPrintf 用于平滑迁移历史的 printf 风格日志到结构化 logger。
func LegacyPrintf(component, format string, args ...any) {
	msg := normalizeStdLogMessage(fmt.Sprintf(format, args...))
	if msg == "" {
		return
	}

	initialized := global.Load() != nil
	if !initialized {
		// 在日志系统未初始化前，回退到标准库 log，避免测试/工具链丢日志。
		log.Print(msg)
		return
	}

	l := L()
	if component != "" {
		l = l.With(zap.String("component", component))
	}
	l = l.WithOptions(zap.AddCallerSkip(1))

	switch inferStdLogLevel(msg) {
	case LevelDebug:
		l.Debug(msg, zap.Bool("legacy_printf", true))
	case LevelWarn:
		l.Warn(msg, zap.Bool("legacy_printf", true))
	case LevelError, LevelFatal:
		l.Error(msg, zap.Bool("legacy_printf", true))
	default:
		l.Info(msg, zap.Bool("legacy_printf", true))
	}
}

type contextKey string

const loggerContextKey contextKey = "ctx_logger"

func IntoContext(ctx context.Context, l *zap.Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if l == nil {
		l = L()
	}
	return context.WithValue(ctx, loggerContextKey, l)
}

func FromContext(ctx context.Context) *zap.Logger {
	if ctx == nil {
		return L()
	}
	if l, ok := ctx.Value(loggerContextKey).(*zap.Logger); ok && l != nil {
		return l
	}
	return L()
}
