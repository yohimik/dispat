// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

// Package changelog renders release entries and prepends them to a changelog
// file inside each package folder. The rendering helpers are shared with other
// release recorders (e.g. GitHub releases), which present the same changelog
// data through a different destination.
package changelog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/pkg/ccme"
	models "github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/fsx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// Format customises how a release entry is rendered. Zero values fall back to
// defaults, so an empty Format is always valid.
//
// The options themselves are model.RecordFormat, embedded rather than
// restated. The renderer used to keep its own copy of every field and a
// SpecFormat that assigned them one by one, which made a forgotten field the
// cheapest mistake in the package: a new option added to the model and missed
// in the copy compiled cleanly, passed every test that did not configure it,
// and then rendered the default for the packages that did, silently, in
// released changelogs. The embed is what makes that forgetting impossible.
// There is no copy left to forget, so an option reaches the renderer the moment
// it reaches the model.
//
// Anything the renderer needs that the resolved model does not carry belongs
// here beside the embed. Today there is nothing: the two describe the same
// entry, and the renderer's defaults live in withDefaults rather than in
// fields of its own.
type Format struct {
	model.RecordFormat
}

// withDefaults fills the fields the model leaves empty, which is where the
// renderer's own defaults live now that it holds no fields of its own: the
// date layout, the four built-in section titles, and the attribution and
// reference policies.
//
// Both policies default to "off", and the section order defaults to nothing at
// all (sectionOrder completes the list from model.DefaultSectionOrder).
// That is what keeps an entry byte for byte what it was before attribution,
// references and configurable sections existed: a configuration that says
// nothing about them renders exactly the entry it always did.
func (f Format) withDefaults() Format {
	defaultStr(&f.DateFormat, "2006-01-02")
	defaultStr(&f.BreakingTitle, "Breaking Changes")
	defaultStr(&f.FeaturesTitle, "Features")
	defaultStr(&f.FixesTitle, "Fixes")
	defaultStr(&f.DependenciesTitle, "Dependencies")
	defaultStr(&f.AuthorsPlacement, AuthorsOff)
	defaultStr(&f.AuthorsFormat, AuthorsFullName)
	defaultStr(&f.AuthorsCommits, AuthorsCommitsCCME)
	defaultStr(&f.AuthorsTitle, "Authors")
	defaultStr(&f.CommitRefsPlacement, RefsOff)
	defaultStr(&f.CommitRefsFormat, "$"+VarCommitShort)
	return f
}

// ResolveRepoEnv completes a pair of forge coordinates from $GITHUB_REPOSITORY
// when the configuration states neither of them.
//
// It is the same fallback the GitHub releaser resolves its repository through,
// applied to the record's link coordinates so that "auto" works in the
// ordinary CI setup, where the repository is what the workflow runs in and
// nobody writes it into the config file. Configuration wins over the
// environment.
//
// Only a wholly unstated pair is completed. A half-configured pair comes back
// as it went in, so "auto" declines rather than crossing a configured owner
// with the environment's repo: that names a repository nobody stated, and a
// published link into it is worse than the plain line the decline renders.
//
// The completion belongs where a recorder is built, once, rather than in the
// renderer: an entry renders the same text wherever it is rendered from, and a
// record that read the environment per line would say different things in a
// run and in a `dispat changelog` step.
func ResolveRepoEnv(owner, repo string) (string, string) {
	if owner != "" || repo != "" {
		return owner, repo
	}
	envOwner, envRepo, ok := strings.Cut(os.Getenv("GITHUB_REPOSITORY"), "/")
	if !ok || envOwner == "" || envRepo == "" {
		return owner, repo
	}
	return envOwner, envRepo
}

// WithRepoEnv completes the format's link coordinates from the environment.
// Every recorder applies it as it is constructed, which is the one place the
// environment is read.
func (f Format) WithRepoEnv() Format {
	f.LinkOwner, f.LinkRepo = ResolveRepoEnv(f.LinkOwner, f.LinkRepo)
	return f
}

func defaultStr(s *string, def string) {
	if *s == "" {
		*s = def
	}
}

