package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// PreviewResult is what Preview rendered, plus the scope it rendered for, so
// a caller reporting "nothing pending" can say what it was nothing for.
type PreviewResult struct {
	// Notes are the rendered sections; empty when no covered package has
	// anything pending.
	Notes string
	// Scope names the selection the notes cover, empty when the preview
	// covered the whole monorepo.
	Scope string
}

// Preview computes the plan and renders pending release notes: the
// breaking-changes, features and fixes sections (plus provider updates) the
// next release's changelog entry and GitHub release body would carry, under
// the configured changelog format. Nothing is executed, tagged or written.
//
// The preview covers every package with something pending, in publish order,
// narrowed by the filter — named packages or spaces, or the folder the command
// was invoked from. Empty notes mean no covered package has anything pending.
// The sections follow the release-notes windowing: a pending prerelease
// previews only its own changeset, a pending stable release the whole window
// since the last stable tag.
func (a *App) Preview(ctx context.Context, f filter.Filter) (PreviewResult, error) {
	pl, err := a.plan(ctx)
	if err != nil {
		return PreviewResult{}, err
	}
	a.printDiagnostics(pl)
	if pl.Fatal() {
		a.log.Error().Msg("refusing to preview: the repository cannot produce a correct plan")
		return PreviewResult{}, errors.New("no correct plan exists")
	}
	sel, err := a.selectPackages(a.planWorkspace(pl), f)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot preview")
		return PreviewResult{}, err
	}

	// Every covered package with something pending, in publish order, so the
	// combined preview reads in the order the releases would happen.
	var parts []string
	for _, name := range sel.Keep(pl.Order) {
		if notes := a.previewOne(pl.Releases[name]); notes != "" {
			parts = append(parts, notes)
		}
	}
	return PreviewResult{Notes: strings.Join(parts, "\n"), Scope: sel.Description}, nil
}

// previewOne renders one package's pending notes; empty when nothing is
// pending.
func (a *App) previewOne(rel *plan.Release) string {
	sections := changelog.RenderSections(rel, changelog.SpecFormat(rel.Pkg.Changelog.Format))
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
