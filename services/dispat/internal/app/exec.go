package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	public "github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// `dispat exec`: run one declared script, here, once. Unlike `dispat run` it
// computes no plan unless asked, sweeps nothing and consults no dependency
// graph, which is what makes it usable as a step inside another script.
//
// One subject decides everything the command does. It says where the script is
// looked up and whose environment the script gets, the way `npm -w core run
// build` is core's script with core's environment rather than two independent
// choices. --script-from is the one escape hatch, and it moves the lookup
// alone.
//
// The subject, that escape hatch and the working directory are all spelled the
// same way, as a Location: three flags asking where in the monorepo something
// is, answered by one grammar rather than three.

// What --env may ask the subject to contribute.
const (
	// EnvScopeStatic adds the subject's declared env. The default, because a
	// script's declared environment is part of its declaration.
	EnvScopeStatic = "static"
	// EnvScopeDispat adds the DISPAT_* release variables, which costs a plan.
	EnvScopeDispat = "dispat"
	// EnvScopeBoth adds both, which is what the script sees under dispat run.
	EnvScopeBoth = "both"
)

// ValidEnvScope reports whether the value is a known --env scope. It is the
// counterpart of ValidOnError: the controller checks the flag before any
// config is loaded, so a usage mistake never first costs a config error.
func ValidEnvScope(v string) bool {
	return v == EnvScopeStatic || v == EnvScopeDispat || v == EnvScopeBoth
}

// NeedsPlan reports whether an --env scope requires a computed plan, which is
// the only git-touching path in the command.
func NeedsPlan(scope string) bool { return scope == EnvScopeDispat || scope == EnvScopeBoth }

// locationKind is which place in the monorepo a location names.
type locationKind int

const (
	kindRoot    locationKind = iota // the top level: no name
	kindPackage                     // one package
	kindSpace                       // one space
	kindCwd                         // wherever the invocation stands
	kindPath                        // a folder, named outright
)

// Location is a place in the monorepo, spelled the same way wherever one is
// asked for: pkg:<name>, space:<name>, root, cwd, or — where a folder makes
// sense — a path. The zero value is the top level, which is what an invocation
// naming nothing gets.
//
// One vocabulary rather than one per flag. --for and --script-from name a
// level of the configuration, --in names a folder, and the three read the same
// because they are three answers to the same question.
type Location struct {
	kind locationKind
	name string
}

// IsPackage reports whether the location is a package, which is what the
// DISPAT_* variables need: they describe one package's release, and a space or
// the top level has no version of its own to report.
func (l Location) IsPackage() bool { return l.kind == kindPackage }

// Deferred reports whether resolving the location needs the configuration:
// a name has to be looked up, or a folder turned into the level it stands in.
// The controller asks before it decides whether a command must load one.
func (l Location) Deferred() bool {
	return l.kind == kindPackage || l.kind == kindSpace || l.kind == kindRoot
}

// label describes the location the way an error message needs to.
func (l Location) label() string {
	switch l.kind {
	case kindPackage:
		return fmt.Sprintf("package %q", l.name)
	case kindSpace:
		return fmt.Sprintf("space %q", l.name)
	case kindCwd:
		return "the current folder"
	case kindPath:
		return fmt.Sprintf("folder %q", l.name)
	}
	return "the top level"
}

// ExecOptions is one `dispat exec` invocation.
type ExecOptions struct {
	// Script is the name to look up, not the shell text.
	Script string
	// Subject is what the invocation is about: the script's level and the
	// environment's. The zero value is the top level.
	Subject Location
	// ScriptFrom overrides where the text is looked up, leaving the
	// environment with Subject. Nil means the subject is used for both.
	ScriptFrom *Location
	// Fallback resolves the name the way dispat run does, falling back from a
	// package to its space to the top level, instead of the named level alone.
	Fallback bool
	// Env is what the subject contributes: one of the EnvScope constants.
	Env string
	// OnFailure runs when the script fails and decides the exit code.
	OnFailure string
	// Args are the arguments typed after `--`, appended to the resolved
	// command. They reach the script and nothing else: OnFailure is about the
	// failure rather than about the work, so it is left as it was written.
	Args []string
	// Dir is where the invocation stands: --root, as the user spelled it. It
	// is the working directory unless In names another, and it is the folder
	// a cwd location resolves against.
	Dir string
	// In is the working directory, when the invocation named one. Nil leaves
	// the script in Dir, which is what every invocation before --in existed
	// got.
	In             *Location
	Stdout, Stderr io.Writer
	// Runner executes the script. Nil means a ShellRunner on the configured
	// shell; tests pass their own.
	Runner script.Runner
}

