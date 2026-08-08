package release

import (
	"bytes"
	"strings"
	"sync"

	"github.com/rs/zerolog"
)

// maxLineBytes caps how much of one output line the writer buffers. A script
// emitting a long run with no newline — a progress bar rewriting itself with
// \r, a minified bundle, a base64 blob — would otherwise grow the buffer
// without bound for the whole life of the command. The head of an overlong
// line is logged with a truncation marker and the rest of the line is
// dropped.
const maxLineBytes = 1 << 20

// lineWriter forwards script output to the logger line by line, so build and
// publish logs stay readable when packages run in parallel.
type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	// truncated marks that the current line overflowed maxLineBytes: its head
	// is already logged and everything up to the next newline is dropped.
	truncated bool
	log       zerolog.Logger
	lvl       zerolog.Level
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
		if w.truncated {
			// The tail of an overlong line whose head is already logged.
			w.truncated = false
		} else if line := strings.TrimRight(string(w.buf[:i]), "\r"); line != "" {
			w.log.WithLevel(w.lvl).Msg(line)
		}
		w.buf = w.buf[i+1:]
	}
	if w.truncated {
		w.buf = w.buf[:0] // still inside the overlong line: keep dropping
	} else if len(w.buf) > maxLineBytes {
		w.log.WithLevel(w.lvl).Msg(string(w.buf[:maxLineBytes]) + " [line truncated: longer than 1 MiB]")
		w.buf = w.buf[:0]
		w.truncated = true
	}
	return len(p), nil
}

// Flush logs any trailing output that did not end with a newline.
func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.truncated {
		w.truncated = false
		w.buf = nil
		return
	}
	if len(w.buf) > 0 {
		w.log.WithLevel(w.lvl).Msg(string(w.buf))
		w.buf = nil
	}
}
