package release

import (
	"bytes"
	"strings"
	"sync"

	"github.com/rs/zerolog"
)

// lineWriter forwards script output to the logger line by line, so build and
// publish logs stay readable when packages run in parallel.
type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	log zerolog.Logger
	lvl zerolog.Level
}

func newLineWriter(log zerolog.Logger, lvl zerolog.Level) *lineWriter {
	return &lineWriter{log: log, lvl: lvl}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]
		if line != "" {
			w.log.WithLevel(w.lvl).Msg(line)
		}
	}
	return len(p), nil
}

// Flush logs any trailing output that did not end with a newline.
func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.log.WithLevel(w.lvl).Msg(string(w.buf))
		w.buf = nil
	}
}
