// Package changelog renders release entries and prepends them to the
// CHANGELOG.md file inside each package folder.
package changelog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yohimik/monorel/internal/conventional"
	"github.com/yohimik/monorel/internal/gitx"
	"github.com/yohimik/monorel/internal/plan"
)

const header = "# Changelog\n"

// FileWriter prepends release entries to CHANGELOG.md inside a package.
type FileWriter struct {
	Now func() time.Time // injectable clock; defaults to time.Now
}

// Append writes the release entry for rel at the top of the package changelog,
// creating the file when missing.
func (w *FileWriter) Append(rel *plan.Release) error {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	path := filepath.Join(rel.Pkg.Dir, "CHANGELOG.md")
	entry := Render(rel, now().UTC())

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

// Render produces one markdown section for a release.
func Render(rel *plan.Release, date time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s (%s)\n", gitx.TagName(rel.Pkg.Name, rel.Next), date.Format("2006-01-02"))

	section := func(title string, kind conventional.Kind) {
		var lines []string
		for _, c := range rel.Commits {
			if c.Kind == kind {
				lines = append(lines, "- "+c.Description)
			}
		}
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n### %s\n\n%s\n", title, strings.Join(lines, "\n"))
	}
	section("Breaking Changes", conventional.KindBreaking)
	section("Features", conventional.KindFeat)
	section("Fixes", conventional.KindFix)

	if len(rel.DueTo) > 0 {
		fmt.Fprintf(&b, "\n### Dependencies\n\n- updated providers: %s\n", strings.Join(rel.DueTo, ", "))
	}
	return b.String()
}