// LocationPackage names one package.
func LocationPackage(name string) Location { return Location{kind: kindPackage, name: name} }

// LocationSpace names one space.
func LocationSpace(name string) Location { return Location{kind: kindSpace, name: name} }

// LocationRoot names the top level, which is also the zero value.
func LocationRoot() Location { return Location{} }

// LocationCwd names wherever the invocation stands.
func LocationCwd() Location { return Location{kind: kindCwd} }

// LocationPath names a folder outright.
func LocationPath(path string) Location { return Location{kind: kindPath, name: path} }

// The words the grammar reserves. A folder actually called one of them is
// still reachable, spelled ./root or ./cwd, which is also how a shell tells a
// path from a word.
const (
	locRoot = "root"
	locCwd  = "cwd"
)

// ParseLocation reads the full grammar — pkg:<name>, space:<name>, root, cwd,
// or anything else as a folder path — for the flags a folder makes sense for.
//
// It lives here rather than in the controller because the type it produces is
// the app's, and its errors name no flag: three flags share this grammar, and
// each says which one it was reading.
func ParseLocation(spec string) (Location, error) {
	switch spec {
	case locRoot:
		return LocationRoot(), nil
	case locCwd:
		return LocationCwd(), nil
	}
	kind, name, ok := strings.Cut(spec, ":")
	if !ok {
		// No colon, so nothing claims to be a level: a folder is the only
		// thing left it can be.
		return LocationPath(spec), nil
	}
	if name == "" {
		return Location{}, fmt.Errorf("invalid location %q: %s names nothing", spec, kind)
	}
	switch kind {
	case "pkg":
		return LocationPackage(name), nil
	case "space":
		return LocationSpace(name), nil
	}
	return Location{}, fmt.Errorf("invalid location %q: unknown kind %q (want pkg, space, %s or %s)",
		spec, kind, locRoot, locCwd)
}

// ParseSubject is ParseLocation for the flags that name a level of the
// configuration rather than a folder. A subject decides which scripts map is
// read and whose environment a script gets, and a bare folder answers neither,
// so the path form is refused here instead of being resolved into a level
// nobody named.
func ParseSubject(spec string) (Location, error) {
	loc, err := ParseLocation(spec)
	if err != nil {
		return Location{}, err
	}
	if loc.kind == kindPath {
		return Location{}, fmt.Errorf("invalid location %q (want pkg:<name>, space:<name>, %s or %s)",
			spec, locRoot, locCwd)
	}
	return loc, nil
}

