package app

import (
	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// logError writes one failure with whatever names it carries for itself: the
// numbered code a dispat log has always printed, and, for the failures of the
// distributed execution profile, the outcome class a machine switches on. An
// error carrying neither is logged exactly as it always was.
func (a *App) logError(err error) *zerolog.Event {
	event := a.log.Error().Err(err)
	if code := config.DiagnosticCode(err); code != "" {
		event.Str("code", code)
	}
	if category := execution.DiagnosticCategory(err); category != "" {
		event.Str("category", category)
	}
	return event
}
