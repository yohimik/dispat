package cli

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/services/dispat/internal/app"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// configuredCommand holds the resolved inputs shared by configuration-aware
// commands. Keeping them together prevents paths and loggers from different
// configuration phases being mixed at dispatch.
type configuredCommand struct {
	config *config.File
	root   string
	path   string
	log    zerolog.Logger
}

func (r *runner) logConfiguration(command configuredCommand) {
	cfg, cfgPath, resolvedRoot, log := command.config, command.path, command.root, command.log
	// The first thing worth knowing about any run is which file it read and
	// which folder it decided was the monorepo root, because both are inferred
	// when no flag names them and "it ran with the wrong config" looks exactly
	// like a configuration bug until you can see them. Logged here rather than
	// at resolution because this is the first moment the configured level is
	// known.
	//
	// The counts are the second thing worth knowing, for the same reason: they
	// say what the loader made of the file, and a configuration that read as
	// nothing — a `$ref` that resolved to an empty fragment, a `spaces` object
	// under a key nobody meant — is otherwise a run that finds no work and
	// explains itself no further.
	scale := configScaleOf(cfg)
	log.Debug().
		Str("config", cfgPath).
		Str("root", resolvedRoot).
		Bool("explicitConfig", r.fs.Changed("config")).
		Int("configFiles", len(cfg.SourceFiles)).
		Int("spaces", len(cfg.Spaces)).
		Int("packageEntries", scale.packageEntries).
		Int("scripts", scale.scripts).
		Int("webhooks", scale.webhooks).
		Msg("configuration loaded")
	// Which files a configuration was actually made of is only interesting
	// once it is made of more than one, and then it is the first question:
	// a `$ref` naming the wrong fragment looks exactly like a key nobody
	// wrote.
	for _, file := range cfg.SourceFiles {
		log.Trace().Str("file", file).Msg("configuration file read")
	}
}

func (r *runner) runConfiguredExec(ctx context.Context, command configuredCommand) int {
	cfg, resolvedRoot, log := command.config, command.root, command.log
	// Straight after the config, which is all it needs: no plan unless
	// --env asked for one, and no update check, for the same reason as if.
	code, err := app.NewWorkspace(resolvedRoot, cfg, r.workspace, log).Exec(ctx, r.execOpts)
	if err != nil {
		return 1
	}
	return code
}

func (r *runner) runConfiguredIf(ctx context.Context, command configuredCommand) int {
	cfg, resolvedRoot, log := command.config, command.root, command.log
	// Only --changed, or an --in naming a package, a space or the root,
	// gets this far; every other `if` already ran without reading any of
	// this. The caller dispatches here before the update check: no `if` path
	// may cost a GitHub request, however much else it asked for.
	a := app.NewWorkspace(resolvedRoot, cfg, r.workspace, log)
	dir := r.resolveHelperDir()
	if r.ifIn != nil {
		var err error
		if dir, err = a.ResolveDir(*r.ifIn, dir); err != nil {
			log.Error().Err(err).Msg("invalid --in")
			return 1
		}
	}
	if *r.o.ifChanged {
		// The gate expands the window with consumers and then asks "is the
		// selection among it" — so with everything selected, --consumers
		// cannot change the answer: expanding a set never empties it and
		// never fills an empty one. Refused only here, because whether the
		// invocation folder narrows the selection needs the resolved root.
		if *r.o.consumers && len(*r.o.pkgFilter)+len(*r.o.spaceFilter)+len(*r.o.groupFilter) == 0 &&
			sameDir(r.resolveHelperDir(), resolvedRoot) {
			log.Error().Msg("--consumers expands what the changes reach and cannot change the answer when everything is selected; add --package, --space or --group, or run from inside a package folder")
			r.usage(cmdIf)
			return 2
		}
		sel := filter.Filter{Packages: *r.o.pkgFilter, Spaces: *r.o.spaceFilter,
			Groups: *r.o.groupFilter, Dir: r.resolveHelperDir()}
		names, err := a.ChangedSelection(ctx, app.WindowOptions{
			Filter: sel, Since: *r.o.since, Consumers: *r.o.consumers})
		if err != nil {
			log.Error().Err(err).Msg("cannot evaluate --changed")
			return 1
		}
		r.ifBranches[0].Cond = app.ResolvedCondition("--changed", len(names) > 0)
		log.Debug().Strs("packages", names).Bool("held", len(names) > 0).
			Msg("changed selection resolved")
	}
	return r.runIfIn(dir)
}

func (r *runner) runConfiguredFor(ctx context.Context, command configuredCommand) int {
	cfg, resolvedRoot, log := command.config, command.root, command.log
	// Only a domain source, or an --in naming a package, a space or the
	// root, gets this far; a literal list already ran without reading any of
	// this. The caller dispatches here before the update check: no loop path
	// may cost a GitHub request, however much else it asked for.
	a := app.NewWorkspace(resolvedRoot, cfg, r.workspace, log)
	dir := r.resolveHelperDir()
	if r.forIn != nil {
		var err error
		if dir, err = a.ResolveDir(*r.forIn, dir); err != nil {
			log.Error().Err(err).Msg("invalid --in")
			return 1
		}
	}
	items := literalItems(r.inv.items)
	if r.forDomain != "" {
		// Dir is --root as the user spelled it, so a loop invoked inside a
		// package folder narrows its window to that package exactly as every
		// other command does. It is inert for the three domains whose terms
		// are the source, since explicit terms beat the inference.
		sel := filter.Filter{Packages: *r.o.pkgFilter, Spaces: *r.o.spaceFilter,
			Groups: *r.o.groupFilter, Dir: r.resolveHelperDir()}
		var err error
		if items, err = a.ForItems(ctx, app.ForSelection{Domain: r.forDomain,
			Window: app.WindowOptions{Filter: sel, Since: *r.o.since, Consumers: *r.o.consumers},
		}); err != nil {
			log.Error().Err(err).Msg("cannot resolve what to iterate over")
			return 1
		}
	}
	// The configured shell, which is the whole point of the command: a loop
	// spelled here runs its body through the same shell every other script
	// of this repository runs through.
	return r.runForItems(ctx, dir, items, &script.ShellRunner{Shell: cfg.Shell, Log: log}, log)
}
