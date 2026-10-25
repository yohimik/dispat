package models

import (
	"encoding/json"
	"fmt"
)

// This file is the `runOnly` key: where a package's build may run. Publication
// stays on the orchestrator, so the second value cannot name a worker.
//
// Some work must never leave the machine that holds the trust. A build whose
// artefact is signed, a publish that logs in, a stage that reads a credential
// no other machine has: an operator has to be able to say so, per package,
// rather than hoping the scheduler happens to place it here. The key says it
// once and the placement obeys it.
//
// It carries one value for both placements, or one per stage, exactly
// as `concurrency` does:
//
//	runOnly: orchestrator          # build and publish stay here
//	runOnly: [worker, orchestrator] # build on a node, publish here
//
// `build` and `publish` are the two values because they are the two placements
// a run has: the test stage and the stages a flow defines before the records
// follow the build's value. The version stage, the lock-file preparation, the
// space login, the recording, the stages after the records and every run-level
// hook always run on the orchestrator, so there is nothing for this key to say
// about them and it says nothing.

// The three places a stage may be allowed to run. They are matched exactly,
// as the execution role and logLevel are, so a misspelling is refused rather
// than guessed at: a value read as the default would place signing work on a
// machine the operator meant to exclude.
const (
	// RunOnlyBoth is the default: builds may run on either node type, while
	// publication remains on the orchestrator.
	RunOnlyBoth = "both"
	// RunOnlyWorker pins the stage to a worker node. A run with no worker
	// links cannot execute such a stage at all and is refused before it
	// starts, rather than running the work in the one place it was told not
	// to.
	RunOnlyWorker = "worker"
	// RunOnlyOrchestrator pins the stage to the machine the release was
	// started on, which is where the credentials, the locks and the records
	// are.
	RunOnlyOrchestrator = "orchestrator"
)

// RunOnly retains the two-part configuration shape. Build may be delegated;
// Publish may state `both` or `orchestrator`, which both run here.
//
// Two named fields rather than a list, because the pair is what every reader
// wants and the list is only how a file may write it. A level that states one
// value states it for both stages, while a worker build needs the explicit
// pair `[worker, orchestrator]` because worker publication is refused.
type RunOnly struct {
	Build   string
	Publish string
}

// MarshalJSON writes the shortest shape that carries everything the value
// says: a bare string when both stages agree, the pair otherwise. A file that
// wrote one word gets one word back, so a configuration dispat rewrites does
// not grow arrays around the entries it left alone.
func (r RunOnly) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.canonical())
}

// MarshalYAML is MarshalJSON's counterpart for a YAML config.
func (r RunOnly) MarshalYAML() (any, error) {
	return r.canonical(), nil
}

// canonical renders the value in the shape both marshallers write.
func (r RunOnly) canonical() any {
	if r.Build == r.Publish {
		return r.Build
	}
	return []string{r.Build, r.Publish}
}

// UnmarshalJSON accepts either shape the key may be written in.
func (r *RunOnly) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	value, err := NormalizeRunOnly(raw, "runOnly")
	if err != nil {
		return err
	}
	*r = value
	return nil
}

// ResolveBuild is where the package's build frame may run. Nil-safe, and an
// unstated value is RunOnlyBoth, so every caller asks one question and no
// caller has to know that an absent key means anywhere.
func (r *RunOnly) ResolveBuild() string {
	if r == nil || r.Build == "" {
		return RunOnlyBoth
	}
	return r.Build
}

// ResolvePublish is where the package's publish frame may run. Nil-safe, on
// the same terms as ResolveBuild.
func (r *RunOnly) ResolvePublish() string {
	if r == nil || r.Publish == "" {
		return RunOnlyBoth
	}
	return r.Publish
}

// NormalizeRunOnly expands one `runOnly` value into the pair everything
// downstream reads, refusing a shape or a word the key does not have. where
// names the level for error messages, since the same key is read at five
// levels and the reader has to be told which one is wrong.
//
// It is the single implementation behind both entry points: UnmarshalJSON
// above, and the CLI's own weak config reader. Two readers of one syntax
// would be two syntaxes eventually, and this is a key whose misreading places
// a signing build on the wrong machine.
func NormalizeRunOnly(raw any, where string) (RunOnly, error) {
	var out RunOnly
	switch x := raw.(type) {
	case string:
		if err := checkRunOnlyValue(where, x); err != nil {
			return RunOnly{}, err
		}
		out = RunOnly{Build: x, Publish: x}
	case []any:
		stated := make([]string, 0, len(x))
		for i, item := range x {
			text, isText := item.(string)
			if !isText {
				return RunOnly{}, fmt.Errorf("%s[%d]: wants %s", where, i, runOnlyVocabulary)
			}
			stated = append(stated, text)
		}
		var err error
		out, err = normalizeRunOnlyPair(where, stated)
		if err != nil {
			return RunOnly{}, err
		}
	case []string:
		var err error
		out, err = normalizeRunOnlyPair(where, x)
		if err != nil {
			return RunOnly{}, err
		}
	default:
		return RunOnly{}, fmt.Errorf(
			"%s: wants %s, or a [build, publish] pair of them", where, runOnlyVocabulary)
	}
	if out.Publish == RunOnlyWorker {
		return RunOnly{}, fmt.Errorf(
			"%s: publication runs on the orchestrator; set publish to orchestrator or both "+
				"(use [worker, orchestrator] for worker builds)", where)
	}
	return out, nil
}

// normalizeRunOnlyPair reads the list form, which says the two stages differ
// and therefore has to name both of them.
//
// A list of one is refused rather than read as the scalar it looks like: the
// scalar already says "both stages", so a one-element list is a pair somebody
// is halfway through writing, and reading it as a value for both stages would
// place the stage that is missing wherever the run liked.
func normalizeRunOnlyPair(where string, stated []string) (RunOnly, error) {
	if len(stated) != 2 {
		return RunOnly{}, fmt.Errorf(
			"%s: a list states both stages as [build, publish], got %d value(s); one value for both stages is written on its own",
			where, len(stated))
	}
	for i, value := range stated {
		if err := checkRunOnlyValue(fmt.Sprintf("%s[%d]", where, i), value); err != nil {
			return RunOnly{}, err
		}
	}
	return RunOnly{Build: stated[0], Publish: stated[1]}, nil
}

// checkRunOnlyValue holds one word to the vocabulary the key has.
func checkRunOnlyValue(where, value string) error {
	switch value {
	case RunOnlyBoth, RunOnlyWorker, RunOnlyOrchestrator:
		return nil
	}
	return fmt.Errorf("%s: %q is not a placement; want %s", where, value, runOnlyVocabulary)
}

// runOnlyVocabulary is the whole of what the key accepts, written once so
// that every refusal offers the reader the same three words.
const runOnlyVocabulary = `"both", "worker" or "orchestrator"`
