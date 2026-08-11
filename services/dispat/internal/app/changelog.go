package app

import (
	"context"

	"github.com/yohimik/dispat/services/dispat/internal/filter"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
)

// ChangelogOptions selects what Changelog covers and how it writes. The
// override fields, when set, replace the corresponding config values for
// every package of the invocation — an explicit flag beats the layered
// per-package configuration.
type ChangelogOptions struct {
	Filter     filter.Filter // which packages the command covers
	File       string        // overrides changelog.file
	Title      string        // overrides changelog.title
	DateFormat string        // overrides changelog.dateFormat
}

// Changelog writes each covered package's pending changelog entry now — the
// same entry the release stage's recorder would write — so a flow can land
// it inside the release commit. An entry that already exists is a skip
// (W222), which is also what makes the release stage skip entries written
// here.
func (a *App) Changelog(ctx context.Context, opts ChangelogOptions) error {
	pl, err := a.stepPlan(ctx)
	if err != nil {
		return err
	}
	targets, err := a.stepTargets(pl, opts.Filter)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot write the changelog")
		return err
	}
	for _, name := range targets {
		rel := pl.Releases[name]
		spec := rel.Pkg.Changelog
		if !spec.Records(rel.IsPrerelease()) {
			changelog.LogSkip(a.log, spec, rel)
			continue
		}
		w := &changelog.FileWriter{
			File:   firstOf(opts.File, spec.File),
			Title:  firstOf(opts.Title, spec.Title),
			Format: changelog.SpecFormat(spec.Format),
			Log:    a.log,
		}
		if opts.DateFormat != "" {
			w.Format.DateFormat = opts.DateFormat
		}
		already, err := w.HasEntryFor(rel)
		if err != nil {
			a.log.Error().Err(err).Str("package", name).Msg("changelog write failed")
			return err
		}
		if err := w.Record(ctx, rel); err != nil {
			a.log.Error().Err(err).Str("package", name).Msg("changelog write failed")
			return err
		}
		if !already { // Record logged the W222 skip itself otherwise
			a.log.Info().Str("package", name).Str("tag", rel.TagName()).Msg("changelog written")
		}
	}
	return nil
}

// firstOf returns the first non-empty value: the flag override when present,
// the configured value otherwise.
func firstOf(override, configured string) string {
	if override != "" {
		return override
	}
	return configured
}
