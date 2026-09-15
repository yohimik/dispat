package cli

import (
	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

func logConfigError(log zerolog.Logger, err error) *zerolog.Event {
	event := log.Error().Err(err)
	if code := config.DiagnosticCode(err); code != "" {
		event.Str("code", code)
	}
	return event
}
