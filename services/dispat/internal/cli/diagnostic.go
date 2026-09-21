package cli

import (
	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// logConfigError writes one pre-command refusal with whatever names it
// carries for itself: the numbered code, and, for the refusals of the
// distributed execution profile, the outcome class beside it. An error
// carrying neither is logged exactly as it always was.
func logConfigError(log zerolog.Logger, err error) *zerolog.Event {
	event := log.Error().Err(err)
	if code := config.DiagnosticCode(err); code != "" {
		event.Str("code", code)
	}
	if category := execution.DiagnosticCategory(err); category != "" {
		event.Str("category", category)
	}
	return execution.AttachIdentity(event, err)
}