// SpecFormat presents a package's resolved record format as the renderer's.
//
// It is the embed and nothing else. It stays a named function because the call
// sites read better for it (a recorder is built from a spec's format, and
// saying so is clearer than the composite literal), and because a wrapper that
// copies no field can never fall behind the model it wraps.
func SpecFormat(f model.RecordFormat) Format { return Format{RecordFormat: f} }

// Dispatcher routes each release through a FileWriter built from the
// package's resolved changelog policy — per-package configuration decides
// the file, the title, the format, and whether a changelog is written at
// all. It implements release.ReleaseRecorder.
type Dispatcher struct {
	Now func() time.Time // injectable clock, passed to the writers
	Log zerolog.Logger   // carries the per-package skip notices
}

// Record writes the release entry through the package's policy; a package
// whose changelog is disabled — or whose policy holds prereleases back —
// records nothing.
func (d *Dispatcher) Record(ctx context.Context, rel *plan.Release) error {
	spec := rel.Pkg.Changelog
	if !spec.Records(rel.Channel) {
		LogSkip(d.Log, spec, rel)
		return nil
	}
	w := &FileWriter{
		File: spec.File, FileTitle: spec.FileTitle, EntrySpacing: spec.EntrySpacing,
		Format: SpecFormat(spec.Format).WithRepoEnv(), Now: d.Now, Log: d.Log,
	}
	return w.Record(ctx, rel)
}

// LogSkip explains why a policy wrote nothing, so the two callers that ask
// spec.Records — the dispatcher and the standalone changelog command — agree
// on the wording. A file switched off outright is ordinary configuration and
// stays at debug level; a release held back by the channels it records on is a
// release-shaped decision the operator should see, named by its channel.
func LogSkip(log zerolog.Logger, spec model.ChangelogSpec, rel *plan.Release) {
	if !spec.Enabled {
		log.Debug().Str("package", rel.Pkg.Name).Msg("changelog file disabled by config")
		return
	}
	log.Info().Str("package", rel.Pkg.Name).Str("tag", rel.TagName()).Str("channel", rel.Channel).
		Msg("changelog entry skipped: the release's channel is not in changelog.channels")
}

// NoteEntry adds a note inside the entry a release just wrote, in the file the
// package's own policy targets, and answers the path it changed.
//
// Inside the entry rather than above it, and after the header line rather than
// before it, because the header is what every re-run recognises an existing
// entry by (see HasEntry). A note that moved or split that line would make the
// next run write the entry a second time.
//
// A package whose policy wrote no entry gets no note: there is nothing to
// annotate, and creating a file to hold a note about a release it does not
// record would be worse than silence. The same goes for a file that somehow
// does not carry the entry.
func NoteEntry(rel *plan.Release, note string) (string, bool, error) {
	spec := rel.Pkg.Changelog
	if !spec.Records(rel.Channel) {
		return "", false, nil
	}
	w := &FileWriter{File: spec.File}
	path := w.path(rel)
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("changelog: %w", err)
	}
	marker := "## " + rel.TagName() + " ("
	bom, text := cutBOM(string(existing))
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		// A blockquote, so it reads as an aside about the entry rather than as
		// one of the changes it lists, and so nothing that parses the sections
		// finds a heading it did not write.
		block := []string{"", "> " + strings.ReplaceAll(strings.TrimRight(note, "\n"), "\n", "\n> ")}
		out := append([]string{}, lines[:i+1]...)
		out = append(out, block...)
		out = append(out, lines[i+1:]...)
		mode := os.FileMode(0o644)
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(path, []byte(bom+strings.Join(out, "\n")), mode); err != nil {
			return "", false, fmt.Errorf("changelog: %w", err)
		}
		return path, true, nil
	}
	return "", false, nil
}

// HasEntry reports whether content already carries the release entry for tag:
// a line beginning "## <tag> (". The match is line-anchored so body text that
// merely quotes a header does not count, and the trailing " (" keeps a tag
// that is a prefix of another (core@1.2.0 vs core@1.2.0-beta.1) from matching
// its extension.
//
// A byte-order mark is cut before the match, so a file that opens on its first
// entry is still read as carrying it; CRLF endings need no handling at all,
// since the marker is anchored at the start of the line rather than the end.
// Both matter because this is the idempotence check of the whole record path:
// an entry the check fails to see is an entry written a second time.
func HasEntry(content []byte, tag string) bool {
	marker := "## " + tag + " ("
	_, text := cutBOM(string(content))
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, marker) {
			return true
		}
	}
	return false
}

