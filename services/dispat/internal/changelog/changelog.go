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
}

func (f Format) withDefaults() Format {
	defaultStr(&f.DateFormat, "2006-01-02")
	defaultStr(&f.BreakingTitle, "Breaking Changes")
	defaultStr(&f.FeaturesTitle, "Features")
	defaultStr(&f.FixesTitle, "Fixes")
	defaultStr(&f.DependenciesTitle, "Dependencies")
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
// whose changelog is disabled records nothing.
func (d *Dispatcher) Record(ctx context.Context, rel *plan.Release) error {
	spec := rel.Pkg.Changelog
	if !spec.Enabled {
		d.Log.Debug().Str("package", rel.Pkg.Name).Msg("changelog file disabled by config")
		return nil
	}
	w := &FileWriter{File: spec.File, Title: spec.Title, Format: SpecFormat(spec.Format), Now: d.Now, Log: d.Log}
	return w.Record(ctx, rel)
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
	// GitHub release alike. It names the part of the version the group holds
	// in common, so a reader of a fixedMajor changelog is not told that the
	// whole version is shared when only the major is.
	if rel.NoChanges() {
		return "No changes — version bump to keep the versioning group on " + plan.SharedPartName(rel.SharedDepth()) + ".\n"
	}
	var parts []string
	// NotesUnits, not Units: a prerelease's entry contains only its own
	// changeset, while a stable release (a graduation included) collects the
	// whole pending window since the last stable tag.
	collect := func(title string, kind ccme.Bump) {
		var lines []string
		for _, c := range rel.NotesUnits() {
			if c.Bump == kind {
				lines = append(lines, "- "+c.Header.Description+"\n"+c.Body)
			}
		}
		if len(lines) > 0 {
			parts = append(parts, "### "+title+"\n\n"+strings.Join(lines, "\n")+"\n")
		}
	}
	collect(f.BreakingTitle, ccme.BumpMajor)
	collect(f.FeaturesTitle, ccme.BumpMinor)
	collect(f.FixesTitle, ccme.BumpPatch)
	if len(rel.DueTo) > 0 {
		// One line per provider, carrying the version movement the consumer
		// picks up — a bare name would leave the reader to hunt the provider's
		// own changelog for what actually changed underneath. On a catch-up
		// From equals To: the provider's version was already out.
		var lines []string
		for _, u := range rel.Updates {
			lines = append(lines, "- "+u.Name+": "+u.From.String()+" -> "+u.To.String())
		}
		if len(lines) == 0 { // no version data (e.g. a hand-built Release): names alone
			for _, name := range rel.DueTo {
				lines = append(lines, "- "+name)
			}
		}
		parts = append(parts, "### "+f.DependenciesTitle+"\n\n"+strings.Join(lines, "\n")+"\n")
	}
	return strings.Join(parts, "\n")
}

// RenderEntry renders one dated changelog entry: a "## pkg@version (date)"
// header followed by the sections.
func RenderEntry(rel *plan.Release, date time.Time, f Format) string {
	f = f.withDefaults()
	header := fmt.Sprintf("## %s (%s)\n", rel.TagName(), date.Format(f.DateFormat))
	sections := RenderSections(rel, f)
	if sections == "" {
		return header
	}
	return header + "\n" + sections
}

// FileWriter prepends release entries to a changelog file inside a package.
// It implements release.ReleaseRecorder.
type FileWriter struct {
	File   string // file name inside the package folder, default "CHANGELOG.md"
	Title  string // first line of the file, default "# Changelog"
	Format Format
	Now    func() time.Time // injectable clock; defaults to time.Now
	Log    zerolog.Logger   // carries the entry-exists skip notice; zero value discards
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
	title := w.Title
	if title == "" {
		title = "# Changelog"
	}
	header := title + "\n"

	path := w.path(rel)
	entry := RenderEntry(rel, now().UTC(), w.Format)

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("changelog: %w", err)
	}
	if HasEntry(existing, rel.TagName()) {
		// The entry was written earlier — by a `dispat changelog` step in the
		// flow, or by a previous run. Writing again would duplicate it, so
		// this write, wherever it comes from, is a skip.
		w.Log.Info().Str("code", plan.CodeChangelogEntryExists).
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
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("changelog: %w", err)
	}
	return nil
}
