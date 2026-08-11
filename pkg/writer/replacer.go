package writer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The replacer is the one place this package turns an intention into bytes on
// disk. Every format writer reads its file through openReplacer and writes it
// through commit, so the read cap, the splice, the overlap check, the proof
// that the result still parses and the atomic write are written once and
// behave the same for all twenty-one of them.
//
// A writer expresses its change one of two ways. The span writers queue byte
// ranges through replace, which cannot disturb anything they do not cover. The
// writers whose format is line-structured, and the one that regenerates the
// whole file through a formatter, hand back finished bytes through setLines or
// setWhole. Both arrive at commit, which is what makes "one entry point" true
// rather than aspirational.

// span is the byte range one value occupies inside a file.
type span struct{ start, end int64 }

// patch is one queued replacement: the bytes of a span become text.
type patch struct {
	span
	text []byte
}

// errOverlappingPatches marks two queued replacements covering the same bytes.
// The result would depend on the order they were queued in, which is never
// what a caller means, so the write is refused instead.
var errOverlappingPatches = errors.New("two replacements cover the same bytes")

// errMixedEdits marks a writer that both queued spans and handed back a
// finished file. The two describe the same bytes twice and only one can win.
var errMixedEdits = errors.New("a regenerated file cannot also carry span replacements")

// replacer holds one file's contents and the changes queued against it.
type replacer struct {
	path    string
	data    []byte
	patches []patch
	whole   []byte
	rebuilt bool
}

// openReplacer reads the file at path, refusing one larger than the cap. The
// size is checked against the open handle and again against what was read, so
// a file growing between the two cannot slip past.
func openReplacer(path string) (*replacer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxManifestBytes {
		return nil, tooLarge(path, info.Size())
	}
	data, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxManifestBytes {
		return nil, tooLarge(path, int64(len(data)))
	}
	return &replacer{path: path, data: data}, nil
}

// tooLarge is the refusal every read cap reports.
func tooLarge(path string, size int64) error {
	return fmt.Errorf("%s: %w (%d bytes)", path, ErrManifestTooLarge, size)
}

// bytes is the file as it was read. The queued patches are not applied to it,
// so a locator may keep using offsets into it until commit.
func (r *replacer) bytes() []byte { return r.data }

// text is the file as it was read, as a string.
func (r *replacer) text() string { return string(r.data) }

// at is the file's current bytes across a span.
func (r *replacer) at(s span) []byte { return r.data[s.start:s.end] }

// replace queues text in place of the span's bytes.
func (r *replacer) replace(s span, text []byte) {
	r.patches = append(r.patches, patch{s, text})
}

// lines is the line-oriented view of the file, freshly split so the caller
// owns it. A writer that edits it hands it back through setLines.
func (r *replacer) lines() []string { return strings.Split(string(r.data), "\n") }

// setLines takes a line view back, joined the way it was split.
func (r *replacer) setLines(lines []string) { r.setWhole([]byte(strings.Join(lines, "\n"))) }

// setWhole takes a regenerated file: what a formatter produces, where there
// are no spans to speak of.
func (r *replacer) setWhole(data []byte) {
	r.whole, r.rebuilt = data, true
}

// result is the file the queued changes describe.
func (r *replacer) result() ([]byte, error) {
	if r.rebuilt {
		if len(r.patches) > 0 {
			return nil, errMixedEdits
		}
		return r.whole, nil
	}
	if len(r.patches) == 0 {
		return r.data, nil
	}
	// Front to back, in one allocation of the exact final size: the naive form
	// re-splices the whole file once per patch, which is quadratic on a
	// manifest carrying hundreds of edits.
	sorted := append([]patch(nil), r.patches...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].start < sorted[j].start })
	size := len(r.data)
	for i, p := range sorted {
		if i > 0 && p.start < sorted[i-1].end {
			return nil, errOverlappingPatches
		}
		size += len(p.text) - int(p.end-p.start)
	}
	out := make([]byte, 0, size)
	at := int64(0)
	for _, p := range sorted {
		out = append(out, r.data[at:p.start]...)
		out = append(out, p.text...)
		at = p.end
	}
	return append(out, r.data[at:]...), nil
}

// commit writes the file, and only when the queued changes actually change it.
//
// verify, when given, is the format's proof that the result still parses. A
// splice is span-precise, but a manifest is user data and no writer here
// commits bytes it has not proved still read as the format they are in; the
// formats with no cheap grammar to check against pass a guard that re-runs
// their own reader instead.
func (r *replacer) commit(verify func(out []byte) error) error {
	out, err := r.result()
	if err != nil {
		return fmt.Errorf("%s: internal error: %w", r.path, err)
	}
	if bytes.Equal(out, r.data) {
		return nil
	}
	if verify != nil {
		if err := verify(out); err != nil {
			return fmt.Errorf("%s: internal error: %w", r.path, err)
		}
	}
	return atomicWrite(r.path, out)
}

// atomicWrite replaces the file's contents keeping its permissions, via a
// same-folder temp file, fsync and rename: a process crash never leaves a
// half-written manifest (a power loss is the filesystem's problem, and the
// fsync narrows even that window).
func atomicWrite(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	// The temp file must live beside the target so the rename stays on one
	// filesystem.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dispat-write-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, info.Mode()); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
