package writer

import (
	"fmt"
	"sort"

	"github.com/yohimik/dispat/pkg/manifest"
)

// The two Docker formats declare the same thing in different syntax: a
// Dockerfile writes an image reference after FROM, a compose file writes one
// after image:. Once each writer has located a reference and the bytes it
// occupies, the rest — deciding whether it may be written, matching it against
// the requested edits, queueing the splice, and sorting the outcomes into
// Applied, Skipped and Missing — is identical, and lives here so the two
// cannot answer the same question differently.

// tagSplice is one queued replacement: the bytes a tag occupies inside a line,
// and what they become. Everything either writer does is expressed through
// these offsets, so a splice can only ever disturb the tag it aimed at — a
// --platform flag before the reference, an AS alias after it, the indentation
// and quoting around it all survive untouched.
type tagSplice struct {
	line       int
	start, end int
	text       string
}

// writableImageRef reports a reference whose tag a writer may replace.
//
// Three shapes are declined, and each has a reason that is not an error:
//
//	redis                    no tag to replace, and inventing one would
//	                         override the default the author chose
//	redis@sha256:...         the digest is what gets pulled, so rewriting the
//	                         tag beside it would leave the file naming one
//	                         version and using another
//	${REGISTRY}/base:${TAG}  the value is resolved outside the file, and a
//	                         literal would sever the indirection it exists for
//
// The last of these is the same judgement isDeferredValue makes for a Maven
// ${property} or an Xcode $(MARKETING_VERSION), spelled for a reference whose
// interpolation can sit in either half.
func writableImageRef(ref manifest.ImageRef) bool {
	return ref.HasTag() && !ref.Pinned() && !ref.Interpolated()
}

// imageTagWriter accumulates the tag splices one file's rewrite will make, and
// remembers what each requested edit met along the way.
type imageTagWriter struct {
	path  string
	edits []Edit
	// seen: the edit's repository was referenced somewhere in the file.
	// found: at least one of those references had a writable tag.
	// changed: at least one of them actually needed changing.
	seen, found, changed map[int]bool
	pending              map[int][]tagSplice
	versionWritten       bool
}

// newImageTagWriter starts a rewrite of the file at path.
func newImageTagWriter(path string, edits []Edit) *imageTagWriter {
	return &imageTagWriter{
		path:    path,
		edits:   edits,
		seen:    make(map[int]bool, len(edits)),
		found:   make(map[int]bool, len(edits)),
		changed: make(map[int]bool, len(edits)),
		pending: make(map[int][]tagSplice),
	}
}

// match offers one located reference to the requested edits: the first whose
// name is this reference's repository claims the bytes, and only that one
// queues a splice, so two edits can never cover the same bytes.
//
// The outcome, though, is recorded against *every* edit naming that
// repository. A file may declare one image twice — two stages on different
// tags of the same base — and the scan that produced the edits then produced
// one per declaration. Resolving only the first would leave the rest looking
// like edits the manifest does not declare, and a caller reporting Missing
// would warn about a declaration that is plainly there.
func (w *imageTagWriter) match(line, start int, text string) error {
	ref := manifest.ParseImageRef(text)
	claim := -1
	for i, e := range w.edits {
		if e.Name == ref.Repository {
			claim = i
			break
		}
	}
	if claim < 0 {
		return nil
	}
	mark := func(m map[int]bool) {
		for i, e := range w.edits {
			if e.Name == ref.Repository {
				m[i] = true
			}
		}
	}
	mark(w.seen)
	if !writableImageRef(ref) {
		return nil
	}
	mark(w.found)
	if ref.Tag == w.edits[claim].Range {
		return nil // already the wanted tag: not a change, and not missing
	}
	if err := w.queue(line, start, ref, w.edits[claim].Range); err != nil {
		return err
	}
	mark(w.changed)
	return nil
}

// setVersion writes the file's own version into a reference the file declares
// as its own. Unlike match there is no Edit behind it, so an unwritable
// reference is simply left alone.
func (w *imageTagWriter) setVersion(line, start int, text, version string) error {
	ref := manifest.ParseImageRef(text)
	if version == "" || !writableImageRef(ref) || ref.Tag == version {
		return nil
	}
	if err := w.queue(line, start, ref, version); err != nil {
		return err
	}
	w.versionWritten = true
	return nil
}

// queue records the splice that turns one reference's tag into text. start is
// where the reference begins in its line, so the tag's own offsets shift by it.
func (w *imageTagWriter) queue(line, start int, ref manifest.ImageRef, tag string) error {
	if !manifest.ValidTag(tag) {
		return fmt.Errorf("%s: refusing to write %q as a Docker tag", w.path, tag)
	}
	w.pending[line] = append(w.pending[line], tagSplice{
		line:  line,
		start: start + ref.TagStart,
		end:   start + ref.TagEnd,
		text:  tag,
	})
	return nil
}

// apply rewrites each touched line back to front, so replacing one reference
// never moves the bytes of another on the same line — which a single RUN
// --mount line with two from= options really can carry. It reports whether
// anything changed.
func (w *imageTagWriter) apply(lines []string) bool {
	changed := false
	for li, queued := range w.pending {
		sort.Slice(queued, func(i, j int) bool { return queued[i].start > queued[j].start })
		line := lines[li]
		for _, q := range queued {
			line = line[:q.start] + q.text + line[q.end:]
			changed = true
		}
		lines[li] = line
	}
	return changed
}

// fill sorts the edits into the result's three buckets. An edit that met a
// reference it could not write is Skipped rather than Missing, because the two
// call for different responses: Skipped is the normal state of a Dockerfile
// pinning its base by digest, while Missing means the caller and the file
// disagree about what is declared.
func (w *imageTagWriter) fill(res *Result) {
	res.VersionWritten = w.versionWritten
	for i, e := range w.edits {
		switch {
		case w.changed[i]:
			res.Applied = append(res.Applied, e)
		case w.found[i]: // already correct: nothing to report
		case w.seen[i]:
			res.Skipped = append(res.Skipped, e)
		default:
			res.Missing = append(res.Missing, e)
		}
	}
}

// verifyImageTags re-reads the written bytes and checks every applied edit
// reads back as the tag it asked for. A splice is span-precise, but the spans
// were measured against a hand-written file, and this is the proof they landed
// where they were aimed. refs are the references the caller re-located in the
// result, the same way it located them in the original.
func verifyImageTags(applied []Edit, refs []string) error {
	wanted := make(map[string]string, len(applied))
	for _, e := range applied {
		wanted[e.Name] = e.Range
	}
	for _, text := range refs {
		ref := manifest.ParseImageRef(text)
		want, ok := wanted[ref.Repository]
		if !ok || !writableImageRef(ref) {
			continue
		}
		if ref.Tag != want {
			return fmt.Errorf("rewrite left %s at %q, want %q", ref.Repository, ref.Tag, want)
		}
	}
	return nil
}
