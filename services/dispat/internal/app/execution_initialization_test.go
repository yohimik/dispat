// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestTaskInitializationFailureKeepsUnknownTransportCause(t *testing.T) {
	uncertain := fmt.Errorf("the atomic push answer was lost: %w", gitx.ErrPushUnknown)
	err := taskInitializationFailure(uncertain)
	assert.Equal(t, execution.CodeConfiguration, config.DiagnosticCode(err))
	assert.ErrorIs(t, err, gitx.ErrPushUnknown)
	assert.NotContains(t, err.Error(), "definitively refused")
	var diagnostic *execution.Diagnostic
	require.True(t, errors.As(err, &diagnostic))
	assert.Equal(t, execution.CategoryConfiguration, diagnostic.DiagnosticCategory())

	coded := execution.NewDiagnostic(execution.CodeIntegrity, execution.CategoryIntegrity,
		"an input is missing: %w", uncertain)
	assert.Same(t, coded, taskInitializationFailure(coded), "an existing diagnostic keeps its class")
	stopped := taskInitializationFailure(fmt.Errorf("stopped: %w", context.Canceled))
	assert.ErrorIs(t, stopped, context.Canceled)
	assert.Empty(t, config.DiagnosticCode(stopped), "an operator stop is not a capability failure")
}
