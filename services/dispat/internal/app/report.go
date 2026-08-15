package app

import (
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// printDiagnostics reports everything the planner observed. Warnings never
// block a release, but several of them — W193, W194, W202 and W208 — are
// non-suppressible precisely because they explain a release outcome that a
// reader of the commit log alone cannot account for (§16), so they must reach
// the operator rather than sit unread on the plan.
//
// parser.quiet (or --quiet-parser) hides the commit-message parser's own
// findings, for a repository whose history predates the convention. It hides
// lines and nothing else: every diagnostic is still counted, the summary says
// how many went unprinted, and what a hidden error does to the run is
// unchanged — the §16 codes above belong to the planner, not the parser, so
// they cannot be hidden at all.
func (a *App) printDiagnostics(pl *plan.Plan) {
	quiet := a.cfg.Parser != nil && a.cfg.Parser.Quiet
	warnings, errors, hidden := 0, 0, 0
	for _, d := range pl.Diagnostics {
		if d.Level == plan.LevelError {
			errors++
		} else {
			warnings++
		}
		// Before the event is built, not after: a zerolog event taken from
		// the pool and never sent is a leak.
		if quiet && ccme.IsDiagnosticCode(d.Code) {
			hidden++
			continue
		}
		ev := a.log.Warn()
		if d.Level == plan.LevelError {
			ev = a.log.Error()
		}
		ev = ev.Str("code", d.Code)
		if d.Pkg != "" {
			ev = ev.Str("package", d.Pkg)
		}
		if d.Commit != "" {
			ev = ev.Str("commit", shortCommit(d.Commit))
		}
		ev.Msg(d.Message)
	}
	if warnings+errors > 0 {
		ev := a.log.Info().Int("warnings", warnings).Int("errors", errors)
		if hidden > 0 {
			ev = ev.Int("hidden", hidden)
		}
		ev.Msg("plan diagnostics")
	}
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// printGraph prints the whole project graph in dependency order, highlighting
// released packages with their version and channel transitions.
//
// The order is the topological one, which is also the publish order of §19.2:
// §13.10 requires the plan to be emitted in it, so that the order packages will
// actually be published in is visible before the run rather than inferred
// afterwards.
func (a *App) printGraph(pl *plan.Plan) {
	releasing, held, deselected := 0, 0, 0
	for _, name := range pl.Order {
		rel := pl.Releases[name]
		ev := a.log.Info().
			Str("package", name).
			Str("space", rel.Pkg.Space.Name)
		if provs := pl.Providers[name]; len(provs) > 0 {
			ev = ev.Strs("dependsOn", provs)
		}
		if !rel.Changed() {
			ev.Str("version", rel.Previous().String()).
				Str("channel", rel.Channel).
				Msg("unchanged")
			continue
		}

		if rel.FromInitials {
			ev = ev.Bool("baselineFromInitials", true)
		}
		// §13.10: where the channel differs from the baseline's, the plan MUST
		// show both — the transition a reader needs to see is "beta -> stable",
		// not the word "stable" alone.
		ev = ev.Str("bump", rel.Bump.String()).
			Str("version", rel.Previous().String()+" -> "+rel.Next.String()).
			Str("channel", rel.ChannelTransition()).
			Str("reason", rel.Reason()).
			Int("ownCommits", len(rel.Units)).
			Strs("dueToProviders", rel.DueTo)
		// §13.10 requires the plan to mark its corrected and suppressed
		// entries. Both are invisible in the numbers above: a restated record
		// looks like an ordinary one, and a suppressed entry looks like a
		// commit that never happened.
		if n := len(rel.Corrects); n > 0 {
			ev = ev.Int("corrected", n)
		}
		if n := len(rel.SuppressedNotes); n > 0 {
			ev = ev.Int("suppressedNotes", n)
		}

		switch {
		case rel.Held:
			held++
			ev.Msg("‖ held (Release-As: none)")
		case len(rel.WaitingFor) > 0:
			// Selected and not going: the graph is the place that has to say
			// so, because the version beside it is one this run will not write.
			deselected++
			ev.Strs("waitingFor", rel.WaitingFor).Msg("⊘ withheld until its providers release")
		case rel.Deselected:
			deselected++
			ev.Msg("⊝ not selected")
		case rel.CatchUp:
			releasing++
			ev.Msg("↻ catch-up")
		case rel.ChannelOnly:
			releasing++
			ev.Msg("◈ channel-only")
		default:
			releasing++
			ev.Msg("● changed")
		}
	}
	ev := a.log.Info().
		Int("packages", len(pl.Order)).
		Int("releasing", releasing).
		Int("held", held)
	if deselected > 0 {
		// Only when a selection was in force, so an unfiltered run's summary
		// line reads exactly as it always has.
		ev = ev.Int("deselected", deselected)
	}
	ev.Msg("release plan ready")
}

// summarize prints one line per processed package plus totals, and returns
// how many packages failed and how many post-publish steps went wrong.
//
// The two counts are separate because they mean different things. A failed
// package did not release and the next run owes it one. A package with
// criticals *did* release: what is missing is part of its record, which no
// re-run will go back and write.
func (a *App) summarize(pl *plan.Plan, results map[string]*release.Result, took time.Duration) (failed, critical int) {
	published, skipped, cancelled := 0, 0, 0
	for _, name := range pl.Order {
		res, ok := results[name]
		if !ok {
			continue
		}
		critical += len(res.Critical)
		var ev *zerolog.Event
		switch res.Status {
		case release.StatusPublished:
			published++
			// Published, but with something missing from its record: an error
			// line, because a released package nobody tagged is not news to
			// bury in the middle of a green summary.
			if len(res.Critical) > 0 {
				ev = a.log.Error().Errs("critical", res.Critical)
			} else {
				ev = a.log.Info()
			}
			ev = ev.Str("tag", pl.Releases[name].TagName())
		case release.StatusFailed:
			failed++
			ev = a.log.Error().Err(res.Err).Str("failedStage", res.FailedStage)
		case release.StatusCancelled:
			// Interrupted, not failed: the next run owes it the same release.
			cancelled++
			ev = a.log.Warn()
		default:
			skipped++
			ev = a.log.Warn()
			if res.Blocked {
				// Planned, not attempted. Non-suppressible (§16).
				ev = ev.Str("code", plan.CodeBlocked).Str("blockedBy", res.BlockedBy)
			}
		}
		ev.Str("package", name).
			Str("status", res.Status.String()).
			Str("version", res.From.String()+" -> "+res.To.String()).
			Str("channel", res.Channel).
			Dur("took", res.Duration).
			Msg("summary")
	}
	// A deselected package is not unchanged — it has a version waiting for the
	// next run — so it is counted on its own and taken out of that total.
	// Narrow only ever touches releasing packages, so it can never overlap the
	// held ones.
	left := pl.Deselected()
	ev := a.log.Info().
		Int("published", published).
		Int("failed", failed).
		Int("skipped", skipped).
		Int("cancelled", cancelled).
		Int("held", len(pl.Held()))
	if critical > 0 {
		// Only when there are any: a "critical: 0" on every healthy run trains
		// the eye to skip the field on the one run that carries it.
		ev = ev.Int("critical", critical)
	}
	if len(left) > 0 {
		ev = ev.Int("deselected", len(left))
	}
	ev.Int("unchanged", len(pl.Order)-len(results)-len(pl.Held())-len(left)).
		Dur("took", took).
		Msg("done")
	return failed, critical
}
