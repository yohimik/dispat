package config

// The `runOnly` key: which machines a package's build and its publish may be
// placed on.
//
// It rides the same ladder as `buildOutputs` and `buildPlatforms` beside it
// (root, space, space folder file, package, package folder file) and replaces
// whole, like `tagFormat` and `versioning`: the nearest level that states it
// wins, and a level that states nothing inherits. What it replaces is the
// pair, not one half of it, because a level stating "this package publishes
// here" has said something about its build as well and silence about half a
// pair is not a thing the reader could resolve.
//
// Two rules are enforced, and only the second one needs a ladder to ask. The
// shape and the vocabulary are pkg/models', so a config file and the public
// type cannot come to disagree about what the key accepts. The one rule that
// belongs here is the contradiction: a space that logs in publishes on the
// orchestrator, so a package of that space whose publish is pinned to a
// worker is two statements that cannot both hold.
//
// Every refusal carries DiagnosticExecution, because what the key describes
// is how a run is executed rather than what it releases.

import (
	"fmt"

	public "github.com/yohimik/dispat/pkg/models"
)

// RunOnly is the placement pair under this package's own name, beside the
// other model aliases, so the field tables read as the config language.
type RunOnly = public.RunOnly

// runOnly fills a `runOnly` key at any of the five levels that carry one. The
// expansion lives in pkg/models, and the refusal is given the execution code
// here, where the config language decides what a refused configuration looks
// like to a reader.
func runOnly(dst **RunOnly) setter {
	return func(val any, at string) error {
		out, err := public.NormalizeRunOnly(val, at)
		if err != nil {
			return WithDiagnostic(DiagnosticExecution, err)
		}
		*dst = &out
		return nil
	}
}

// checkRunOnlyAgainstLogin refuses a package whose publish is pinned to a
// worker while its space configures a login.
//
// A login is authentication, and authentication is the one thing that must
// not travel: a space that logs in publishes on the orchestrator, in the
// checkout its verified build outputs were installed into, so that no login
// command, export or credential ever enters a mailbox. A package of such a
// space stating `publish: worker` is therefore not a placement the run could
// honour, and the two statements are refused together rather than one of them
// being quietly preferred, because either of them may be the mistake.
func checkRunOnlyAgainstLogin(label string, placement *RunOnly, login []string) error {
	if len(login) == 0 || placement.ResolvePublish() != public.RunOnlyWorker {
		return nil
	}
	return WithDiagnostic(DiagnosticExecution, fmt.Errorf(
		"%s: runOnly publishes on a worker while the space configures flow.login; "+
			"a space that logs in publishes on the orchestrator, so drop the login or publish on %q or %q",
		label, public.RunOnlyBoth, public.RunOnlyOrchestrator))
}
