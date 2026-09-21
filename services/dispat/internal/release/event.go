package release

import (
	"time"

	"github.com/rs/zerolog"

	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The release-progress events an Observerx receives. The names are the public
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
	// The execution fields, set on every event of a run that states an
	// execution object and on none of the events of a run that does not.
	//
	// Role and Node name the process this event was sent from, exactly as
	// they name the process every log line was written by: a receiver
	// hearing from an orchestrator and from a worker at once tells them
	// apart by these two and nothing else. Worker names another node the
	// event is about, which is the node a stage was placed on, so "the
	// orchestrator reports that build-a finished" is one event rather than
	// two readings of one field.
	Role   string `json:"role,omitempty"`
	Node   string `json:"node,omitempty"`
	Worker string `json:"worker,omitempty"`
}

// Sender is the process that writes a run's log lines and sends its events:
// which role it plays in a distributed run, and which node it is.
//
// It is one value rather than two strings passed around because the two are
// only ever true together: a process either takes part in distributed
// execution, in which case it has both, or it does not, in which case it
// names neither and every line and payload stays what it was. The zero value
// is that second case, which is what keeps a release with no execution object
// byte for byte the run it always was.
type Sender struct {
	// Role is "orchestrator" or "worker".
	Role string
	// Node is this node's own name: its execution.name where one is stated,
	// and the machine's host name where none is.
	Node string
}

// IsStated reports whether this process takes part in distributed execution
// and therefore has an identity to put on what it writes.
func (s Sender) IsStated() bool { return s.Role != "" }

// Stamp names the sender on one event, and answers the event unchanged for a
// process that takes no part in distributed execution.
//
// The event is taken and answered by value because that is what an event is:
// an immutable snapshot a consumer may hold, so naming its sender produces
// the event that is sent rather than editing one somebody else may be reading.
func (s Sender) Stamp(event Event) Event {
	if !s.IsStated() {
		return event
	}
	event.Role, event.Node = s.Role, s.Node
	return event
}

// Attach answers the logger every line of this process is written through:
// the given one, with the sender's own name on it.
//
// One child logger is derived once, where the configuration is first known,
// rather than a field written at each call site: a line that forgot it would
// be a line whose machine nobody can name, and in a run spread over several
// machines that is the one question every other reading starts from.
func (s Sender) Attach(log zerolog.Logger) zerolog.Logger {
	if !s.IsStated() {
		return log
	}
	return log.With().Str("role", s.Role).Str("node", s.Node).Logger()
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

// Observerx receives release-progress events. Implementations must be
// goroutine-safe and must return immediately — the executor calls it from
// concurrent task goroutines and never waits on anything the observer does
// with the event. A nil Observerx on the Executor disables observation.
type Observerx interface {
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
