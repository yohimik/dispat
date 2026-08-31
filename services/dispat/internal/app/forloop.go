package app

import (
	"context"
	"errors"
	"io"
	"strconv"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// `dispat for`: the shell's own `for x in ...; do ...; done`, spelled so it
// means the same thing under every shell a configuration may name. A POSIX loop
// copied into a script is the one construct that breaks the moment `shell` is
// `cmd /C`, and a loop is what a script reaches for the instant it has more
// than one thing to do.
//
// The iteration itself reads no configuration, like the rest of the shell
// helpers: a list of words is a list of words wherever it is standing. What the
// items are may take a configuration to say, and that is what ForItems is for.

// ForItem is one thing the loop runs over: the value the item's own name
// carries, and the variables that describe it. Literal items carry none; a
// package carries its name, its space, its folder and its versioning group.
//
// There is no per-item folder here on purpose. Every iteration runs where the
// invocation stands, so a relative path in the script means one thing however
// far down the list it fires; a script that wants the item's folder is handed
// it as DISPAT_DIR and may cd there itself.
type ForItem struct {
	Value string
	Env   []string // KEY=value pairs describing the item
}

// ForOptions is one whole loop.
type ForOptions struct {
	// Items are iterated in order, one script run per item.
	Items []ForItem
	// Scripts are the --do scripts, run in order for each item and stopping at
	// the first one of them that fails: within an item, a sequence gates its own
	// remainder, the same fail-fast a release stage's sequence gets.
	Scripts []string
	// KeepGoing continues past a failing item instead of stopping at it. The
	// exit code is the first failure's either way.
	KeepGoing bool
	// RequireItems turns an iteration over nothing into a refusal, for the
	// pipeline stage whose point is that the list was not empty.
	RequireItems bool
	// OnFailure runs once after a failed loop and decides the exit code in its
	// place. Empty means the first failure's code propagates as it stands.
	OnFailure string
	// Dir is the working directory of every iteration: --root, or --in.
	Dir string
	// Runner executes the scripts. Nil means a plain ShellRunner, which is
	// /bin/sh -c: the config-free path has no configuration to take a shell
	// from.
	Runner         script.Runner
	Stdout, Stderr io.Writer
	Log            zerolog.Logger
}

// The variables every iteration exports, whatever it is iterating over. They
// are appended after the item's own, so nothing an item describes can shadow
// them and a script may read DISPAT_ITEM knowing what it holds.
const (
	// ItemEnvVar is the item itself: the literal word, or the package, space or
	// group name.
	ItemEnvVar = "DISPAT_ITEM"
	// IndexEnvVar is the item's 0-based position and TotalEnvVar the item count,
	// so `[ "$DISPAT_INDEX" -eq 0 ]` is a first-iteration test and the pair
	// renders a progress line without counting anything.
	IndexEnvVar = "DISPAT_INDEX"
	TotalEnvVar = "DISPAT_TOTAL"
)

// RunFor iterates and returns the process exit code.
//
// The loop is strictly sequential. That is the fidelity claim: a shell's `for`
// runs one body at a time, a script written against one is entitled to assume
// it, and concurrency over a selection is what `dispat run` already is.
//
// The first failure's code is the loop's, so a pipeline gating on a specific
// code still sees the code an item's script chose. Without --keep-going the
// loop stops there; with it every item still runs and the first failure is
// still what the command reports, because a later item succeeding says nothing
// about the one that did not.
func RunFor(ctx context.Context, opts ForOptions) (int, error) {
	if len(opts.Items) == 0 {
		if opts.RequireItems {
			err := errors.New("nothing to iterate over and --require-items is set")
			opts.Log.Error().Err(err).Msg("refusing to run the loop")
			return 1, err
		}
		// An empty list is an honest no-op, the same answer `for x in $EMPTY`
		// gives: the loop ran, zero times.
		opts.Log.Info().Msg("nothing to iterate over, nothing to run")
		return 0, nil
	}

	runner := opts.Runner
	if runner == nil {
		runner = &script.ShellRunner{Log: opts.Log}
	}
	total := len(opts.Items)
	code, failed, ran := 0, 0, 0
	for i, item := range opts.Items {
		// Between items rather than around the whole loop: a Ctrl-C reaching the
		// running script already ended that item, and what is left to decide is
		// whether the next one starts. It breaks rather than returning, so an
		// interrupted loop still reaches its --on-failure — a cleanup exists for
		// the run that did not finish, and Ctrl-C is that run.
		if ctx.Err() != nil {
			opts.Log.Debug().Int("index", i).Msg("the loop was cancelled, stopping")
			if code == 0 {
				code = 1
			}
			break
		}
		ran++
		opts.Log.Debug().Int("index", i).Str("item", item.Value).Msg("running loop item")
		itemCode, err := shellCall{
			Runner: runner, Dir: opts.Dir, Scripts: opts.Scripts,
			Env:    itemEnv(item, i, total),
			Stdout: opts.Stdout, Stderr: opts.Stderr, Log: opts.Log,
		}.run(ctx)
		if err != nil {
			return 1, err
		}
		if itemCode == 0 {
			continue
		}
		failed++
		if code == 0 {
			code = itemCode
		}
		if !opts.KeepGoing {
			opts.Log.Debug().Int("index", i).Str("item", item.Value).Int("code", itemCode).
				Msg("item failed, stopping the loop")
			break
		}
		opts.Log.Debug().Int("index", i).Str("item", item.Value).Int("code", itemCode).
			Msg("item failed, continuing")
	}
	// items is the list's length and ran what the loop got through, which differ
	// exactly when something stopped it early: a failure, or an interruption.
	opts.Log.Info().Int("items", total).Int("ran", ran).Int("failed", failed).Msg("loop finished")

	if code == 0 || opts.OnFailure == "" {
		return code, nil
	}
	// Once for the loop rather than once per item: --on-failure reacts to the
	// command having failed, and a cleanup run per failing item under
	// --keep-going would fire as many times as the list is long. It survives
	// whatever ended the loop, the same treatment every other failure script
	// gets.
	opts.Log.Debug().Int("code", code).Msg("the loop failed, running the failure script")
	return shellCall{
		Runner: runner, Dir: opts.Dir, Scripts: []string{opts.OnFailure},
		Stdout: opts.Stdout, Stderr: opts.Stderr, Log: opts.Log,
	}.run(context.WithoutCancel(ctx))
}

// itemEnv is the item's own variables with the iterator's appended, in that
// order: exec keeps the last value for a repeated key, so the three below win
// over anything an item or the process environment carries under those names.
func itemEnv(item ForItem, index, total int) []string {
	env := make([]string, 0, len(item.Env)+3)
	env = append(env, item.Env...)
	return append(env,
		ItemEnvVar+"="+item.Value,
		IndexEnvVar+"="+strconv.Itoa(index),
		TotalEnvVar+"="+strconv.Itoa(total))
}
