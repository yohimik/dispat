// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// tagFinder is the optional Taggerx extension that looks one tag HEAD
// reaches up by its exact name; *gitx.LocalGitx implements it. With it a
// release tag costs one git process when the name is new, which is nearly
// always: the create-only write is the existence check, and only a refused
// write asks which tag is in the way. It takes precedence over tagInspector,
// which lists every tag of the package HEAD reaches before every write.
type tagFinder interface {
	FindTag(ctx context.Context, name string) (gitx.Tag, bool, error)
	TagExists(ctx context.Context, name string) (bool, error)
	ResolveCommit(ctx context.Context, rev string) (string, error)
}

// tagWrite is one release tag CreateReleaseTagAs writes: its name, and
// whether the run forces tags.
type tagWrite struct {
	name     string
	isForced bool
}

// createFoundTag is CreateReleaseTagAs for a tagger that finds a tag by its
// exact name, with the same outcomes as the listing path. The write goes
// first and create-only, forced or not, since a new name is the ordinary
// case. When git refuses it, the tag in the way decides: one HEAD reaches at
// the release's target commit is the early-tag skip (W223), one HEAD reaches
// elsewhere is left where it is (E221), and one HEAD cannot reach, which no
// baseline ever saw, is rewritten under force and is the write's own failure
// without (configuration/records.md, Force). A write refused with no tag of
// that name anywhere failed for a reason of its own, and is reported as it
// is rather than tried again.
func createFoundTag(ctx context.Context, finder tagFinder, tagger Taggerx, rel *plan.Release, write tagWrite, log zerolog.Logger) error {
	target := rel.ExportedCommit()
	message := "release " + write.name
	err := writeTag(ctx, tagger, false, write.name, message, target)
	if err == nil {
		createAliasTags(ctx, tagger, rel, log)
		return nil
	}
	existing, found, findErr := finder.FindTag(ctx, write.name)
	switch {
	case findErr != nil:
		// Not a reason to fail a package that has published, but the reason
		// the refusal is all there is to report, so it is said out loud.
		log.Warn().Err(findErr).Str("tag", write.name).
			Msg("existing tags could not be listed after tagging was refused")
	case found:
		return existingTagOutcome(ctx, finder, existing, target, log)
	}
	if !write.isForced {
		return err
	}
	if isPresent, existsErr := finder.TagExists(ctx, write.name); existsErr != nil || !isPresent {
		return err
	}
	if err := writeTag(ctx, tagger, true, write.name, message, target); err != nil {
		return err
	}
	createAliasTags(ctx, tagger, rel, log)
	return nil
}

// existingTagOutcome decides a release tag whose name the repository already
// carries: nil with W223 when it is at the release's target commit (HEAD when
// the release exports none), ErrTagAtOtherCommit otherwise.
func existingTagOutcome(ctx context.Context, finder tagFinder, existing gitx.Tag, target string, log zerolog.Logger) error {
	if target == "" {
		target = "HEAD"
	}
	sha, err := finder.ResolveCommit(ctx, target)
	if err != nil {
		return fmt.Errorf("tag %s already exists and the release target %q cannot be resolved: %w", existing.Name, target, err)
	}
	if sha == existing.Commit {
		log.Warn().Str("code", plan.CodeTagExists).Str("tag", existing.Name).
			Msg("tag already exists at the release commit, skipped")
		return nil
	}
	return fmt.Errorf("%w: %s is at %s, not at the release commit %s",
		ErrTagAtOtherCommit, existing.Name, existing.Commit, sha)
}
