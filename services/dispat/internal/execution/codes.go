// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// What a distributed run calls its failures, in both vocabularies at once.
//
// dispat has always printed a numbered code, and a CI job filters on it. The
// specification's §28.9 instead names six outcome classes and requires a
// stable machine-readable identifier for each, because the class is what
// decides how a reader has to react and the numbers are one implementation's.
// Both travel on the same error here: the code a dispat log has always
// carried, and the category beside it.

import (
	"errors"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// The numbered codes this profile adds, in dispat's own E22x and W24x ranges.
// They are dispat's, not the specification's: §28.9 allocates no numbers and
// says so, so these have to stay out of the ranges the specification reserves
// for commit-message diagnostics.
const (
	// CodeConfiguration reports a configuration no distributed run could be
	// executed under. The constant is the configuration package's own, so the
	// code a file is refused with at load and the code a run refuses the same
	// settings with cannot drift apart. The dependency runs one way: this
	// package reads the configuration language, never the other way round.
	CodeConfiguration = config.DiagnosticExecution
	// CodeAuthority reports work refused because of who asked for it: a
	// release initiated under worker authority or on a worker node, an
	// assignment that is not authentically this run's, or a write a task's
	// authority does not extend to.
	CodeAuthority = "E226"
	// CodeIntegrity reports input or output data that is missing, changed,
	// incomplete, incompatible or escaping its declared roots. It fails the
	// prerequisite it belongs to and blocks that prerequisite's consumers,
	// leaving unrelated successful work alone.
	CodeIntegrity = "E227"
	// CodePublicationUnknown reports a publication whose outcome this run
	// cannot establish: an authorized publisher that never reported back. The
	// run is incomplete and exits non-zero, and no second attempt is made
	// under this authorization.
	CodePublicationUnknown = "E228"
	// CodeTransport reports transport state a run could not leave in a safe
	// place: an attempt that had to be fenced, or owned refs whose survival
	// leaves an effect unresolved.
	CodeTransport = "E229"
	// CodeLockLost is the code dispat has always reported a lost or unusable
	// release lock under. It is spelled here rather than added to this
	// profile's own range because the condition is not new: §28.9 classes it
	// as `native-recording-or-lock`, and the class is what a reader switches
	// on.
	CodeLockLost = "E336"
	// CodeTransportRetained is the same subject as a warning, for the
	// leftovers that are merely untidy: temporary refs a completed run could
	// not delete, and writes a build made outside what it declared. Their
	// existence erases no release record, so they keep a W code and a run
	// that is otherwise clean still exits 0.
	CodeTransportRetained = "W244"
)

// The six categories of §28.9, spelled exactly as the specification's table
// names them. They are what a machine switches on: a reader that has never
// heard of E227 still knows that io-integrity means the bytes are wrong and
// that the work depending on them is blocked.
const (
	// CategoryConfiguration is `execution-configuration`: an invalid role,
	// capacity, endpoint, protocol, task graph or transfer capability, which
	// fails before anything is dispatched.
	CategoryConfiguration = "execution-configuration"
	// CategoryAuthority is `execution-authority`: worker initiation, an
	// unauthenticated assignment, stale ownership or an unauthorized write,
	// each refused before the operation it would have performed.
	CategoryAuthority = "execution-authority"
	// CategoryIntegrity is `io-integrity`: input or output data that cannot
	// be used as it stands.
	CategoryIntegrity = "io-integrity"
	// CategoryPublicationUnknown is `publication-unknown`: a lost
	// acknowledgement or an unfenced publisher, which withholds
	// reauthorization and any unsafe lock handover.
	CategoryPublicationUnknown = "publication-unknown"
	// CategoryNativeRecordingOrLock is `native-recording-or-lock`. It is the
	// class of the codes dispat already emits for exactly these conditions
	// (E220, E221 and E222 for a tag or record, E335 and E336 for a lock)
	// rather than a class of new ones, and it is named here so that the
	// mapping is written down in one place.
	CategoryNativeRecordingOrLock = "native-recording-or-lock"
	// CategoryTransportCleanup is `transport-cleanup`: owned temporary refs
	// or bundles that could not be removed, which is CodeTransportRetained
	// when it is harmless and CodeTransport when an effect is left unresolved.
	CategoryTransportCleanup = "transport-cleanup"
)

// Identity names the distributed work a failure belongs to: which run, which
// worker, which task and which attempt of it.
//
// Every field is optional, because most execution failures are about a
// configuration rather than about work: a run that refuses to start has no
// task to name, and naming an empty one would put four meaningless fields in
// every line. What is set is what the failure knows.
//
// The node is called Worker because these failures are decided on the
// orchestrator and are about somebody else: `node` names the process that
// wrote the line, and a line that used it for the machine it is reporting on
// would say the worker reported its own abandonment.
type Identity struct {
	Run     string
	Worker  string
	Task    string
	Attempt int
}

// Diagnostic is one execution failure carrying both names for itself.
//
// It implements DiagnosticCode the way the configuration package's own coded
// errors do, so every logger that already prints a code prints this one
// without being told about this package at all, and it adds the category
// beside it for the readers that switch on the class rather than the number.
type Diagnostic struct {
	code     string
	category string
	identity Identity
	err      error
}

// NewDiagnostic builds one coded execution failure from its message.
//
// The message is formatted here rather than taken as a built error because
// every failure this profile reports is dispat's own refusal rather than
// something that came back from elsewhere, and a constructor that cannot be
// handed a nil error is a constructor whose result is always safe to log.
// Wrapping still works: a caller writes %w in the format, as anywhere else.
func NewDiagnostic(code, category, format string, args ...any) error {
	return &Diagnostic{code: code, category: category, err: fmt.Errorf(format, args...)}
}

// NewIdentifiedDiagnostic is NewDiagnostic for the failures that happened to
// one piece of work rather than to the configuration as a whole.
//
// It is a second constructor rather than a field the caller fills afterwards
// because a diagnostic is built where it is decided and logged immediately:
// an error that could be relabelled after it was created would be an error
// two callers could disagree about.
func NewIdentifiedDiagnostic(identity Identity, code, category, format string, args ...any) error {
	return &Diagnostic{code: code, category: category, identity: identity,
		err: fmt.Errorf(format, args...)}
}

// Error is the sentence the reader is owed, which is the wrapped error's.
func (e *Diagnostic) Error() string { return e.err.Error() }

// Unwrap keeps errors.Is and errors.As working through the code, so a caller
// testing for a sentinel is not defeated by a diagnostic having been attached.
func (e *Diagnostic) Unwrap() error { return e.err }

// DiagnosticCode is the numbered code, read by the same interface the
// configuration package's errors are read through.
func (e *Diagnostic) DiagnosticCode() string { return e.code }

// DiagnosticCategory is the §28.9 outcome class.
func (e *Diagnostic) DiagnosticCategory() string { return e.category }

// DiagnosticIdentity is the work this failure is about, as the error carries
// it. A diagnostic built without one answers the zero identity.
func (e *Diagnostic) DiagnosticIdentity() Identity { return e.identity }

// AttachIdentity adds to a log event whatever work err names itself against,
// and nothing at all for an error that names none.
//
// It lives here rather than in each logger because the fields are the
// specification's vocabulary (§28.3) and there is one right spelling of them:
// two loggers writing `task` and `taskName` would be two log formats.
func AttachIdentity(event *zerolog.Event, err error) *zerolog.Event {
	var carrier interface{ DiagnosticIdentity() Identity }
	if !errors.As(err, &carrier) {
		return event
	}
	identity := carrier.DiagnosticIdentity()
	if identity.Run != "" {
		event.Str("run", identity.Run)
	}
	if identity.Worker != "" {
		event.Str("worker", identity.Worker)
	}
	if identity.Task != "" {
		event.Str("task", identity.Task)
	}
	if identity.Attempt > 0 {
		event.Int("attempt", identity.Attempt)
	}
	return event
}

// DiagnosticCategory returns the first §28.9 category in err's unwrap chain,
// and the empty string for the errors that carry none.
//
// It is read by interface rather than by type so that a category can travel
// on an error this package did not build, and so that a logger asking the
// question does not have to import this package's error type to ask it.
func DiagnosticCategory(err error) string {
	var diagnostic interface{ DiagnosticCategory() string }
	if errors.As(err, &diagnostic) {
		return diagnostic.DiagnosticCategory()
	}
	return ""
}
