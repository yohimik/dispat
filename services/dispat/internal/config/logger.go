package config

// The loader's own events, on dispat's logger.
//
// pkg/config emits which directories the ascent tried and what it made of
// each, which files a `$ref` pulled in, and which key an edit was written to.
// Those are the answer to "it ran with the wrong config", which otherwise
// looks exactly like a configuration bug until you can see the ascent.
//
// The adapter reads the logger through an atomic pointer rather than holding
// one, because the loader is built when the package is initialised and the
// CLI's boot logger only exists once the flags have been parsed. Nothing is
// logged until UseLogger has been called, which is what a test run wants too.

import (
	"sync/atomic"

	"github.com/rs/zerolog"

	lib "github.com/yohimik/dispat/pkg/config"
)

// bootLog is where the loader's events go. Nil until the CLI hands over its
// boot logger.
var bootLog atomic.Pointer[zerolog.Logger]

// UseLogger points the loader's events at the CLI's boot logger, which is the
// one that exists while the configuration is still being found and read: the
// configured logger is built from the file this is about to load.
func UseLogger(l zerolog.Logger) { bootLog.Store(&l) }

// bootLogger is the pkg/config Logger over whatever UseLogger last stored.
type bootLogger struct{}

func (bootLogger) Enabled(level lib.Level) bool {
	l := bootLog.Load()
	if l == nil {
		return false
	}
	z := zerologLevel(level)
	return z >= l.GetLevel() && z != zerolog.Disabled
}

func (bootLogger) Log(level lib.Level, event string, fields ...lib.Field) {
	l := bootLog.Load()
	if l == nil {
		return
	}
	e := l.WithLevel(zerologLevel(level))
	if e == nil {
		return
	}
	for _, f := range fields {
		switch f.Kind() {
		case lib.KindString:
			e = e.Str(f.Key, f.Text())
		case lib.KindInt:
			e = e.Int64(f.Key, f.Number())
		case lib.KindBool:
			e = e.Bool(f.Key, f.Flag())
		case lib.KindError:
			e = e.AnErr(f.Key, f.Cause())
		default:
			e = e.Interface(f.Key, f.Value())
		}
	}
	e.Msg(event)
}

// zerologLevel maps the library's levels onto zerolog's. They are the same
// five, in the same order, which is why the mapping is a switch rather than
// arithmetic: the day either side adds one, this fails to compile rather than
// quietly logging at the wrong level.
func zerologLevel(level lib.Level) zerolog.Level {
	switch level {
	case lib.LevelTrace:
		return zerolog.TraceLevel
	case lib.LevelDebug:
		return zerolog.DebugLevel
	case lib.LevelInfo:
		return zerolog.InfoLevel
	case lib.LevelWarn:
		return zerolog.WarnLevel
	case lib.LevelError:
		return zerolog.ErrorLevel
	}
	return zerolog.DebugLevel
}
