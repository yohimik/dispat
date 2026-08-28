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
	"github.com/yohimik/dispat/services/dispat/internal/fsx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// Format customises how a release entry is rendered. Zero values fall back to
// defaults, so an empty Format is always valid.
type Format struct {
	DateFormat        string // Go time layout, default "2006-01-02"
	BreakingTitle     string // default "Breaking Changes"
	FeaturesTitle     string // default "Features"
	FixesTitle        string // default "Fixes"
	DependenciesTitle string // default "Dependencies"
	// ReleaseName names the release. In a changelog entry it writes a
	// sub-header under the date line; empty writes none. The GitHub recorder
	// reads it as the release's name instead.
	ReleaseName string
	// Header and Footer bracket the sections of an entry. They carry their
	// own package filters, so one configured list serves a whole workspace.
	Header []model.EntryLine
	Footer []model.EntryLine

	// The authors policy, resolved from the package's record format. Placement
	// defaults to "off", which is what keeps an entry byte for byte what it
	// was before attribution existed.
	AuthorsPlacement string
	AuthorsFormat    string
	AuthorsCommits   string
	AuthorsInclude   []string
	AuthorsExclude   []string
	AuthorsTitle     string
}

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
	return f
}

func defaultStr(s *string, def string) {
	if *s == "" {
		*s = def
	}
}

// SpecFormat maps a package's resolved record format onto the renderer's.
func SpecFormat(f model.RecordFormat) Format {
	return Format{
		DateFormat:        f.DateFormat,
		BreakingTitle:     f.BreakingTitle,
		FeaturesTitle:     f.FeaturesTitle,
		FixesTitle:        f.FixesTitle,
		DependenciesTitle: f.DependenciesTitle,
		ReleaseName:       f.ReleaseName,
		Header:            f.Header,
		Footer:            f.Footer,
		AuthorsPlacement:  f.AuthorsPlacement,
		AuthorsFormat:     f.AuthorsFormat,
		AuthorsCommits:    f.AuthorsCommits,
		AuthorsInclude:    f.AuthorsInclude,
		AuthorsExclude:    f.AuthorsExclude,
		AuthorsTitle:      f.AuthorsTitle,
	}
}

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
	w := &FileWriter{File: spec.File, FileTitle: spec.FileTitle, Format: SpecFormat(spec.Format), Now: d.Now, Log: d.Log}
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

// HasEntry reports whether content already carries the release entry for tag:
// a line beginning "## <tag> (". The match is line-anchored so body text that
// merely quotes a header does not count, and the trailing " (" keeps a tag
// that is a prefix of another (core@1.2.0 vs core@1.2.0-beta.1) from matching
// its extension.
func HasEntry(content []byte, tag string) bool {
	marker := "## " + tag + " ("
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, marker) {
			return true
		}
	}
	return false
}

// RenderSections renders the grouped commit sections of a release (breaking
// changes, features, fixes, dependency updates) without any entry header —
// suitable as the body of a GitHub release.
func RenderSections(rel *plan.Release, f Format) string {
	f = f.withDefaults()
	// A shared-versioning ride has no content to group: one line states that
	// the version moved and nothing else did, in the changelog and in the
	// GitHub release alike.
	if rel.NoChanges() {
		return noChangesLine(rel)
	}
	var parts []string
	// NotesUnits, not Units: a prerelease's entry contains only its own
	// changeset, while a stable release (a graduation included) collects the
	// whole pending window since the last stable tag.
	collect := func(title string, kind ccme.Bump) {
		var lines []string
		for _, c := range rel.NotesUnits() {
			if c.Bump == kind {
				// The attribution follows the correction note: the note is part
				// of what the line says about the work, and who did it comes
				// after what was done.
				lines = append(lines, "- "+c.Header.Description+correctionNote(rel, c)+
					authorSuffix(rel, c, f)+"\n"+c.Body)
			}
		}
		if len(lines) > 0 {
			parts = append(parts, "### "+title+"\n\n"+strings.Join(lines, "\n")+"\n")
		}
	}
	collect(f.BreakingTitle, ccme.BumpMajor)
	collect(f.FeaturesTitle, ccme.BumpMinor)
	collect(f.FixesTitle, ccme.BumpPatch)
	if len(rel.Updates) > 0 {
		// One line per provider whose version this release picks up, carrying
		// the movement — a bare name would leave the reader to hunt the
		// provider's own changelog for what actually changed underneath. On a
		// catch-up From equals To: the provider's version was already out.
		//
		// Updates rather than DueTo, so the section appears whenever a
		// provider moved rather than only when it propagated a bump. A
		// consumer released beside its provider with no caret between them
		// genuinely ships against the new version, and a changelog that says
		// nothing about it is the reader's problem later.
		lines := make([]string, 0, len(rel.Updates))
		for _, u := range rel.Updates {
			lines = append(lines, "- "+u.Name+": "+u.From.String()+" -> "+u.To.String())
		}
		parts = append(parts, "### "+f.DependenciesTitle+"\n\n"+strings.Join(lines, "\n")+"\n")
	}
	// Sections are never empty: a release can be admitted to the plan with
	// nothing to group (a pin, a channel transition, work its reverts cancel
	// out), and a record with an empty body reads as a broken write rather
	// than a deliberate one. The line names the release's actual cause.
	if len(parts) == 0 {
		return noChangesLine(rel)
	}
	return strings.Join(parts, "\n")
}

// noChangesLine states why an entry carries no sections. The ride line names
// the part of the version the group holds in common, so a reader of a
// fixedMajor changelog is not told that the whole version is shared when only
// the major is; the other causes are named in the order that best explains an
// empty body — suppression explains missing sections even when commits exist,
// a pin and a channel move explain a release that never had any.
func noChangesLine(rel *plan.Release) string {
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
	blocks = appendBlock(blocks, RenderSections(rel, f))
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
	Now       func() time.Time // injectable clock; defaults to time.Now
	Log       zerolog.Logger   // carries the entry-exists skip notice; zero value discards
}

// title renders the file's opening block for rel, with the default applied.
// The same text heads a new file and is stripped off an existing one before
// the new entry goes in, which is why a title that varies from one release to
// the next does not belong here: the strip would miss and the old title would
// survive inside the file.
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
	body := strings.TrimPrefix(string(existing), header)
	body = strings.TrimLeft(body, "\n")

	content := header + "\n" + entry
	if body != "" {
		content += "\n" + body
	}
	// The write replaces the whole file after the publish already happened; an
	// interrupted plain write here would take the package's history with it,
	// so it goes through the atomic replace.
	if err := fsx.WriteFileAtomic(path, []byte(content), mode); err != nil {
		return fmt.Errorf("changelog: %w", err)
	}
	return nil
}
