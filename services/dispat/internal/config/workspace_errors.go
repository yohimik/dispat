package config

import "errors"

// Diagnostic codes owned by polyrepository workspace validation. They live
// here rather than in plan so configuration can report structured failures
// without depending on the planner.
const (
	DiagnosticRepositoryInvalid = "E330"
	DiagnosticOwnershipInvalid  = "E331"
	DiagnosticComposition       = "E332"
	DiagnosticBoundary          = "E333"
	// DiagnosticLinkGraph reports a linked fleet whose links do not
	// form a tree: a second path to a repository the walk already reached.
	DiagnosticLinkGraph = "E338"
	// DiagnosticIdentity reports a repository identity that cannot be trusted
	// to name one participant: a missing, reserved or malformed `repository`,
	// a roster naming the same peer twice, or a linked checkout whose own
	// identity contradicts the link it was reached through.
	DiagnosticIdentity = "E339"
	// DiagnosticLinkOneSided reports a fleet link only one of its two ends
	// declares. The fleet still composes; the missing half is what `dispat
	// compute` proposes.
	DiagnosticLinkOneSided = "W332"
	// DiagnosticRosterDisagreement reports peers that do not agree on who
	// belongs to the fleet, which is how a repository added to one roster and
	// not the others shows up.
	DiagnosticRosterDisagreement = "W333"
)

type workspaceDiagnostic struct {
	code string
	err  error
}

func (e *workspaceDiagnostic) Error() string          { return e.err.Error() }
func (e *workspaceDiagnostic) Unwrap() error          { return e.err }
func (e *workspaceDiagnostic) DiagnosticCode() string { return e.code }

// DiagnosticCode returns the first structured diagnostic code in err's
// unwrap chain. Errors without a code return the empty string.
func DiagnosticCode(err error) string {
	var diagnostic interface{ DiagnosticCode() string }
	if errors.As(err, &diagnostic) {
		return diagnostic.DiagnosticCode()
	}
	return ""
}

// WithDiagnostic gives err a structured code while preserving its text,
// identity, and unwrap chain. Nil and already-coded errors pass through.
func WithDiagnostic(code string, err error) error {
	if err == nil || code == "" || DiagnosticCode(err) != "" {
		return err
	}
	return &workspaceDiagnostic{code: code, err: err}
}
