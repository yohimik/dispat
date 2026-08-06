package release

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/script"
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

// outputEnvPrefix is what an exported name is published under; scripts may
// also spell the name with this prefix already attached in their export line.
const outputEnvPrefix = "DISPAT_OUTPUT_"

// sourceEnvPrefix is where an export's provenance is published:
// DISPAT_OUTPUT_SOURCE_<NAME>=<package>:<stage>.
const sourceEnvPrefix = "DISPAT_OUTPUT_SOURCE_"

// reservedPrefix guards the rest of the script environment: an export whose
// name starts with DISPAT_ but is neither a DISPAT_OUTPUT_-spelled output nor
// plan.GitHubExport is rejected rather than passed through, because it would
// otherwise override a real DISPAT_* variable in every later script.
const reservedPrefix = "DISPAT_"

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
				OutputEnvVar, i+1, outputEnvPrefix, plan.GitHubExport, line)
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
	case strings.HasPrefix(name, outputEnvPrefix):
		name = strings.TrimPrefix(name, outputEnvPrefix)
	case strings.HasPrefix(name, reservedPrefix):
		return "", false
	}
	return name, validEnvName(name)
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

// outputsEnv renders the accumulated outputs: one DISPAT_OUTPUT_<NAME> per
// export with its DISPAT_OUTPUT_SOURCE_<NAME> provenance, plus the
// DISPAT_OUTPUTS listing, set even when empty so a shell loop iterates zero
// times instead of reading an unset variable. plan.GitHubExport travels under
// its full name (no re-prefixing, no listing entry): it is a directive to the
// GitHub recorder, not an ordinary output.
func outputsEnv(rel *plan.Release) []string {
	names := make([]string, 0, len(rel.Outputs))
	env := make([]string, 0, len(rel.Outputs)*2+1)
	for _, o := range rel.Outputs {
		if strings.HasPrefix(o.Name, reservedPrefix) { // plan.GitHubExport
			env = append(env, o.Name+"="+o.Value)
			continue
		}
		names = append(names, o.Name)
		env = append(env, outputEnvPrefix+o.Name+"="+o.Value)
		if o.Source != "" {
			env = append(env, sourceEnvPrefix+o.Name+"="+o.Source)
		}
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

// runSequenceCapturing runs a command sequence with an output file attached —
// env is extended with DISPAT_OUTPUT pointing at a fresh temp file — and
// returns whatever the sequence exported, each output stamped with source.
// The outputs are returned even when the sequence failed, so the caller can
// hand the outcome scripts what was exported before the failure; a malformed
// export is returned as parseErr, separate from the sequence's own error.
func runSequenceCapturing(ctx context.Context, runner script.Runner, dir, stage, source string, commands, env []string, log zerolog.Logger, failFast bool) (outs []plan.Output, seqErr, parseErr error) {
	f, err := os.CreateTemp("", "dispat-output-*")
	if err != nil {
		return nil, fmt.Errorf("creating the %s file: %w", OutputEnvVar, err), nil
	}
	file := f.Name()
	_ = f.Close()
	defer os.Remove(file)

	seqErr = RunSequence(ctx, runner, dir, stage, commands, append(env, OutputEnvVar+"="+file), log, failFast)
	// parseOutputs is all-or-nothing: on a malformed line nothing is returned.
	outs, parseErr = parseOutputs(file, source)
	return outs, seqErr, parseErr
}

// RunSequenceWithOutputs runs a command sequence with an output file
// attached and merges everything the sequence exported onto the release —
// even when the sequence failed, so the outcome scripts see what was
// exported before the failure. A malformed export is an error of its own
// (surfaced like a failing command; warn-only callers warn).
func RunSequenceWithOutputs(ctx context.Context, runner script.Runner, rel *plan.Release, dir, stage string, commands, env []string, log zerolog.Logger, failFast bool) error {
	if len(commands) == 0 {
		return nil
	}
	outs, seqErr, parseErr := runSequenceCapturing(ctx, runner, dir, stage,
		rel.Pkg.Name+":"+stage, commands, env, log, failFast)
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
