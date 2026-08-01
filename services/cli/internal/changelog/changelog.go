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

	"github.com/yohimik/dispat/internal/conventional"
	"github.com/yohimik/dispat/internal/gitx"
	"github.com/yohimik/dispat/internal/plan"
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

// RenderSections renders the grouped commit sections of a release (breaking
// changes, features, fixes, dependency updates) without any entry header —
// suitable as the body of a GitHub release.
func RenderSections(rel *plan.Release, f Format) string {
	f = f.withDefaults()
	var parts []string
	collect := func(title string, kind conventional.Kind) {
		var lines []string
		for _, c := range rel.Commits {
			if c.Kind == kind {
				lines = append(lines, "- "+c.Description)
			}
		}
		if len(lines) > 0 {
			parts = append(parts, "### "+title+"\n\n"+strings.Join(lines, "\n")+"\n")
		}
	}
	collect(f.BreakingTitle, conventional.KindBreaking)
	collect(f.FeaturesTitle, conventional.KindFeat)
	collect(f.FixesTitle, conventional.KindFix)
	if len(rel.DueTo) > 0 {
		parts = append(parts, "### "+f.DependenciesTitle+"\n\n- updated providers: "+strings.Join(rel.DueTo, ", ")+"\n")
	}
	return strings.Join(parts, "\n")
}

// RenderEntry renders one dated changelog entry: a "## pkg@version (date)"
// header followed by the sections.
func RenderEntry(rel *plan.Release, date time.Time, f Format) string {
	f = f.withDefaults()
	header := fmt.Sprintf("## %s (%s)\n", gitx.TagName(rel.Pkg.Name, rel.Next), date.Format(f.DateFormat))
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
}

// Record writes the release entry for rel at the top of the package
// changelog, creating the file when missing.
func (w *FileWriter) Record(_ context.Context, rel *plan.Release) error {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	file := w.File
	if file == "" {
		file = "CHANGELOG.md"
	}
	title := w.Title
	if title == "" {
		title = "# Changelog"
	}
	header := title + "\n"

	path := filepath.Join(rel.Pkg.Dir, file)
	entry := RenderEntry(rel, now().UTC(), w.Format)

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("changelog: %w", err)
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
