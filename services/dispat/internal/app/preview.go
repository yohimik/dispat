package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
)

// Preview computes the plan and renders the pending release notes of one
// package: the breaking-changes, features and fixes sections (plus provider
// updates) its next release's changelog entry and GitHub release body would
// carry, under the configured changelog format. Nothing is executed, tagged
// or written; an empty string means the package has nothing pending. The
// sections follow the release-notes windowing: a pending prerelease previews
// only its own changeset, a pending stable release the whole window since the
// last stable tag.
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
	rel := pl.Releases[pkg]
	if rel == nil {
		err := fmt.Errorf("unknown package %q (discovered: %s)", pkg, strings.Join(pl.Order, ", "))
		a.log.Error().Err(err).Msg("unknown package")
		return "", err
	}

	sections := changelog.RenderSections(rel, entryFormat(a.cfg.Changelog.EntryFormatConfig))
	if sections == "" && !rel.Releasing() {
		return "", nil
	}
	// The changelog entry's header carries the tag and a date; a preview has
	// no date yet, so it shows the channel movement instead — the transition
	// (§13.10) is the context a reader needs to judge the sections below.
	header := fmt.Sprintf("## %s (%s)\n", rel.TagName(), rel.ChannelTransition())
	if sections == "" {
		return header, nil
	}
	return header + "\n" + sections, nil
}
