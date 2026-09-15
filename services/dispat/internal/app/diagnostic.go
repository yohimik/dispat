package app

import (
	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

func (a *App) logError(err error) *zerolog.Event {
	event := a.log.Error().Err(err)
	if code := config.DiagnosticCode(err); code != "" {
		event.Str("code", code)
	}
	return event
}
