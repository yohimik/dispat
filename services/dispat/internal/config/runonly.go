package config

// The `runOnly` key: which machines may run a package's build and related
// computation. Publication always stays on the orchestrator.
//
// It rides the same ladder as `buildOutputs` and `buildPlatforms` beside it
// (root, space, space folder file, package, package folder file) and replaces
// whole, like `tagFormat` and `versioning`: the nearest level that states it
// wins, and a level that states nothing inherits. What it replaces is the
// pair, not one half of it, because a level stating "this package publishes
// here" has said something about its build as well and silence about half a
// pair is not a thing the reader could resolve.
//
// The public parser refuses choreographer publication in files. The resolved-space
// check below also refuses typed configurations assembled without that parser.
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

// checkRunOnlyPublishPlacement refuses a typed configuration whose publication
// is pinned to a choreographer, including one constructed without parsing a file.
func checkRunOnlyPublishPlacement(label string, placement *RunOnly) error {
	if placement.ResolvePublish() != public.RunOnlyWorker {
		return nil
	}
	return WithDiagnostic(DiagnosticExecution, fmt.Errorf(
		"%s: publication runs on the orchestrator; set publish to orchestrator or both "+
			"(use [choreographer, orchestrator] for choreographer builds)", label))
}
