package core

import (
	"errors"
	"fmt"
	tunlog "github.com/xjasonlyu/tun2socks/v2/log"
	xraylog "github.com/xtls/xray-core/common/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"strings"
	"sync"
	"sync/atomic"
)

// LogHandler receives the log lines of the core: the level ("info", "warning",
// "error", ...), the message and where it came from ("xray", "tun2socks",
// "iobridge"). It is called from one goroutine of its own, one line at a time
// and in order, never from the goroutines that carry traffic. If it falls
// behind by more than a queue of lines, the new lines are dropped (and the loss
// is reported), so it should still return quickly.
type LogHandler interface {
	OnLog(level, message, source string)
}

var (
	logMu      sync.RWMutex
	logHandler LogHandler
)

// logQueueSize is how many lines wait for the handler. Lines are about a
// hundred bytes, so a full queue is some tens of kilobytes.
const logQueueSize = 512

type logLine struct {
	level, message, source string
	flushed                chan struct{} // set on the marker flushLogs queues, nothing else
}

var (
	logQueue   = make(chan logLine, logQueueSize)
	logDropped atomic.Int64
	logStart   sync.Once
)

// SetLogHandler sets where the log lines go; nil stops delivering them. Safe at
// any time, also while the core runs.
func SetLogHandler(h LogHandler) {
	logMu.Lock()
	logHandler = h
	logMu.Unlock()
}

// emitLog hands a line to the handler without waiting for it.
//
// The lines are logged by the goroutines that carry connections and packets,
// and the handler is a call into Kotlin or Swift. A handler that waits (the
// main thread is busy, a lock is held) must not hold those goroutines, so the
// lines go through a queue to one goroutine that calls the handler, in order.
// When the handler falls behind by more than the queue holds, the new lines are
// dropped and counted; the count is reported when the handler is back. With no
// handler nothing is queued.
func emitLog(level, message, source string) {
	logMu.RLock()
	h := logHandler
	logMu.RUnlock()
	if h == nil {
		return
	}
	logStart.Do(func() { go deliverLogs() })
	select {
	case logQueue <- logLine{level: level, message: message, source: source}:
	default:
		logDropped.Add(1)
	}
}

// deliverLogs calls the handler for the queued lines, one at a time.
func deliverLogs() {
	for line := range logQueue {
		if line.flushed != nil {
			close(line.flushed)
			continue
		}
		if n := logDropped.Swap(0); n > 0 {
			deliverLog("warning", fmt.Sprintf("%d log lines were dropped: the handler is too slow", n), "core")
		}
		deliverLog(line.level, line.message, line.source)
	}
}

// deliverLog calls the handler, which is read again here: it may have changed
// since the line was queued. A panic of the handler ends that call, not the
// delivery of the lines after it.
func deliverLog(level, message, source string) {
	logMu.RLock()
	h := logHandler
	logMu.RUnlock()
	if h == nil {
		return
	}
	defer func() { _ = recover() }()
	h.OnLog(level, message, source)
}

// flushLogs returns once every line queued before the call has been handed to
// the handler. For tests, which look at what the handler got.
func flushLogs() {
	done := make(chan struct{})
	logStart.Do(func() { go deliverLogs() })
	logQueue <- logLine{flushed: done}
	<-done
}

// LogInfo and LogError send a line of the core itself to the handler.
func LogInfo(message, source string)  { emitLog("info", message, source) }
func LogError(message, source string) { emitLog("error", message, source) }

// verboseLogs raises xray and tun2socks to info for debug builds. The app sets
// it before Start from its own debuggable flag, so a release build can't turn
// it on. Even then engine's start-up logger stays at warn (see startTun): its
// "[STACK]" line would print the proxy password.
var verboseLogs atomic.Bool

// SetVerboseLogs takes effect on the next Start.
func SetVerboseLogs(on bool) { verboseLogs.Store(on) }

// VerboseLogs reports whether this is a debug build, see SetVerboseLogs.
func VerboseLogs() bool { return verboseLogs.Load() }

// ErrDebugOnly is what the debug commands return in a release build.
var ErrDebugOnly = errors.New("debug builds only: SetVerboseLogs(true) was not called")

// RequireDebug is the gate of the debug commands (heap profile, goroutine
// dump, GC tuning): nil in a debug build, ErrDebugOnly otherwise. The apps
// register their debug entry points only when they are debuggable; this keeps
// the core from relying on that alone.
func RequireDebug() error {
	if !verboseLogs.Load() {
		return ErrDebugOnly
	}
	return nil
}

// InstallLogForwarding routes the logs of xray and tun2socks to the handler,
// filtered by level. The tun2socks logger reads the debug flag when it is
// created, so this is called again at every start to pick up SetVerboseLogs.
func InstallLogForwarding() {
	xraylog.RegisterHandler(&xrayLogForwarder{})
	tunlog.SetLogger(newTun2socksForwardingLogger())
}

// xrayLogForwarder replaces xray's own log handler, and with it the severity
// filter app/log applies from the config's "log" block (RegisterHandler
// discards the previous handler). So the filtering has to happen here, or
// every Debug line and every access/DNS record reaches the app.
type xrayLogForwarder struct{}

func (*xrayLogForwarder) Handle(msg xraylog.Message) {
	verbose := verboseLogs.Load()
	level := "info"
	if gm, ok := msg.(*xraylog.GeneralMessage); ok {
		if !keepXraySeverity(gm.Severity, verbose) {
			return
		}
		level = strings.ToLower(gm.Severity.String())
	} else if !verbose {
		return // access and DNS records: who went where, debug builds only
	}
	emitLog(level, msg.String(), "xray")
}

// keepXraySeverity: Warning and above always, Info too in debug builds, Debug
// never (it is per-packet noise). Lower Severity values are more severe.
func keepXraySeverity(s xraylog.Severity, verbose bool) bool {
	limit := xraylog.Severity_Warning
	if verbose {
		limit = xraylog.Severity_Info
	}
	return s <= limit
}

func newTun2socksForwardingLogger() *zap.Logger {
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = ""
	// Only filter once this logger replaces engine's own (engine.Key.LogLevel
	// no longer applies); the sole filter on the iOS path. Keep >= warn
	// outside debug builds.
	level := zapcore.WarnLevel
	if verboseLogs.Load() {
		level = zapcore.InfoLevel
	}
	return zap.New(&tun2socksForwardCore{
		LevelEnabler: level,
		encoder:      zapcore.NewConsoleEncoder(encoderCfg),
	})
}

type tun2socksForwardCore struct {
	zapcore.LevelEnabler
	encoder zapcore.Encoder
}

func (c *tun2socksForwardCore) With(fields []zapcore.Field) zapcore.Core {
	clone := c.encoder.Clone()
	for _, f := range fields {
		f.AddTo(clone)
	}
	return &tun2socksForwardCore{LevelEnabler: c.LevelEnabler, encoder: clone}
}

func (c *tun2socksForwardCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.LevelEnabler.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *tun2socksForwardCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	buf, err := c.encoder.EncodeEntry(ent, fields)
	if err != nil {
		return err
	}
	emitLog(ent.Level.String(), strings.TrimRight(buf.String(), "\n"), "tun2socks")
	buf.Free()
	return nil
}

func (c *tun2socksForwardCore) Sync() error { return nil }