// Exec resolves the named script and runs it, returning the process exit code.
func (a *App) Exec(ctx context.Context, opts ExecOptions) (int, error) {
	// The locations first, because everything below is about a level or a
	// folder and a cwd one is neither until the workspace has been read.
	subject, err := a.ResolveSubject(opts.Subject, opts.Dir)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot run the script")
		return 1, err
	}
	from := subject
	// A --script-from saying what --for already said is the subject over again,
	// resolved once: `--for cwd --script-from cwd` is one question about one
	// folder, and asking it twice would report the answer twice.
	if opts.ScriptFrom != nil && *opts.ScriptFrom != opts.Subject {
		if from, err = a.ResolveSubject(*opts.ScriptFrom, opts.Dir); err != nil {
			a.log.Error().Err(err).Msg("cannot run the script")
			return 1, err
		}
	}
	dir := opts.Dir
	if opts.In != nil {
		if dir, err = a.ResolveDir(*opts.In, opts.Dir); err != nil {
			a.log.Error().Err(err).Msg("cannot run the script")
			return 1, err
		}
	}
	commands, err := a.lookupScript(opts.Script, from, opts.Fallback)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot run the script")
		return 1, err
	}
	env, err := a.execEnv(ctx, subject, opts.Env)
	if err != nil {
		return 1, err
	}
	runner := opts.Runner
	if runner == nil {
		runner = &script.ShellRunner{Shell: a.cfg.Shell, Log: a.log}
	}
	log := a.log.With().Str("script", opts.Script).Logger()
	log.Debug().Str("subject", subject.label()).Str("from", from.label()).
		Str("dir", dir).Msg("running script")
	return shellCall{
		Runner: runner, Dir: dir, Scripts: script.AppendArgsToLast(commands, opts.Args), Env: env,
		OnFailure: opts.OnFailure, Stdout: opts.Stdout, Stderr: opts.Stderr, Log: log,
	}.run(ctx)
}

// ResolveSubject turns a location into the level of the configuration it
// names. Every kind but cwd already is one; cwd is the folder the invocation
// stands in, read through the same rule `dispat run` reads it with, so
// standing somewhere means one thing across the whole CLI.
//
// A folder inside no package and no space resolves to the top level. That is
// the widest answer rather than a refusal, matching the filter's own reading of
// a folder that stands for nothing, and it is said out loud because the
// invocation asked for a narrower one.
func (a *App) ResolveSubject(loc Location, dir string) (Location, error) {
	if loc.kind != kindCwd {
		return loc, nil
	}
	pkgs, err := a.packages()
	if err != nil {
		return Location{}, err
	}
	at := filter.Locate(dir, a.discoveredWorkspace(pkgs))
	switch {
	case at.Package != "":
		a.log.Debug().Str("dir", dir).Str("subject", at.Package).Msg("current folder resolved to a package")
		return LocationPackage(at.Package), nil
	case at.Space != "":
		a.log.Debug().Str("dir", dir).Str("subject", at.Space).Msg("current folder resolved to a space")
		return LocationSpace(at.Space), nil
	}
	a.log.Info().Str("dir", dir).Msg("the current folder is in no package and no space, using the top level")
	return LocationRoot(), nil
}

// PlainDir resolves the locations a folder alone can answer: cwd, which is
// where the invocation stands, and a path, which the command line already
// spelled in full. Neither needs a configuration, which is what lets
// `dispat if --in ./build` keep the command's promise to read nothing.
//
// A location naming a level is refused rather than guessed at. The caller
// asked for the config-free half knowing which half it had.
func PlainDir(loc Location, dir string) (string, error) {
	if loc.Deferred() {
		return "", fmt.Errorf("%s cannot be resolved without a configuration", loc.label())
	}
	return checkDir(loc, plainDir(loc, dir))
}

// plainDir is the folder arithmetic, without the check.
func plainDir(loc Location, dir string) string {
	if loc.kind == kindPath {
		if filepath.IsAbs(loc.name) {
			return loc.name
		}
		// Relative to where the invocation stands, not to the monorepo root:
		// --in is a folder the caller is pointing at, and they point from
		// where they are.
		return filepath.Join(dir, filepath.FromSlash(loc.name))
	}
	return dir
}

// checkDir refuses a folder that is not there, or is not a folder, before any
// script is handed to a shell. Left to the shell it would surface as whatever
// that shell says about a working directory it cannot enter, naming neither
// the flag nor the value that was wrong.
func checkDir(loc Location, dir string) (string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("cannot run in %s: %w", loc.label(), err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cannot run in %s: %s is not a folder", loc.label(), dir)
	}
	return dir, nil
}