// utf8BOM is the byte-order mark an editor on Windows leaves at the head of a
// UTF-8 file.
const utf8BOM = "\ufeff"

// cutBOM separates a leading byte-order mark from the rest of a file.
//
// The mark is not content: it belongs before everything, including whatever a
// rewrite puts above the old entries, and a title compared against it would
// fail to match for a reason nobody would find by reading the changelog. It is
// carried through the rewrite rather than dropped, because removing it is a
// change to a file dispat was asked to append to.
func cutBOM(s string) (bom, rest string) {
	if strings.HasPrefix(s, utf8BOM) {
		return utf8BOM, s[len(utf8BOM):]
	}
	return "", s
}

// RenderSections renders the grouped commit sections of a release (breaking
// changes, features, fixes, dependency updates) without any entry header —
// suitable as the body of a GitHub release. It interpolates against the
// release's own variables; RenderBody hands its own lookup down instead, so
// that one entry reads every template against the same values.
func RenderSections(rel *plan.Release, f Format) string {
	return renderSections(rel, f.withDefaults(), ReleaseLookup(rel))
}

// noChangesLine states why an entry carries no sections: the configured
// sentence, or the built-in that names the release's actual cause.
//
// A configured sentence that expands to nothing falls back rather than
// standing: an expansion is empty when a variable it names is not set, which
// is a mistake in the template rather than an instruction to publish an empty
// entry.
//
// Whitespace is nothing. "${A} ${B}" with neither name set expands to a single
// space, which publishes an entry whose only content is a blank line — the
// same mistake, wearing the one character that would let it through an
// emptiness test. The recorder reports the fallback as W241.
func noChangesLine(rel *plan.Release, f Format, look Lookup) string {
	if text := Expand(f.NoChangesText, look); strings.TrimSpace(text) != "" {
		return text + "\n"
	}
	return builtinNoChangesLine(rel)
}

// builtinNoChangesLine states why an entry carries no sections. The ride line
// names the part of the version the group holds in common, so a reader of a
// fixedMajor changelog is not told that the whole version is shared when only
// the major is; the other causes are named in the order that best explains an
// empty body — suppression explains missing sections even when commits exist,
// a pin and a channel move explain a release that never had any.
func builtinNoChangesLine(rel *plan.Release) string {
	switch {
	case rel.FixedRide:
		return "No changes: a version bump to keep the versioning group on " + plan.SharedPartName(rel.SharedDepth()) + ".\n"
	case len(rel.SuppressedNotes) > 0:
		return "No changes: the pending work and its reverts cancel out.\n"
	case rel.Pinned:
		return "No changes: a version set by Release-As.\n"
	case rel.ChannelChanged():
		return "No changes: a channel transition, " + rel.ChannelTransition() + ".\n"
	default:
		return "No changes.\n"
	}
}

// correctionNote annotates a restatement with the records it replaces.
//
// §7.4.2 asks for the restatement to be rendered once, as the carrying unit's
// entry, and allows naming what it corrects. Naming it is what keeps the
// changelog honest: the entry describes work whose original record said
// something else, and a reader chasing the commit behind a line would
// otherwise land on a message that does not match it.
func correctionNote(rel *plan.Release, u *ccme.Unit) string {
	targets := rel.UnitCorrects(u)
	if len(targets) == 0 {
		return ""
	}
	return " (corrects " + strings.Join(targets, ", ") + ")"
}

