package app

import (
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// printDiagnostics reports everything the planner observed. Warnings never
// block a release, but several of them — W193, W194, W202 and W208 — are
// non-suppressible precisely because they explain a release outcome that a
// reader of the commit log alone cannot account for (§16), so they must reach
// the operator rather than sit unread on the plan.
func (a *App) printDiagnostics(pl *plan.Plan) {
	warnings, errors := 0, 0
	for _, d := range pl.Diagnostics {
		ev := a.log.Warn()
		if d.Level == plan.LevelError {
			ev = a.log.Error()
			errors++
		} else {
			warnings++
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
		a.log.Info().Int("warnings", warnings).Int("errors", errors).Msg("plan diagnostics")
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
	releasing, held := 0, 0
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

		switch {
		case rel.Held:
			held++
			ev.Msg("‖ held (Release-As: none)")
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
	a.log.Info().
		Int("packages", len(pl.Order)).
		Int("releasing", releasing).
		Int("held", held).
		Msg("release plan ready")
}

// summarize prints one line per processed package plus totals, and returns
// how many packages failed.
func (a *App) summarize(pl *plan.Plan, results map[string]*release.Result, took time.Duration) int {
	published, failed, skipped := 0, 0, 0
	for _, name := range pl.Order {
		res, ok := results[name]
		if !ok {
			continue
		}
		var ev *zerolog.Event
		switch res.Status {
		case release.StatusPublished:
			published++
			ev = a.log.Info().Str("tag", pl.Releases[name].TagName())
		case release.StatusFailed:
			failed++
			ev = a.log.Error().Err(res.Err).Str("failedStage", res.FailedStage)
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
	a.log.Info().
		Int("published", published).
		Int("failed", failed).
		Int("skipped", skipped).
		Int("held", len(pl.Held())).
		Int("unchanged", len(pl.Order)-len(results)-len(pl.Held())).
		Dur("took", took).
		Msg("done")
	return failed
}
