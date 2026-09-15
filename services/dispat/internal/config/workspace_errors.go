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
