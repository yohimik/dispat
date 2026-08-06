package release

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/cli/internal/plan"
	"github.com/yohimik/dispat/services/cli/internal/script"
)

// OutputEnvVar is the variable every per-package script and hook receives:
// the path of a file the script may append NAME=value lines to,
// GITHUB_OUTPUT-style, to export values for everything that runs after it:
//
//	echo "IMAGE_DIGEST=$(cat digest.txt)" >> "$DISPAT_OUTPUT"
//	echo "GITHUB_ATTACHMENTS=$PWD/dist/app.tgz $PWD/dist/SHA256SUMS" >> "$DISPAT_OUTPUT"
//
// Each exported value reaches every later script and hook of the package —
// the outcome scripts onFail/onSkip included — as DISPAT_OUTPUT_<NAME>, with
// DISPAT_OUTPUTS listing the exported names. Re-exporting a name overrides
// its earlier value, the way a shell re-assignment would. The GITHUB_ATTACHMENTS
// output is special only to the GitHub recorder: its value is a
// whitespace-separated list of absolute file paths uploaded as release assets.
const OutputEnvVar = "DISPAT_OUTPUT"

// outputEnvPrefix is what an exported name is published under.
const outputEnvPrefix = "DISPAT_OUTPUT_"

// parseOutputs reads one sequence's output file. A line is NAME=value with
// NAME a valid environment variable name; blank lines are skipped, anything
// else is an error — a typo here would otherwise silently drop an export.
// Within the file, a later line overrides an earlier one for the same name.
func parseOutputs(path string) ([]plan.Output, error) {
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
		if !ok || !validEnvName(name) {
			return nil, fmt.Errorf("%s line %d: want NAME=value, got %q", OutputEnvVar, i+1, line)
		}
		if at, dup := index[name]; dup {
			out[at].Value = value
			continue
		}
		index[name] = len(out)
		out = append(out, plan.Output{Name: name, Value: value})
	}
	return out, nil
}

// MergeOutputs folds exports onto the release: first-export order is kept, a
// re-exported name takes the new value. It is also how `dispat run` carries a
// provider's exports into its consumers. Only the goroutine running the
// package's current task touches rel.Outputs — tasks of one package are
// strictly ordered by the scheduler, and a run-command consumer reads its
// providers' outputs only after their tasks completed — so no lock is needed.
func MergeOutputs(rel *plan.Release, outs []plan.Output) {
	for _, o := range outs {
		found := false
		for i := range rel.Outputs {
			if rel.Outputs[i].Name == o.Name {
				rel.Outputs[i].Value = o.Value
				found = true
				break
			}
		}
		if !found {
			rel.Outputs = append(rel.Outputs, o)
		}
	}
}

// outputsEnv renders the accumulated outputs: one DISPAT_OUTPUT_<NAME> per
// export plus the DISPAT_OUTPUTS listing, set even when empty so a shell
// loop iterates zero times instead of reading an unset variable.
func outputsEnv(rel *plan.Release) []string {
	names := make([]string, 0, len(rel.Outputs))
	env := make([]string, 0, len(rel.Outputs)+1)
	for _, o := range rel.Outputs {
		names = append(names, o.Name)
		env = append(env, outputEnvPrefix+o.Name+"="+o.Value)
	}
	return append(env, "DISPAT_OUTPUTS="+strings.Join(names, " "))
}

// validEnvName reports whether s is a portable environment variable name:
// [A-Za-z_][A-Za-z0-9_]*.
func validEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		letter := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// RunSequenceWithOutputs runs a command sequence with an output file
// attached: env is extended with DISPAT_OUTPUT pointing at a fresh temp
// file, and everything the sequence exported is merged onto the release
// afterwards — even when the sequence failed, so the outcome scripts see
// what was exported before the failure. A malformed export is an error of
// its own (surfaced like a failing command; warn-only callers warn).
func RunSequenceWithOutputs(ctx context.Context, runner script.Runner, rel *plan.Release, dir, stage string, commands, env []string, log zerolog.Logger, failFast bool) error {
	if len(commands) == 0 {
		return nil
	}
	f, err := os.CreateTemp("", "dispat-output-*")
	if err != nil {
		return fmt.Errorf("creating the %s file: %w", OutputEnvVar, err)
	}
	file := f.Name()
	_ = f.Close()
	defer os.Remove(file)

	seqErr := RunSequence(ctx, runner, dir, stage, commands, append(env, OutputEnvVar+"="+file), log, failFast)
	// parseOutputs is all-or-nothing: on a malformed line nothing is merged.
	outs, parseErr := parseOutputs(file)
	MergeOutputs(rel, outs)
	if seqErr != nil {
		return seqErr
	}
	if parseErr != nil {
		if failFast {
			return parseErr
		}
		log.Warn().Err(parseErr).Str("stage", stage).Msg("script outputs invalid (not fatal)")
	}
	return nil
}
