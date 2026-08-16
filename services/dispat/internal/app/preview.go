package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// PreviewOptions selects what a preview covers and which record bodies it
// renders. Neither body asked for means the changelog entry, which is what a
// preview has always printed.
type PreviewOptions struct {
	// Filter narrows the packages covered, exactly as it does for a release.
	Filter filter.Filter
	// Changelog renders the changelog entry body, GitHub the GitHub release
	// body. Set together they render both, labelled.
	Changelog bool
	GitHub    bool
}

// bodies reports which record bodies to render, resolving "neither asked for"
// to the changelog alone, and whether the two need labelling.
func (o PreviewOptions) bodies() (changelogBody, githubBody, labelled bool) {
	if !o.Changelog && !o.GitHub {
		return true, false, false
	}
	return o.Changelog, o.GitHub, o.Changelog && o.GitHub
}

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
// next release's changelog entry and GitHub release body would carry. Nothing
// is executed, tagged or written.
//
// The preview covers every package with something pending, in publish order,
// narrowed by the filter — named packages or spaces, or the folder the command
// was invoked from. Empty notes mean no covered package has anything pending.
// The sections follow the release-notes windowing: a pending prerelease
// previews only its own changeset, a pending stable release the whole window
// since the last stable tag.
//
// Each body is rendered under its own entry format, so the configured lines a
// release would write — the ones the release's channel admits included — are
// the lines the preview shows.
func (a *App) Preview(ctx context.Context, opts PreviewOptions) (PreviewResult, error) {
	pl, err := a.plan(ctx)
	if err != nil {
		return PreviewResult{}, err
	}
	a.printDiagnostics(pl)
	if pl.Fatal() {
		a.log.Error().Msg("refusing to preview: the repository cannot produce a correct plan")
		return PreviewResult{}, errors.New("no correct plan exists")
	}
	sel, err := a.selectPackages(a.planWorkspace(pl), opts.Filter)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot preview")
		return PreviewResult{}, err
	}

	// Every covered package with something pending, in publish order, so the
	// combined preview reads in the order the releases would happen.
	var parts []string
	for _, name := range sel.Keep(pl.Order) {
		if notes := a.previewOne(pl.Releases[name], opts); notes != "" {
			parts = append(parts, notes)
		}
	}
	return PreviewResult{Notes: strings.Join(parts, "\n"), Scope: sel.Description}, nil
}

// previewOne renders one package's pending notes; empty when nothing is
// pending.
func (a *App) previewOne(rel *plan.Release, opts PreviewOptions) string {
	format := changelog.SpecFormat(rel.Pkg.Changelog.Format)
	// Whether there is anything to preview is whether the package has a
	// reason to release at all — the sections themselves never render empty,
	// so they cannot be the gate. A workspace-wide footer would otherwise
	// give every unchanged package a body and put it in the preview.
	if !rel.Changed() && !rel.Releasing() {
		return ""
	}
	// The changelog entry's header carries the tag and a date; a preview has
	// no date yet, so it shows the channel movement instead: the transition
	// (§13.10) is the context a reader needs to judge the sections below.
	header := fmt.Sprintf("## %s (%s)\n", rel.TagName(), rel.ChannelTransition())

	wantChangelog, wantGitHub, labelled := opts.bodies()
	blocks := make([]string, 0, 2)
	if wantChangelog {
		blocks = appendPreviewBlock(blocks, labelled, "changelog",
			a.previewChangelogBody(rel, format, opts.Changelog))
	}
	if wantGitHub {
		blocks = appendPreviewBlock(blocks, labelled, "github release", a.previewGitHubBody(rel))
	}
	if len(blocks) == 0 {
		return header
	}
	a.log.Trace().Str("package", rel.Pkg.Name).Str("tag", rel.TagName()).
		Str("channel", rel.Channel).Bool("changelog", wantChangelog).Bool("github", wantGitHub).
		Msg("preview rendered")
	return header + "\n" + strings.Join(blocks, "\n")
}

// previewChangelogBody renders what the changelog entry would carry. Asked for
// by name, a policy that records nothing on this release's channel says so
// rather than showing a body no file would receive; the flagless default shows
// the pending notes, which is the whole point of a bare preview.
func (a *App) previewChangelogBody(rel *plan.Release, format changelog.Format, explicit bool) string {
	if explicit {
		if reason := withheldReason(rel.Pkg.Changelog.Enabled, rel.Pkg.Changelog.Channels, rel.Channel); reason != "" {
			return "changelog entry withheld: " + reason + "\n"
		}
	}
	return changelog.RenderEntryBody(rel, format, nil)
}

// previewGitHubBody renders what the GitHub release body would carry, under
// the github entry format rather than the changelog's.
//
// The release block a run adds from its script outputs is absent here: it is
// built from what publishing exported, and a preview has published nothing.
// Whether the release is created at all also depends on the DISPAT_EXPORT_GITHUB
// export, which no preview can know; the note covers the configured policy,
// which is the part a reader can act on.
func (a *App) previewGitHubBody(rel *plan.Release) string {
	spec := rel.Pkg.GitHub
	if reason := withheldReason(spec.Enabled, spec.Channels, rel.Channel); reason != "" {
		return "github release withheld: " + reason + "\n"
	}
	return changelog.RenderEntryBody(rel, changelog.SpecFormat(spec.Format), nil)
}

// withheldReason says why a record policy would write nothing for a release,
// and is empty when it would write.
func withheldReason(enabled bool, channels []string, channel string) string {
	switch {
	case !enabled:
		return "disabled by config"
	case !model.ChannelsAdmit(channels, channel):
		return fmt.Sprintf("the channels do not admit %s", channel)
	default:
		return ""
	}
}

// appendPreviewBlock adds one rendered body, under a label when the preview
// carries more than one and a reader would otherwise have to guess which is
// which. An empty body contributes nothing, label included.
func appendPreviewBlock(blocks []string, labelled bool, label, body string) []string {
	if body == "" {
		return blocks
	}
	if labelled {
		body = "--- " + label + " ---\n\n" + body
	}
	return append(blocks, body)
}
