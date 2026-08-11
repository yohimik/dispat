package app

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// ChangelogOptions selects what Changelog covers and how it writes. The
// override fields, when set, replace the corresponding config values for
// every package of the invocation — an explicit flag beats the layered
// per-package configuration.
type ChangelogOptions struct {
	Window     WindowOptions // which packages the command covers
	OnError    string        // what a failure does to the failed package's dependents
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
	covered, err := a.coveredPackages(ctx, pl, opts.Window)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot write the changelog")
		return err
	}
	_, err = a.sweepStep(ctx, pl, covered, &changelogWork{app: a, opts: opts}, opts.OnError, "changelog")
	return err
}

// changelogWork is `dispat changelog`'s share of a sweep: one package's
// pending entry written into its own file. Each package writes inside its own
// folder, so the sweep runs them on the build budget like any other work.
type changelogWork struct {
	app  *App
	opts ChangelogOptions
}

func (w *changelogWork) stage() string { return "changelog" }

func (w *changelogWork) resolve(_ context.Context, rel *plan.Release) (task, error) {
	if !w.app.releasing(rel) {
		return nil, nil
	}
	spec := rel.Pkg.Changelog
	if !spec.Records(rel.IsPrerelease()) {
		changelog.LogSkip(w.app.log, spec, rel)
		return nil, nil
	}
	fw := &changelog.FileWriter{
		File:   firstOf(w.opts.File, spec.File),
		Title:  firstOf(w.opts.Title, spec.Title),
		Format: changelog.SpecFormat(spec.Format),
		Log:    w.app.log,
	}
	if w.opts.DateFormat != "" {
		fw.Format.DateFormat = w.opts.DateFormat
	}
	return func(ctx context.Context) error {
		already, err := fw.HasEntryFor(rel)
		if err != nil {
			return fmt.Errorf("changelog write failed: %w", err)
		}
		if err := fw.Record(ctx, rel); err != nil {
			return fmt.Errorf("changelog write failed: %w", err)
		}
		if !already { // Record logged the W222 skip itself otherwise
			w.app.log.Info().Str("package", rel.Pkg.Name).Str("tag", rel.TagName()).
				Msg("changelog written")
		}
		return nil
	}, nil
}

// firstOf returns the first non-empty value: the flag override when present,
// the configured value otherwise.
func firstOf(override, configured string) string {
	if override != "" {
		return override
	}
	return configured
}