// ResolveDir turns any location into the folder a script runs in, including
// the ones only the configuration can place.
func (a *App) ResolveDir(loc Location, dir string) (string, error) {
	out, err := a.locationDir(loc, dir)
	if err != nil {
		return "", err
	}
	if out, err = checkDir(loc, out); err != nil {
		return "", err
	}
	a.log.Debug().Str("in", loc.label()).Str("dir", out).Msg("working directory resolved")
	return out, nil
}

// locationDir is ResolveDir without the check, so the two concerns stay apart.
func (a *App) locationDir(loc Location, dir string) (string, error) {
	switch loc.kind {
	case kindPackage:
		p, err := a.discoverPackage(loc.name)
		if err != nil {
			return "", err
		}
		return p.Dir, nil
	case kindSpace:
		sc, ok := a.cfg.Space(loc.name)
		if !ok {
			return "", fmt.Errorf("unknown space %q", loc.name)
		}
		// The same join discovery uses for a space's primary folder — the
		// first configured path — so a space means one folder whoever asks.
		return filepath.Join(a.root, filepath.FromSlash(sc.Path.First())), nil
	case kindRoot:
		return a.root, nil
	}
	return plainDir(loc, dir), nil
}

// lookupScript finds the shell text for a name at the given level: the one
// command it binds, or the sequence of them.
//
// Exact by default: the level named is the only one read, so a script defined
// at the wrong level fails loudly instead of running text from a level nobody
// asked about. --fallback is `dispat run`'s own resolution, the package over
// its space over the top level, and the two report their failure differently
// because "not in that map" and "nowhere in this chain" are different problems.
func (a *App) lookupScript(name string, from Location, fallback bool) (public.Script, error) {
	levels, err := a.scriptLevels(from, fallback)
	if err != nil {
		return nil, err
	}
	key := strings.ToLower(name) // the config's map keys arrive lowercased
	for _, l := range levels {
		if cmds, ok := l.scripts[key]; ok {
			return cmds, nil
		}
	}
	if len(levels) == 1 {
		return nil, fmt.Errorf("no script %q in %s", name, levels[0].label)
	}
	tried := make([]string, 0, len(levels))
	for _, l := range levels {
		tried = append(tried, l.label)
	}
	return nil, fmt.Errorf("no script %q in %s", name, strings.Join(tried, ", nor in "))
}

// scriptLevel is one map a lookup may read, with the phrase an error uses for
// it. Keeping the two together is what stops a resolution site and its message
// from describing different places, the same reasoning as config.scriptScope.
type scriptLevel struct {
	scripts map[string]public.Script
	label   string
}

// scriptLevels lists the maps to read, nearest first.
//
// A package's own map comes from the built model rather than from the config
// maps directly: a package declares scripts across four layers, two of which
// live in files only discovery reads, and the layer fold is the one place that
// knows all four. --fallback then skips straight to the package's effective
// map, which is already every layer plus its space's and the top level's, so
// the layered case reads exactly what `dispat run` would resolve.
func (a *App) scriptLevels(from Location, fallback bool) ([]scriptLevel, error) {
	root := scriptLevel{a.cfg.Scripts, "the top level"}
	switch from.kind {
	case kindSpace:
		sc, ok := a.cfg.Space(from.name)
		if !ok {
			return nil, fmt.Errorf("unknown space %q", from.name)
		}
		levels := []scriptLevel{{sc.Scripts, from.label()}}
		if fallback {
			levels = append(levels, root)
		}
		return levels, nil
	case kindPackage:
		p, err := a.discoverPackage(from.name)
		if err != nil {
			return nil, err
		}
		if !fallback {
			return []scriptLevel{{p.OwnScripts, from.label()}}, nil
		}
		return []scriptLevel{
			{p.OwnScripts, from.label()},
			{p.Space.Scripts, "its space or the top level"},
		}, nil
	}
	return []scriptLevel{root}, nil
}

