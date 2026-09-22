package app

import (
	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// logError writes one failure with whatever names it carries for itself: the
// numbered code a dispat log has always printed, the outcome class a machine
// switches on for the failures of the distributed execution profile, and the
// run, node, task and attempt when the failure happened to one of them. An
// error carrying none of it is logged exactly as it always was.
func (a *App) logError(err error) *zerolog.Event {
	return annotateError(a.log.Error().Err(err), err)
}

// annotateError attaches those names to an event a caller already started,
// for the lines that carry fields of their own beside them: a sweep's
// package failure names the package and the stage, and a failure of a task a
// node ran names the node as well.
func annotateError(event *zerolog.Event, err error) *zerolog.Event {
	if code := config.DiagnosticCode(err); code != "" {
		event.Str("code", code)
	}
	if category := execution.DiagnosticCategory(err); category != "" {
		event.Str("category", category)
	}
	return execution.AttachIdentity(event, err)
}