// RenderBody assembles the body of one entry: the configured header lines,
// the grouped sections, any extra sections the caller appends, and the
// configured footer lines. A nil look means the release's own ReleaseLookup.
//
// The release name is not here. A GitHub release carries it as the release's
// own name, so writing it into the body too would say it twice; the changelog
// entry, which has no such field, adds it through RenderEntryBody.
//
// Blocks are separated by exactly one blank line and empty ones are dropped,
// so an entry reads the same whether none of the optional blocks are
// configured or all of them are — and a body with none of them is byte for
// byte the sections alone, as it was before they existed.
func RenderBody(rel *plan.Release, f Format, look Lookup, extra ...string) string {
	f = f.withDefaults()
	if look == nil {
		look = ReleaseLookup(rel)
	}
	blocks := make([]string, 0, len(extra)+4)
	blocks = appendBlock(blocks, RenderLines(f.Header, rel, look))
	blocks = appendBlock(blocks, renderSections(rel, f, look))
	// The authors block sits after the sections it attributes and before
	// anything the caller appends, so the GitHub recorder's "### Release"
	// details stay the last thing before the footer. The footer staying last
	// is load-bearing beyond taste: self-update reads release notes by cutting
	// at the "---" a release footer conventionally opens with, and a block
	// inserted after it would be read as part of the cut-away tail.
	blocks = appendBlock(blocks, authorsSection(rel, f))
	for _, e := range extra {
		blocks = appendBlock(blocks, e)
	}
	blocks = appendBlock(blocks, RenderLines(f.Footer, rel, look))
	return joinBlocks(blocks)
}

// appendBlock drops empty blocks instead of joining them, so an unconfigured
// header costs no blank line.
func appendBlock(blocks []string, s string) []string {
	if s == "" {
		return blocks
	}
	return append(blocks, s)
}

// joinBlocks puts exactly one blank line between blocks. A block is trimmed to
// a single trailing newline before the separator is added, because what a
// block ends with varies — a section carrying commit bodies ends differently
// from one that does not, and a configured line list may end on a deliberate
// blank line. The last block is left exactly as its renderer produced it.
func joinBlocks(blocks []string) string {
	var b strings.Builder
	for i, block := range blocks {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i == len(blocks)-1 {
			b.WriteString(block)
			continue
		}
		b.WriteString(strings.TrimRight(block, "\n"))
		b.WriteByte('\n')
	}
	return b.String()
}

// RenderEntryBody is what goes under a changelog entry's header line: the
// release-name sub-header, when one is configured, followed by the shared
// body. The entry header is "## ", so its sub-header is "### ".
func RenderEntryBody(rel *plan.Release, f Format, look Lookup) string {
	if look == nil {
		look = ReleaseLookup(rel)
	}
	body := RenderBody(rel, f, look)
	name := Expand(f.ReleaseName, look)
	if name == "" {
		return body
	}
	return joinBlocks(appendBlock([]string{"### " + name + "\n"}, body))
}

// RenderEntry renders one dated changelog entry: a "## pkg@version (date)"
// header followed by the body.
func RenderEntry(rel *plan.Release, date time.Time, f Format) string {
	f = f.withDefaults()
	header := fmt.Sprintf("## %s (%s)\n", rel.TagName(), date.Format(f.DateFormat))
	body := RenderEntryBody(rel, f, nil)
	if body == "" {
		return header
	}
	return header + "\n" + body
}

// FileWriter prepends release entries to a changelog file inside a package.
// It implements release.ReleaseRecorder.
type FileWriter struct {
	File string // file name inside the package folder, default "CHANGELOG.md"
	// FileTitle heads the file, above every entry; an empty list means the
	// default "# Changelog".
	FileTitle []model.EntryLine
	Format    Format
	// EntrySpacing is how many blank lines separate the entry being written
	// from the one below it. 0 means the default, so a writer assembled by
	// hand needs no more configuration than it ever did.
	EntrySpacing int
	Now          func() time.Time // injectable clock; defaults to time.Now
	Log          zerolog.Logger   // carries the entry-exists skip notice; zero value discards
}

// spacing is the writer's blank-line count with the default applied.
func (w *FileWriter) spacing() int {
	if w.EntrySpacing <= 0 {
		return models.DefaultEntrySpacing
	}
	return w.EntrySpacing
}

// title renders the file's opening block for rel, with the default applied.
// The same text heads a new file and is what an existing one is recognised by,
// which is why a title that varies from one release to the next does not
// belong here: a title the recognition misses is read as a preamble the file
// brought with it, and from then on the file keeps the title it had while the
// configured one is never written at all.
func (w *FileWriter) title(rel *plan.Release) string {
	if len(w.FileTitle) == 0 {
		return "# Changelog\n"
	}
	return RenderLines(w.FileTitle, rel, ReleaseLookup(rel))
}

// path resolves the changelog file the writer targets for rel, with the
// file-name default applied.
func (w *FileWriter) path(rel *plan.Release) string {
	file := w.File
	if file == "" {
		file = "CHANGELOG.md"
	}
	return filepath.Join(rel.Pkg.Dir, file)
}

