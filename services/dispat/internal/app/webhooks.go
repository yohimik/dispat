package app

import (
	"context"
	"os"
	"time"

	public "github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/webhook"
)

// webhookDispatcher builds the routed dispatcher for one release run: the
// top-level list plus every planned package's effective list, so a package's
// events reach exactly the endpoints its level of the ladder subscribed. It
// returns nil when nothing anywhere declares a webhook — the nil is what
// keeps every webhook-less run on exactly the code path it always had.
func (a *App) webhookDispatcher(pl *plan.Plan) *webhook.Dispatcher {
	perPackage := map[string][]public.WebhookConfig{}
	declared := len(a.cfg.Webhooks) > 0
	for name, rel := range pl.Releases {
		perPackage[name] = rel.Pkg.Webhooks
		declared = declared || len(rel.Pkg.Webhooks) > 0
	}
	if !declared {
		return nil
	}
	endpoints := webhook.ResolveWorkspace(a.cfg.Webhooks, perPackage, a.log)
	if len(endpoints) == 0 {
		// Every declared webhook resolved inactive (an env gate, say): with
		// no endpoint to reach, the run needs no dispatcher at all.
		return nil
	}
	return webhook.NewDispatcher(endpoints, nil, a.log)
}

// Trigger delivers one script-raised event to the configured webhooks:
// `dispat trigger <event> [message]`, invoked from a stage script, saying
// what only the script knows. The package, stage and version travel from the
// DISPAT_* environment the enclosing run exported, so the event attributes
// itself to the script that raised it and routes to that package's effective
// webhook list; outside a run the fields are absent and the top-level list
// alone hears it.
//
// It always returns nil once the config is loaded: like every other webhook
// outcome, an endpoint that cannot be reached is a W239 warning, never an
// exit code — a script must not be able to fail its stage by reporting.
func (a *App) Trigger(ctx context.Context, event string, progress *int, message string) error {
	wh := a.triggerDispatcher()
	if wh == nil {
		a.log.Debug().Msg("no webhooks configured, nothing to trigger")
		return nil
	}
	// Deliveries drain before the command returns (bounded by the flush
	// deadline): a trigger is a short-lived process, and an event left in a
	// queue at exit would simply be lost.
	defer wh.Close(context.WithoutCancel(ctx))
	wh.Event(release.Event{
		Name:     event,
		Time:     time.Now(),
		Package:  os.Getenv(plan.PackageEnvVar),
		Stage:    os.Getenv("DISPAT_STAGE"),
		Version:  os.Getenv(plan.NewVersionEnvVar),
		Channel:  os.Getenv("DISPAT_CHANNEL"),
		Progress: progress,
		Message:  message,
	})
	return nil
}

// triggerDispatcher resolves the dispatcher a trigger delivers through. The
// workspace routing is the same one a release uses, so a package-level
// webhook hears its own package's triggers; when the workspace cannot be
// walked — a trigger is a leaf command and must not fail over what a release
// would refuse — the top-level list alone is resolved, unrestricted.
func (a *App) triggerDispatcher() *webhook.Dispatcher {
	pkgs, err := a.packages()
	if err != nil {
		a.log.Warn().Err(err).Msg("workspace discovery failed, only top-level webhooks are reachable")
		endpoints := webhook.Resolve(a.cfg.Webhooks, a.log)
		if len(endpoints) == 0 {
			return nil
		}
		return webhook.NewDispatcher(endpoints, nil, a.log)
	}
	perPackage := map[string][]public.WebhookConfig{}
	declared := len(a.cfg.Webhooks) > 0
	for _, p := range pkgs {
		perPackage[p.Name] = p.Webhooks
		declared = declared || len(p.Webhooks) > 0
	}
	if !declared {
		return nil
	}
	endpoints := webhook.ResolveWorkspace(a.cfg.Webhooks, perPackage, a.log)
	if len(endpoints) == 0 {
		return nil
	}
	return webhook.NewDispatcher(endpoints, nil, a.log)
}

// releaseStartedEvent snapshots the plan the run is about to execute: one
// line per releasing package, in plan order, so a listener knows the whole
// intended run from the first delivery.
func (a *App) releaseStartedEvent(pl *plan.Plan) release.Event {
	ev := release.Event{Name: release.EventReleaseStarted, Time: time.Now(), Root: a.root}
	for _, name := range pl.Order {
		rel := pl.Releases[name]
		if !rel.Releasing() {
			continue
		}
		ev.Packages = append(ev.Packages, release.EventPackage{
			Package:         name,
			Version:         rel.Next.String(),
			PreviousVersion: rel.Previous().String(),
			Channel:         rel.Channel,
		})
	}
	return ev
}

// releaseFinishedEvent snapshots how the run settled: the outcome counts and
// one line per executed package, mirroring what the summary logs. status is
// the run's own word — "succeeded", "failed" or "interrupted".
func (a *App) releaseFinishedEvent(pl *plan.Plan, results map[string]*release.Result, status string) release.Event {
	ev := release.Event{Name: release.EventReleaseFinished, Time: time.Now(), Root: a.root, Status: status}
	for _, name := range pl.Order {
		res, ok := results[name]
		if !ok {
			continue
		}
		switch res.Status {
		case release.StatusPublished:
			ev.Published++
		case release.StatusFailed:
			ev.Failed++
		case release.StatusCancelled:
			ev.Cancelled++
		default:
			ev.Skipped++
		}
		ev.Packages = append(ev.Packages, release.EventPackage{
			Package:         name,
			Version:         res.To.String(),
			PreviousVersion: res.From.String(),
			Channel:         res.Channel,
			Status:          res.Status.String(),
		})
	}
	return ev
}
