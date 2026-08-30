package app

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
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
	FileTitle  string        // overrides changelog.fileTitle, as a single line
	DateFormat string        // overrides changelog.dateFormat
	// ReleaseName overrides changelog.releaseName. It is interpolated like
	// the configured value, so a flow can pass a name its own scripts built.
	ReleaseName string
	// Authors overrides the changelog.authors object, field by field.
	Authors AuthorOptions
}

// AuthorOptions are the authors entry-format overrides the `changelog` and
// `github` step commands share. An empty field means the flag was not given
// and the configured value stands; the two lists replace the configured list
// whole, the way a nearer configuration layer does.
type AuthorOptions struct {
	Placement string
	Format    string
	Commits   string
	Include   []string
	Exclude   []string
	Title     string
}

// validate refuses a bad enum before any planning happens, in the words the
// config file would have been refused in: a flag and a key naming the same
// setting should not fail differently.
func (o AuthorOptions) validate() error {
	for _, e := range []struct{ key, value string }{
		{"placement", o.Placement}, {"format", o.Format}, {"commits", o.Commits},
	} {
		if err := config.ValidateAuthorsEnum(e.key, e.value); err != nil {
			return err
		}
	}
	return nil
}

// apply overlays the flags onto a resolved record format.
func (o AuthorOptions) apply(f model.RecordFormat) model.RecordFormat {
	f.AuthorsPlacement = firstOf(o.Placement, f.AuthorsPlacement)
	f.AuthorsFormat = firstOf(o.Format, f.AuthorsFormat)
	f.AuthorsCommits = firstOf(o.Commits, f.AuthorsCommits)
	f.AuthorsTitle = firstOf(o.Title, f.AuthorsTitle)
	if len(o.Include) > 0 {
		f.AuthorsInclude = o.Include
	}
	if len(o.Exclude) > 0 {
		f.AuthorsExclude = o.Exclude
	}
	return f
}

// Changelog writes each covered package's pending changelog entry now — the
// same entry the release stage's recorder would write — so a flow can land
// it inside the release commit. An entry that already exists is a skip
// (W226), which is also what makes the release stage skip entries written
// here.
func (a *App) Changelog(ctx context.Context, opts ChangelogOptions) error {
	if err := opts.Authors.validate(); err != nil {
		a.log.Error().Err(err).Msg("cannot write the changelog")
		return err
	}
	env, err := a.wireStep(&opts.Window)
	if err != nil {
		return err
	}
	pl, err := a.stepPlan(ctx)
	if err != nil {
		return err
	}
	if err := a.alignStep(pl, env); err != nil {
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
	if !spec.Records(rel.Channel) {
		changelog.LogSkip(w.app.log, spec, rel)
		return nil, nil
	}
	fw := &changelog.FileWriter{
		File:         firstOf(w.opts.File, spec.File),
		FileTitle:    spec.FileTitle,
		EntrySpacing: spec.EntrySpacing,
		Format:       changelog.SpecFormat(w.opts.Authors.apply(spec.Format)),
		Log:          w.app.log,
	}
	// A flag states the whole title, the same way a nearer configuration layer
	// does: one line, no filters, replacing whatever was configured.
	if w.opts.FileTitle != "" {
		fw.FileTitle = []model.EntryLine{{Line: []string{w.opts.FileTitle}}}
	}
	if w.opts.DateFormat != "" {
		fw.Format.DateFormat = w.opts.DateFormat
	}
	if w.opts.ReleaseName != "" {
		fw.Format.ReleaseName = w.opts.ReleaseName
	}
	return func(ctx context.Context) error {
		already, err := fw.HasEntryFor(rel)
		if err != nil {
			return fmt.Errorf("changelog write failed: %w", err)
		}
		if err := fw.Record(ctx, rel); err != nil {
			return fmt.Errorf("changelog write failed: %w", err)
		}
		if !already { // Record logged the W226 skip itself otherwise
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
