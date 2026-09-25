package release

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/envname"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// OutputEnvVar is the variable every per-package script and hook receives:
// the path of a file the script may append NAME=value lines to,
// GITHUB_OUTPUT-style, to export values for everything that runs after it:
//
//	echo "DISPAT_OUTPUT_IMAGE_DIGEST=$(cat digest.txt)" >> "$DISPAT_OUTPUT"
//	echo "DISPAT_EXPORT_GITHUB=$PWD/dist/app.tgz $PWD/dist/SHA256SUMS" >> "$DISPAT_OUTPUT"
//
// A name may be written with or without the DISPAT_OUTPUT_ prefix — both
// spellings export the same variable. Each exported value reaches every later
// script and hook of the package — the outcome scripts onFail/onSkip included
// — as DISPAT_OUTPUT_<NAME>, with DISPAT_OUTPUTS listing the exported names
// and DISPAT_OUTPUT_SOURCE_<NAME> naming the script that exported each one.
// Re-exporting a name overrides its earlier value, the way a shell
// re-assignment would. The one export with a consumer inside dispat is
// DISPAT_EXPORT_GITHUB (plan.GitHubExport), which travels under its full name
// and gates the package's GitHub release; every other DISPAT_-prefixed name
// is reserved and rejected, so a typo cannot shadow the DISPAT_* environment.
const OutputEnvVar = "DISPAT_OUTPUT"

// parseOutputs reads one sequence's output file. A line is NAME=value with
// NAME a valid environment variable name, optionally spelled with the
// DISPAT_OUTPUT_ prefix (stripped, so both spellings address one output);
// blank lines are skipped, anything else is an error — a typo here would
// otherwise silently drop an export. Within the file, a later line overrides
// an earlier one for the same name. source stamps every parsed output.
func parseOutputs(path, source string) ([]plan.Output, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []plan.Output
	index := make(map[string]int)
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if ok {
			name, ok = normalizeOutputName(name)
		}
		if !ok {
			return nil, fmt.Errorf("%s line %d: want [%s]NAME=value or %s=value, got %q",
				OutputEnvVar, i+1, plan.OutputEnvPrefix, plan.GitHubExport, line)
		}
		if at, dup := index[name]; dup {
			out[at].Value, out[at].Source = value, source
			continue
		}
		index[name] = len(out)
		out = append(out, plan.Output{Name: name, Value: value, Source: source})
	}
	return out, nil
}

// normalizeOutputName maps an export line's name onto the name it is stored
// under: plan.GitHubExport stays whole, a DISPAT_OUTPUT_-spelled name loses
// the prefix, any other DISPAT_-prefixed name is rejected (reserved), and a
// bare name passes as is. ok is false for anything that is not a valid
// environment variable name after the mapping.
func normalizeOutputName(name string) (string, bool) {
	switch {
	case name == plan.GitHubExport:
		return name, true
	case strings.HasPrefix(name, plan.OutputEnvPrefix):
		name = strings.TrimPrefix(name, plan.OutputEnvPrefix)
	case strings.HasPrefix(name, plan.ReservedEnvPrefix):
		return "", false
	}
	return name, envname.IsValid(name)
}

// MergeOutputs folds exports onto the release: first-export order is kept, a
// re-exported name takes the new value and source. It is also how `dispat
// run` carries a provider's exports into its consumers. Only the goroutine
// running the package's current task touches rel.Outputs — tasks of one
// package are strictly ordered by the scheduler, and a run-command consumer
// reads its providers' outputs only after their tasks completed — so no lock
// is needed.
func MergeOutputs(rel *plan.Release, outs []plan.Output) {
	for _, o := range outs {
		found := false
		for i := range rel.Outputs {
			if rel.Outputs[i].Name == o.Name {
				rel.Outputs[i].Value, rel.Outputs[i].Source = o.Value, o.Source
				found = true
				break
			}
		}
		if !found {
			rel.Outputs = append(rel.Outputs, o)
		}
	}
}

// capture runs the sequence with an output file attached — the environment is
// extended with DISPAT_OUTPUT pointing at a fresh temp file — and returns
// whatever the sequence exported, each output stamped with source. The
// outputs are returned even when the sequence failed, so the caller can hand
// the outcome scripts what was exported before the failure; a malformed
// export is returned as parseErr, separate from the sequence's own error.
func (s Sequence) capture(ctx context.Context, source string) (outs []plan.Output, seqErr, parseErr error) {
	f, err := os.CreateTemp("", "dispat-output-*")
	if err != nil {
		return nil, fmt.Errorf("creating the %s file: %w", OutputEnvVar, err), nil
	}
	file := f.Name()
	// Closed immediately and reopened by parseOutputs: the script writes to
	// the path, not to this handle. The close error is discarded because the
	// file is still empty and the deferred remove takes it either way.
	_ = f.Close()
	defer os.Remove(file)

	s.Env = append(s.Env, OutputEnvVar+"="+file)
	seqErr = s.Run(ctx)
	// parseOutputs is all-or-nothing: on a malformed line nothing is returned.
	outs, parseErr = parseOutputs(file, source)
	return outs, seqErr, parseErr
}

// RunMergingOutputs runs the sequence with an output file attached and merges
// everything it exported onto the release — even when the sequence failed, so
// the outcome scripts see what was exported before the failure. A malformed
// export is an error of its own (surfaced like a failing command; warn-only
// sequences warn).
func (s Sequence) RunMergingOutputs(ctx context.Context, rel *plan.Release) error {
	outs, err := s.RunCollectingOutputs(ctx, rel.Pkg.Name+":"+s.Stage)
	MergeOutputs(rel, outs)
	return err
}

// RunCollectingOutputs is RunMergingOutputs without the release: it answers
// what the sequence exported instead of folding it onto a plan value.
//
// It exists for the node that runs somebody else's stage frame. A worker has
// the commands, the folder and the environment of one frame and no plan at
// all, so the export rules — the temporary file, the all-or-nothing parse, the
// warn-only mode's tolerance of a malformed line — have to be reachable
// without one. Exported so that both callers run the same rules rather than
// two implementations of them.
func (s Sequence) RunCollectingOutputs(ctx context.Context, source string) ([]plan.Output, error) {
	if len(s.Commands) == 0 {
		return nil, nil
	}
	outs, seqErr, parseErr := s.capture(ctx, source)
	if seqErr != nil {
		return outs, seqErr
	}
	if parseErr != nil {
		if s.FailFast {
			return outs, parseErr
		}
		s.Log.Warn().Err(parseErr).Str("stage", s.Stage).Msg("script outputs invalid (not fatal)")
	}
	return outs, nil
}
