package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// Preview computes the plan and renders pending release notes: the
// breaking-changes, features and fixes sections (plus provider updates) the
// next release's changelog entry and GitHub release body would carry, under
// the configured changelog format. Nothing is executed, tagged or written.
//
// With a package name, the preview covers that one package and an empty
// string means it has nothing pending. With pkg empty, the preview covers
// every package that has something pending, in publish order, and an empty
// string means none does. The sections follow the release-notes windowing: a
// pending prerelease previews only its own changeset, a pending stable
// release the whole window since the last stable tag.
func (a *App) Preview(ctx context.Context, pkg string) (string, error) {
	pl, err := a.plan(ctx)
	if err != nil {
		return "", err
	}
	a.printDiagnostics(pl)
	if pl.Fatal() {
		a.log.Error().Msg("refusing to preview: the repository cannot produce a correct plan")
		return "", errors.New("no correct plan exists")
	}

	if pkg != "" {
		rel := pl.Releases[pkg]
		if rel == nil {
			err := fmt.Errorf("unknown package %q (discovered: %s)", pkg, strings.Join(pl.Order, ", "))
			a.log.Error().Err(err).Msg("unknown package")
			return "", err
		}
		return a.previewOne(rel), nil
	}

	// Every package with something pending, in publish order, so the combined
	// preview reads in the order the releases would happen.
	var parts []string
	for _, name := range pl.Order {
		if notes := a.previewOne(pl.Releases[name]); notes != "" {
			parts = append(parts, notes)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// previewOne renders one package's pending notes; empty when nothing is
// pending.
func (a *App) previewOne(rel *plan.Release) string {
	sections := changelog.RenderSections(rel, entryFormat(a.cfg.Changelog.EntryFormatConfig))
	if sections == "" && !rel.Releasing() {
		return ""
	}
	// The changelog entry's header carries the tag and a date; a preview has
	// no date yet, so it shows the channel movement instead: the transition
	// (§13.10) is the context a reader needs to judge the sections below.
	header := fmt.Sprintf("## %s (%s)\n", rel.TagName(), rel.ChannelTransition())
	if sections == "" {
		return header
	}
	return header + "\n" + sections
}