// HasEntryFor reports whether the writer's file already carries the entry
// for rel's planned tag — what Record's own skip checks, exposed so a caller
// can tell a fresh write from a skip. A missing file has no entries.
func (w *FileWriter) HasEntryFor(rel *plan.Release) (bool, error) {
	existing, err := os.ReadFile(w.path(rel))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("changelog: %w", err)
	}
	return HasEntry(existing, rel.TagName()), nil
}

// Record writes the release entry for rel at the top of the package
// changelog, creating the file when missing.
func (w *FileWriter) Record(_ context.Context, rel *plan.Release) error {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	header := w.title(rel)

	path := w.path(rel)
	entry := RenderEntry(rel, now().UTC(), w.Format)

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("changelog: %w", err)
	}
	// A changelog that already exists keeps its own permissions across the
	// rewrite; only a fresh file gets the default.
	mode := os.FileMode(0o644)
	if err == nil {
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
	}
	if HasEntry(existing, rel.TagName()) {
		// The entry was written earlier — by a `dispat changelog` step in the
		// flow, or by a previous run. Writing again would duplicate it, so
		// this write, wherever it comes from, is a skip.
		w.Log.Warn().Str("code", plan.CodeChangelogEntryExists).
			Str("package", rel.Pkg.Name).Str("tag", rel.TagName()).
			Msg("changelog entry already exists, skipped")
		return nil
	}
	// What the entry's own policy could not do, said once for the entry that
	// is actually about to be written rather than for the skip above it.
	LogRecordPolicy(w.Log, rel, w.Format)

	bom, rest := cutBOM(string(existing))
	parts := w.divide(rest, header)
	if parts.preamble {
		// The file heads with something dispat did not write, so it keeps its
		// own head and the entry goes underneath it. Said once per write,
		// because a configured title that is never written looks from the
		// outside like a title that was ignored.
		w.Log.Debug().Str("package", rel.Pkg.Name).Str("tag", rel.TagName()).Str("path", path).
			Msg("existing changelog keeps its own head, the configured title is not written")
	}

	// The entry is closed on exactly one newline and the seam below it is
	// exactly the configured number of blank lines. Left to the entry's own
	// tail the seam varied with what the last section happened to be — a
	// dependencies list ended one way and a section of bodiless bullets
	// another — so a file's spacing recorded the shape of each release rather
	// than one rule.
	var b strings.Builder
	b.WriteString(bom)
	if top := strings.TrimRight(parts.top, "\r\n"); top != "" {
		b.WriteString(top)
		b.WriteString("\n")
		b.WriteString(strings.Repeat("\n", parts.blank))
	}
	b.WriteString(strings.TrimRight(entry, "\n"))
	b.WriteString("\n")
	if parts.body != "" {
		b.WriteString(strings.Repeat("\n", w.spacing()))
		b.WriteString(parts.body)
	}
	// The write replaces the whole file after the publish already happened; an
	// interrupted plain write here would take the package's history with it,
	// so it goes through the atomic replace.
	if err := fsx.WriteFileAtomic(path, []byte(b.String()), mode); err != nil {
		return fmt.Errorf("changelog: %w", err)
	}
	return nil
}

// fileParts is how an existing changelog divides for a rewrite: what stays
// above the new entry, how far below it the entry sits, and what the entry is
// written above. Only the top of a file dispat wrote is ever re-rendered;
// everything else is the file's own bytes.
type fileParts struct {
	top   string // the rendered title, or the file's own preamble
	blank int    // blank lines between top and the new entry
	body  string // from the first entry heading down, untouched
	// preamble records that top is the file's own head rather than the
	// rendered title, so the writer can say so once per write.
	preamble bool
}

