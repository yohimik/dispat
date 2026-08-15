package cli

import (
	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/app"
)

// Flag-value grammar for the two shell helpers, kept beside the other
// grammars the controller owns (manifests.go): the controller parses what the
// command line says and answers a mistake with the usage exit, so a malformed
// invocation never first costs a config load.

// parseBranches builds the if/elif/else chain from the leading condition and
// the repeatable flags. The leading condition arrives already built, because
// only the caller knows which of its sources spoke: a positional spec, or a
// flag the environment cannot answer (--changed, --file, --dir).
//
// The pairing is positional: pflag's StringArray keeps each flag's values in
// the order they were given, so the leading condition takes the first --then
// and each --elif takes the next. A count that cannot pair is the one thing
// checked before anything is indexed, because an off-by-one here would run the
// wrong branch rather than fail.
func parseBranches(lead app.Condition, o *options, usage func(string), log zerolog.Logger) ([]app.Branch, bool) {
	thens, elifs := *o.ifThen, *o.ifElif
	if len(thens) == 0 {
		log.Error().Msg("if needs at least one --then: the script the condition runs")
		usage(cmdIf)
		return nil, false
	}
	if len(thens) != len(elifs)+1 {
		log.Error().Int("conditions", len(elifs)+1).Int("then", len(thens)).
			Msg("every condition needs its own --then: one for the leading condition and one for each --elif")
		usage(cmdIf)
		return nil, false
	}
	branches := make([]app.Branch, 0, len(thens))
	branches = append(branches, app.Branch{Cond: lead, Script: thens[0]})
	for i, spec := range elifs {
		c, err := app.ParseCondition(spec)
		if err != nil {
			log.Error().Err(err).Msg("invalid condition")
			return nil, false
		}
		branches = append(branches, app.Branch{Cond: c, Script: thens[i+1]})
	}
	return branches, true
}

// execSubject reads --for into the one subject an exec invocation is about.
// Unset is the top level, which is what an invocation naming nothing gets.
func execSubject(o *options, log zerolog.Logger) (app.Location, bool) {
	if *o.execFor == "" {
		return app.LocationRoot(), true
	}
	subj, err := app.ParseSubject(*o.execFor)
	if err != nil {
		log.Error().Err(err).Msg("invalid --for")
		return app.LocationRoot(), false
	}
	return subj, true
}

// execScriptFrom reads --script-from, which moves the script lookup without
// moving the environment. Unset means the subject is used for both.
func execScriptFrom(o *options, log zerolog.Logger) (*app.Location, bool) {
	if *o.execScriptFrom == "" {
		return nil, true
	}
	from, err := app.ParseSubject(*o.execScriptFrom)
	if err != nil {
		log.Error().Err(err).Msg("invalid --script-from")
		return nil, false
	}
	return &from, true
}

// helperIn reads --in, the folder the chosen script runs in. Unset is nil,
// leaving the script where the invocation stands, which is what every
// invocation gets that does not ask.
func helperIn(o *options, log zerolog.Logger) (*app.Location, bool) {
	if *o.helperIn == "" {
		return nil, true
	}
	in, err := app.ParseLocation(*o.helperIn)
	if err != nil {
		log.Error().Err(err).Msg("invalid --in")
		return nil, false
	}
	return &in, true
}

// checkExecEnv validates --env against the subject before any config is
// loaded. The DISPAT_* variables describe one package's release, so asking for
// them without naming a package is a mistake worth catching here rather than a
// quietly smaller environment discovered inside a script.
//
// Only a subject the flags already settled is checked. `--for cwd` is a
// package or it is not, and which one it is takes a workspace to say, so that
// case is left to the same refusal on the app's side once the folder has been
// read. Guessing here would refuse an invocation that is about to be correct.
func checkExecEnv(o *options, subj app.Location, usage func(string), log zerolog.Logger) bool {
	if !app.ValidEnvScope(*o.execEnv) {
		log.Error().Str("env", *o.execEnv).Msgf("unknown --env value (want %s, %s or %s)",
			app.EnvScopeStatic, app.EnvScopeDispat, app.EnvScopeBoth)
		return false
	}
	if app.NeedsPlan(*o.execEnv) && subj.Deferred() && !subj.IsPackage() {
		log.Error().Str("env", *o.execEnv).Msgf(
			"--env %s needs a package: the DISPAT_* variables describe one package's release, so name it with --for pkg:<name>",
			*o.execEnv)
		usage(cmdExec)
		return false
	}
	return true
}
