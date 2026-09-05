package release

import (
	"time"

	public "github.com/yohimik/dispat/pkg/models/v2"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The release-progress events an Observer receives. The names are the public
// webhook vocabulary — an event travels to external receivers under exactly
// the name a config file subscribes to — aliased here so the executor states
// no string of its own.
const (
	EventReleaseStarted   = public.WebhookReleaseStarted
	EventReleaseFinished  = public.WebhookReleaseFinished
	EventStageStarted     = public.WebhookStageStarted
	EventStageSucceeded   = public.WebhookStageSucceeded
	EventPackagePublished = public.WebhookPackagePublished
	EventPackageFailed    = public.WebhookPackageFailed
	EventPackageSkipped   = public.WebhookPackageSkipped
	EventPackageCancelled = public.WebhookPackageCancelled
	EventScriptProgress   = public.WebhookScriptProgress
)

// Event is one release-progress notification: an immutable snapshot taken at
// the transition, never a pointer into executor state, so a consumer may hold
// it for as long as delivery takes while the run moves on. The json tags
// mirror the log stream's field names — the payload and the log lines speak
// one vocabulary.
type Event struct {
	Name string    `json:"event"`
	Time time.Time `json:"timestamp"`
	// The package fields, set on stage.* and package.* events.
	Package         string `json:"package,omitempty"`
	Stage           string `json:"stage,omitempty"`
	Version         string `json:"version,omitempty"`
	PreviousVersion string `json:"previousVersion,omitempty"`
	Channel         string `json:"channel,omitempty"`
	Tag             string `json:"tag,omitempty"`
	Status          string `json:"status,omitempty"`
	FailedStage     string `json:"failedStage,omitempty"`
	Error           string `json:"error,omitempty"`
	Code            string `json:"code,omitempty"`
	BlockedBy       string `json:"blockedBy,omitempty"`
	// The script.progress fields, raised by `dispat trigger` from inside a
	// stage script. Progress is a pointer so a genuine 0 still travels.
	Progress *int   `json:"progress,omitempty"`
	Message  string `json:"message,omitempty"`
	// The run fields, set on release.* events.
	Root      string         `json:"root,omitempty"`
	Published int            `json:"published,omitempty"`
	Failed    int            `json:"failed,omitempty"`
	Skipped   int            `json:"skipped,omitempty"`
	Cancelled int            `json:"cancelled,omitempty"`
	Packages  []EventPackage `json:"packages,omitempty"`
}

// EventPackage is one package line of a release.started plan or a
// release.finished outcome.
type EventPackage struct {
	Package         string `json:"package"`
	Version         string `json:"version"`
	PreviousVersion string `json:"previousVersion,omitempty"`
	Channel         string `json:"channel,omitempty"`
	Status          string `json:"status,omitempty"`
}

// Observer receives release-progress events. Implementations must be
// goroutine-safe and must return immediately — the executor calls it from
// concurrent task goroutines and never waits on anything the observer does
// with the event. A nil Observer on the Executor disables observation.
type Observer interface {
	Event(ev Event)
}

// notify hands one event to the observer, stamping the time it happened. The
// nil check lives here so every emission site stays one line.
func (e *Executor) notify(ev Event) {
	if e.Observer == nil {
		return
	}
	ev.Time = time.Now()
	e.Observer.Event(ev)
}

// packageEvent snapshots one package's identity — name, versions, channel —
// which every stage.* and package.* event carries.
func packageEvent(name string, rel *plan.Release, event string) Event {
	return Event{
		Name:            event,
		Package:         name,
		Version:         rel.Next.String(),
		PreviousVersion: rel.Previous().String(),
		Channel:         rel.Channel,
	}
}
