package release

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func captureWriter() (*lineWriter, *bytes.Buffer) {
	var buf bytes.Buffer
	return newLineWriter(zerolog.New(&buf), zerolog.InfoLevel), &buf
}

func TestLineWriterSplitsLines(t *testing.T) {
	w, buf := captureWriter()
	_, err := w.Write([]byte("one\ntwo\r\n\npart"))
	assert.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, `"message":"one"`)
	assert.Contains(t, out, `"message":"two"`, "trailing \\r is trimmed")
	assert.NotContains(t, out, `"message":""`, "blank lines are skipped")
	assert.NotContains(t, out, "part", "an unterminated tail stays buffered")

	buf.Reset()
	_, err = w.Write([]byte("ial\n"))
	assert.NoError(t, err)
	assert.Contains(t, buf.String(), `"message":"partial"`, "the tail joins its continuation")
}

func TestLineWriterFlushPartial(t *testing.T) {
	w, buf := captureWriter()
	_, _ = w.Write([]byte("no newline at end"))
	assert.Empty(t, buf.String())
	w.Flush()
	assert.Contains(t, buf.String(), `"message":"no newline at end"`)

	buf.Reset()
	w.Flush()
	assert.Empty(t, buf.String(), "a second flush has nothing left")
}

func TestLineWriterCapsOversizedLine(t *testing.T) {
	w, buf := captureWriter()
	head := strings.Repeat("a", maxLineBytes)
	_, _ = w.Write([]byte(head))
	_, _ = w.Write([]byte("bbbb")) // pushes past the cap with no newline
	out := buf.String()
	assert.Contains(t, out, "[line truncated: longer than 1 MiB]")

	buf.Reset()
	_, _ = w.Write([]byte("still the same overlong line"))
	assert.Empty(t, buf.String(), "the tail of a truncated line is dropped")

	_, _ = w.Write([]byte("...end\nnext\n"))
	out = buf.String()
	assert.NotContains(t, out, "end", "everything up to the newline belongs to the truncated line")
	assert.Contains(t, out, `"message":"next"`, "the following line logs normally")
}

func TestLineWriterFlushDuringTruncation(t *testing.T) {
	w, buf := captureWriter()
	_, _ = w.Write([]byte(strings.Repeat("x", maxLineBytes+1)))
	buf.Reset()
	w.Flush()
	assert.Empty(t, buf.String(), "a truncated line's head is already logged; the flush drops the rest")

	_, _ = w.Write([]byte("fresh\n"))
	assert.Contains(t, buf.String(), `"message":"fresh"`, "the flush also reset the truncation state")
}