// discoverPackage finds one package in the workspace by name,
// case-insensitively.
func (a *App) discoverPackage(name string) (*model.Package, error) {
	pkgs, err := a.packages()
	if err != nil {
		return nil, err
	}
	for _, p := range pkgs {
		if strings.EqualFold(p.Name, name) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("unknown package %q", name)
}

// execEnv builds what the subject contributes, per the --env scope.
//
// Nothing is computed for a scope that did not ask for it: the default adds the
// declared env from the configuration alone, and only dispat and both reach for
// a plan. That is the whole performance claim of the command, so the plan sits
// behind this check and nowhere earlier.
func (a *App) execEnv(ctx context.Context, subj Location, scope string) ([]string, error) {
	if !NeedsPlan(scope) {
		return a.staticEnv(subj)
	}
	if subj.kind != kindPackage {
		err := fmt.Errorf("--env %s needs a package: the DISPAT_* variables describe one package's release, and %s has no version of its own", scope, subj.label())
		a.log.Error().Err(err).Msg("cannot build the environment")
		return nil, err
	}
	pl, err := a.stepPlan(ctx)
	if err != nil {
		return nil, err
	}
	name, err := a.plannedPackage(pl, subj.name)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot build the environment")
		return nil, err
	}
	env := release.CommandEnv(pl, name, "exec", release.WorkspaceEnv(pl, a.log))
	if scope == EnvScopeBoth {
		// CommandEnv already layers the declared env underneath the computed
		// one, so both is what it returns as it stands.
		return env, nil
	}
	return withoutStatic(env, pl.Releases[name].Pkg.Space.Env), nil
}

// declaredEnv is the Location's declared env as the sorted KEY=value pairs a
// script receives, layered file under space under package.
//
// A package's layers are already merged onto the built model by the package
// build (config.buildSpace), so this reads that rather than merging again: two
// mergers would be two answers to the same question. A space has no built form
// outside a package, so its two layers are merged here through the same
// MergeEnv the build uses.
func (a *App) declaredEnv(subj Location) ([]string, error) {
	switch subj.kind {
	case kindSpace:
		sc, ok := a.cfg.Space(subj.name)
		if !ok {
			return nil, fmt.Errorf("unknown space %q", subj.name)
		}
		return config.EnvPairs(config.MergeEnv(a.cfg.Env, sc.Env)), nil
	case kindPackage:
		p, err := a.discoverPackage(subj.name)
		if err != nil {
			return nil, err
		}
		return p.Space.Env, nil
	}
	return config.EnvPairs(a.cfg.Env), nil
}

// plannedPackage resolves a package name against the plan, case-insensitively,
// and refuses one the plan has no entry for rather than letting a nil release
// reach the environment renderers.
func (a *App) plannedPackage(pl *plan.Plan, name string) (string, error) {
	for pkg, rel := range pl.Releases {
		if rel != nil && strings.EqualFold(pkg, name) {
			return pkg, nil
		}
	}
	return "", fmt.Errorf("package %q is not in the release plan, so it has no DISPAT_* variables", name)
}

// staticEnv is the declared env of the Location, expanded the way a release
// expands it.
func (a *App) staticEnv(subj Location) ([]string, error) {
	static, err := a.declaredEnv(subj)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot build the environment")
		return nil, err
	}
	// No computed set to protect here, so StaticEnv is doing the expansion
	// alone: $NAME in a declared value resolves against the process
	// environment, exactly as it would inside a release.
	return release.StaticEnv(static, nil), nil
}

// withoutStatic drops the declared pairs from a computed environment, which is
// what --env dispat asks for: the release variables without the configuration's
// own. A declared name can never collide with a computed one, since the config
// refuses the DISPAT_ prefix, so removing by name is exact.
func withoutStatic(env, static []string) []string {
	if len(static) == 0 {
		return env
	}
	drop := make(map[string]bool, len(static))
	for _, pair := range static {
		name, _, _ := strings.Cut(pair, "=")
		drop[name] = true
	}
	out := env[:0:0]
	for _, pair := range env {
		name, _, _ := strings.Cut(pair, "=")
		if !drop[name] {
			out = append(out, pair)
		}
	}
	return out
}
