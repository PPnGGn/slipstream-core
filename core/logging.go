package core

import (
	tunlog "github.com/xjasonlyu/tun2socks/v2/log"
	xraylog "github.com/xtls/xray-core/common/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"strings"
	"sync"
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

func InstallLogForwarding() {
	xraylog.RegisterHandler(&xrayLogForwarder{})
	tunlog.SetLogger(newTun2socksForwardingLogger())
}

type xrayLogForwarder struct{}

func (*xrayLogForwarder) Handle(msg xraylog.Message) {
	level := "info"
	if gm, ok := msg.(*xraylog.GeneralMessage); ok {
		level = strings.ToLower(gm.Severity.String())
	}
	emitLog(level, msg.String(), "xray")
}

func newTun2socksForwardingLogger() *zap.Logger {
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = ""
	return zap.New(&tun2socksForwardCore{
		LevelEnabler: zapcore.InfoLevel,
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
