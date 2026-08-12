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
// the repeatable flags.
//
// The pairing is positional: pflag's StringArray keeps each flag's values in
// the order they were given, so the leading condition takes the first --then
// and each --elif takes the next. A count that cannot pair is the one thing
// checked before anything is indexed, because an off-by-one here would run the
// wrong branch rather than fail.
func parseBranches(cond string, o *options, usage func(string), log zerolog.Logger) ([]app.Branch, bool) {
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
	for i, spec := range append([]string{cond}, elifs...) {
		c, err := app.ParseCondition(spec)
		if err != nil {
			log.Error().Err(err).Msg("invalid condition")
			return nil, false
		}
		branches = append(branches, app.Branch{Cond: c, Script: thens[i]})
	}
	return branches, true
}

// execSubject reads the --for-package / --for-space pair into the one subject
// an exec invocation is about. Naming both is a usage mistake: an invocation
// has one subject, and choosing between them for the user would guess.
func execSubject(o *options, usage func(string), log zerolog.Logger) (app.ExecSubject, bool) {
	pkg, space := *o.execForPackage, *o.execForSpace
	if pkg != "" && space != "" {
		log.Error().Msg("--for-package and --for-space name the same thing two ways: an exec invocation has one subject")
		usage(cmdExec)
		return app.ExecSubjectRoot(), false
	}
	switch {
	case pkg != "":
		return app.ExecSubjectPackage(pkg), true
	case space != "":
		return app.ExecSubjectSpace(space), true
	}
	return app.ExecSubjectRoot(), true
}

// execScriptFrom reads --script-from, which moves the script lookup without
// moving the environment. Unset means the subject is used for both.
func execScriptFrom(o *options, log zerolog.Logger) (*app.ExecSubject, bool) {
	if *o.execScriptFrom == "" {
		return nil, true
	}
	from, err := app.ParseScriptFrom(*o.execScriptFrom)
	if err != nil {
		log.Error().Err(err).Msg("invalid --script-from")
		return nil, false
	}
	return &from, true
}

// checkExecEnv validates --env against the subject before any config is
// loaded. The DISPAT_* variables describe one package's release, so asking for
// them without naming a package is a mistake worth catching here rather than a
// quietly smaller environment discovered inside a script.
func checkExecEnv(o *options, subj app.ExecSubject, usage func(string), log zerolog.Logger) bool {
	if !app.ValidEnvScope(*o.execEnv) {
		log.Error().Str("env", *o.execEnv).Msgf("unknown --env value (want %s, %s or %s)",
			app.EnvScopeStatic, app.EnvScopeDispat, app.EnvScopeBoth)
		return false
	}
	if app.NeedsPlan(*o.execEnv) && !subj.IsPackage() {
		log.Error().Str("env", *o.execEnv).Msgf(
			"--env %s needs a package: the DISPAT_* variables describe one package's release, so name it with --for-package",
			*o.execEnv)
		usage(cmdExec)
		return false
	}
	return true
}
