package core

import (
	tunlog "github.com/xjasonlyu/tun2socks/v2/log"
	xraylog "github.com/xtls/xray-core/common/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"strings"
	"sync"
	"sync/atomic"
)

type LogHandler interface {
	OnLog(level, message, source string)
}

var (
	logMu      sync.RWMutex
	logHandler LogHandler
)

func SetLogHandler(h LogHandler) {
	logMu.Lock()
	logHandler = h
	logMu.Unlock()
}

func emitLog(level, message, source string) {
	logMu.RLock()
	h := logHandler
	logMu.RUnlock()
	if h != nil {
		h.OnLog(level, message, source)
	}
}

func LogInfo(message, source string)  { emitLog("info", message, source) }
func LogError(message, source string) { emitLog("error", message, source) }

// verboseLogs raises xray and tun2socks to info for debug builds. The app sets
// it before Start from its own debuggable flag, so a release build can't turn
// it on. Even then engine's start-up logger stays at warn (see StartTun): its
// "[STACK]" line would print the proxy password.
var verboseLogs atomic.Bool

// SetVerboseLogs takes effect on the next Start.
func SetVerboseLogs(on bool) { verboseLogs.Store(on) }

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
	// no longer applies); the sole filter on the iOS/macOS path. Keep >= warn
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
