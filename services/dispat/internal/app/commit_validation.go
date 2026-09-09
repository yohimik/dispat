// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"errors"
	"fmt"

	"github.com/yohimik/dispat/pkg/ccme"
)

// ErrInvalidCommitMessage identifies a proposed commit message that the
// repository's configured parser refused. Callers can use errors.Is without
// having to inspect ccme's diagnostic text.
var ErrInvalidCommitMessage = errors.New("invalid commit message")

// CommitMessageError reports how many error diagnostics invalidated a
// proposed commit message. The message itself is deliberately absent: commit
// messages may contain issue details, credentials pasted by mistake, or other
// text that must not be copied into an error or log event.
type CommitMessageError struct {
	Errors int
}

func (e *CommitMessageError) Error() string {
	return fmt.Sprintf("%v (%d parser errors)", ErrInvalidCommitMessage, e.Errors)
}

func (e *CommitMessageError) Unwrap() error { return ErrInvalidCommitMessage }

// ValidateCommitMessage validates the complete message Git proposes to
// commit, using the same resolved parser configuration as release planning.
// Every diagnostic is reported individually without logging message text.
// Warnings remain non-blocking; one or more errors refuse the message.
//
// The caller is responsible for observing Git's final message file after its
// editor and message-transforming hooks have run. Keeping that orchestration
// outside this method makes the validation rule reusable without pretending
// an earlier command-line message is necessarily what Git will commit.
func (a *App) ValidateCommitMessage(message []byte) error {
	parser, err := ccme.NewParser(a.cfg.ResolvedParser)
	if err != nil {
		// Configuration loading normally proves this before App is built. Keep
		// the boundary defensive for callers that construct a File directly.
		a.log.Error().Err(err).Msg("cannot configure commit-message validator")
		return fmt.Errorf("configure commit-message validator: %w", err)
	}

	result, _ := parser.Parse(string(message))
	for _, diagnostic := range result.Diagnostics {
		event := a.log.Warn()
		if diagnostic.IsError() {
			event = a.log.Error()
		}
		event = event.
			Str("code", diagnostic.Code).
			Int("line", diagnostic.Position.Line).
			Int("column", diagnostic.Position.Column)
		if diagnostic.UnitIndex >= 0 {
			event = event.Int("unit", diagnostic.UnitIndex+1)
		}
		event.Msg(diagnostic.Message)
	}

	errorCount := len(result.Errors())
	if errorCount > 0 {
		a.log.Error().Int("errors", errorCount).Int("warnings", len(result.Warnings())).
			Msg("commit message refused")
		return &CommitMessageError{Errors: errorCount}
	}
	a.log.Debug().Int("units", len(result.Units)).Int("warnings", len(result.Warnings())).
		Msg("commit message validated")
	return nil
}