// divide splits an existing changelog into what stays above the new entry and
// what the new entry is written above.
//
// Two shapes reach here, and the invariant is the same for both: content that
// predates dispat is preserved, and it is preserved *where it is*.
//
// A file dispat has been writing heads with the title dispat renders. The
// title is stripped and written again, the entry goes one blank line under it,
// and the entries below are untouched — what the writer has always done.
//
// A file dispat did not write heads with something else: YAML front matter, a
// title in somebody else's words, a badge row, a paragraph of introduction.
// All of it is a preamble, ending at the first line that opens an entry
// heading, and it stays at the top of the file with the new entry inserted
// below it. Prepending above it — which is what the writer used to do —
// published a second H1 over the file's own and pushed front matter off the
// head of the file, where it stops being front matter at all. No title is
// written in that case either: the file already has one, in its own words, and
// adding dispat's would say it twice.
//
// The preamble's own line endings are left alone; only the blank lines between
// it and the entry are dispat's to write, and those it writes as the rest of
// the entry is written.
func (w *FileWriter) divide(rest, title string) fileParts {
	// Nothing to preserve: a file that is empty or blank is a fresh file, and
	// a fresh file gets the title.
	if strings.TrimSpace(rest) == "" {
		return fileParts{top: title, blank: 1}
	}
	// An empty title is nothing to recognise a file by, and titleMatch answers
	// that it covers the first zero bytes of every file there is. Taken as a
	// match it would strip nothing, write nothing in its place, and insert the
	// entry above the file's own head — the one shape the preamble path exists
	// to prevent. A title renders empty when every line of the configured
	// fileTitle is filtered out for this package, which is a package the
	// configuration deliberately gave no title to.
	if title != "" {
		if n := titleMatch(rest, title); n >= 0 {
			return fileParts{top: title, blank: 1, body: strings.TrimLeft(rest[n:], "\r\n")}
		}
	}
	at := entryHeadingIndex(rest)
	if at < 0 {
		// A changelog with no entry headings at all: every byte of it is
		// preamble, and this release opens the record below it.
		return fileParts{top: rest, blank: w.spacing(), preamble: true}
	}
	return fileParts{top: rest[:at], blank: w.spacing(), body: rest[at:], preamble: at > 0}
}

// titleMatch reports how many bytes at the head of content the rendered title
// covers, -1 when the title does not head it.
//
// The comparison runs line by line so that a file whose endings became CRLF —
// checked out on Windows, or saved by an editor that converts — still matches
// the title dispat renders with "\n". A title the strip fails to see is a
// title the next release writes a second copy of, above the first.
func titleMatch(content, title string) int {
	at := 0
	for _, want := range strings.SplitAfter(title, "\n") {
		if want == "" {
			continue // SplitAfter's empty tail after a final newline
		}
		text, terminated := strings.CutSuffix(want, "\n")
		if !strings.HasPrefix(content[at:], text) {
			return -1
		}
		at += len(text)
		if !terminated {
			continue
		}
		switch {
		case strings.HasPrefix(content[at:], "\r\n"):
			at += 2
		case strings.HasPrefix(content[at:], "\n"):
			at++
		default:
			return -1
		}
	}
	return at
}

// entryHeadingIndex is the offset of the first line opening an entry heading,
// -1 when the content has none. It is where a file's own preamble ends and the
// changelog's records begin. Anchored at the start of a line, so a heading
// quoted inside a commit body — or a "### " section heading, which shares the
// first two characters — is not mistaken for one.
//
// A heading inside a fenced code block is not one either. A preamble that
// explains the file's own shape shows an entry heading as an example, and
// splitting the file there would leave the opening fence above the new entry
// and its closing fence below: the preamble is cut in half and the markdown
// after it never closes.
func entryHeadingIndex(s string) int {
	const heading = "## "
	fence := "" // the marker of the fence currently open, "" outside one
	for at := 0; at < len(s); {
		line := s[at:]
		nl := strings.IndexByte(line, '\n')
		if nl >= 0 {
			line = line[:nl]
		}
		trimmed := strings.TrimSpace(line)
		switch marker := isFence(trimmed); {
		case fence != "":
			if marker == fence {
				fence = ""
			}
		case marker != "":
			fence = marker
		case strings.HasPrefix(line, heading):
			return at
		}
		if nl < 0 {
			return -1
		}
		at += nl + 1
	}
	return -1
}

// isFence reports the marker a line opens or closes a fenced block with, "" for
// a line that is not a fence. The semantics are self-update's own notes parser,
// spelled again here rather than shared: both read the markdown a release
// writes, and a fence one of them sees and the other does not is a file cut in
// a place its author never wrote.
func isFence(trimmed string) string {
	for _, marker := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, marker) {
			return marker
		}
	}
	return ""
}
