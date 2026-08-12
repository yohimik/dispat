package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/config"
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

// subjectKind is which level of the configuration a subject names.
type subjectKind int

const (
	subjectRoot    subjectKind = iota // the top level: no name
	subjectPackage                    // one package
	subjectSpace                      // one space
)

// ExecSubject is the level an invocation is about. The zero value is the top
// level, which is what an invocation naming nothing gets.
type ExecSubject struct {
	kind subjectKind
	name string
}

// IsPackage reports whether the subject is a package, which is what the
// DISPAT_* variables need: they describe one package's release, and a space or
// the top level has no version of its own to report.
func (s ExecSubject) IsPackage() bool { return s.kind == subjectPackage }

// label describes the subject the way an error message needs to.
func (s ExecSubject) label() string {
	switch s.kind {
	case subjectPackage:
		return fmt.Sprintf("package %q", s.name)
	case subjectSpace:
		return fmt.Sprintf("space %q", s.name)
	}
	return "the top level"
}

// ExecOptions is one `dispat exec` invocation.
type ExecOptions struct {
	// Script is the name to look up, not the shell text.
	Script string
	// Subject is what the invocation is about: the script's level and the
	// environment's. The zero value is the top level.
	Subject ExecSubject
	// ScriptFrom overrides where the text is looked up, leaving the
	// environment with Subject. Nil means the subject is used for both.
	ScriptFrom *ExecSubject
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
	// Dir is the working directory: --root, as the user spelled it.
	Dir            string
	Stdout, Stderr io.Writer
	// Runner executes the script. Nil means a ShellRunner on the configured
	// shell; tests pass their own.
	Runner script.Runner
}

// ExecSubjectPackage names a package as the subject of an exec invocation.
func ExecSubjectPackage(name string) ExecSubject {
	return ExecSubject{kind: subjectPackage, name: name}
}

// ExecSubjectSpace names a space.
func ExecSubjectSpace(name string) ExecSubject { return ExecSubject{kind: subjectSpace, name: name} }

// ExecSubjectRoot names the top level, which is also the zero value.
func ExecSubjectRoot() ExecSubject { return ExecSubject{} }

// ParseScriptFrom reads a --script-from value: pkg:<name>, space:<name> or
// root. It lives here rather than in the controller because the subject type
// it produces is the app's.
func ParseScriptFrom(spec string) (ExecSubject, error) {
	if spec == "root" {
		return ExecSubjectRoot(), nil
	}
	kind, name, ok := strings.Cut(spec, ":")
	if !ok || name == "" {
		return ExecSubject{}, fmt.Errorf("invalid --script-from %q (want pkg:<name>, space:<name> or root)", spec)
	}
	switch kind {
	case "pkg":
		return ExecSubjectPackage(name), nil
	case "space":
		return ExecSubjectSpace(name), nil
	}
	return ExecSubject{}, fmt.Errorf("invalid --script-from %q: unknown kind %q (want pkg, space or root)", spec, kind)
}

// Exec resolves the named script and runs it, returning the process exit code.
func (a *App) Exec(ctx context.Context, opts ExecOptions) (int, error) {
	from := opts.Subject
	if opts.ScriptFrom != nil {
		from = *opts.ScriptFrom
	}
	command, err := a.lookupScript(opts.Script, from, opts.Fallback)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot run the script")
		return 1, err
	}
	env, err := a.execEnv(ctx, opts.Subject, opts.Env)
	if err != nil {
		return 1, err
	}
	runner := opts.Runner
	if runner == nil {
		runner = &script.ShellRunner{Shell: a.cfg.Shell}
	}
	log := a.log.With().Str("script", opts.Script).Logger()
	log.Debug().Str("subject", opts.Subject.label()).Str("from", from.label()).Msg("running script")
	return shellCall{
		Runner: runner, Dir: opts.Dir, Script: script.AppendArgs(command, opts.Args), Env: env,
		OnFailure: opts.OnFailure, Stdout: opts.Stdout, Stderr: opts.Stderr, Log: log,
	}.run(ctx)
}

// lookupScript finds the shell text for a name at the given level.
//
// Exact by default: the level named is the only one read, so a script defined
// at the wrong level fails loudly instead of running text from a level nobody
// asked about. --fallback is `dispat run`'s own resolution, the package over
// its space over the top level, and the two report their failure differently
// because "not in that map" and "nowhere in this chain" are different problems.
func (a *App) lookupScript(name string, from ExecSubject, fallback bool) (string, error) {
	levels, err := a.scriptLevels(from, fallback)
	if err != nil {
		return "", err
	}
	key := strings.ToLower(name) // viper lowercases the config's map keys
	for _, l := range levels {
		if cmd, ok := l.scripts[key]; ok {
			return cmd, nil
		}
	}
	if len(levels) == 1 {
		return "", fmt.Errorf("no script %q in %s", name, levels[0].label)
	}
	tried := make([]string, 0, len(levels))
	for _, l := range levels {
		tried = append(tried, l.label)
	}
	return "", fmt.Errorf("no script %q in %s", name, strings.Join(tried, ", nor in "))
}

// scriptLevel is one map a lookup may read, with the phrase an error uses for
// it. Keeping the two together is what stops a resolution site and its message
// from describing different places, the same reasoning as config.scriptScope.
type scriptLevel struct {
	scripts map[string]string
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
func (a *App) scriptLevels(from ExecSubject, fallback bool) ([]scriptLevel, error) {
	root := scriptLevel{a.cfg.Scripts, "the top level"}
	switch from.kind {
	case subjectSpace:
		sc, ok := a.cfg.Space(from.name)
		if !ok {
			return nil, fmt.Errorf("unknown space %q", from.name)
		}
		levels := []scriptLevel{{sc.Scripts, from.label()}}
		if fallback {
			levels = append(levels, root)
		}
		return levels, nil
	case subjectPackage:
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
	pkgs, _, err := config.DiscoverPackages(a.cfg, a.root)
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
func (a *App) execEnv(ctx context.Context, subj ExecSubject, scope string) ([]string, error) {
	if !NeedsPlan(scope) {
		return a.staticEnv(subj)
	}
	if subj.kind != subjectPackage {
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

// declaredEnv is the ExecSubject's declared env as the sorted KEY=value pairs a
// script receives, layered file under space under package.
//
// A package's layers are already merged onto the built model by the package
// build (config.buildSpace), so this reads that rather than merging again: two
// mergers would be two answers to the same question. A space has no built form
// outside a package, so its two layers are merged here through the same
// MergeEnv the build uses.
func (a *App) declaredEnv(subj ExecSubject) ([]string, error) {
	switch subj.kind {
	case subjectSpace:
		sc, ok := a.cfg.Space(subj.name)
		if !ok {
			return nil, fmt.Errorf("unknown space %q", subj.name)
		}
		return config.EnvPairs(config.MergeEnv(a.cfg.Env, sc.Env)), nil
	case subjectPackage:
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

// staticEnv is the declared env of the ExecSubject, expanded the way a release
// expands it.
func (a *App) staticEnv(subj ExecSubject) ([]string, error) {
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
