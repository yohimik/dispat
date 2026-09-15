package config

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceDiagnosticPreservesErrorChain(t *testing.T) {
	cause := errors.New("cause")
	err := WithDiagnostic(DiagnosticRepositoryInvalid, fmt.Errorf("workspace failed: %w", cause))

	var diagnostic interface{ DiagnosticCode() string }
	require.True(t, errors.As(err, &diagnostic))
	assert.Equal(t, DiagnosticRepositoryInvalid, diagnostic.DiagnosticCode())
	assert.Equal(t, DiagnosticRepositoryInvalid, DiagnosticCode(err))
	assert.Equal(t, "workspace failed: cause", err.Error())
	assert.ErrorIs(t, err, cause)
}

func TestWithDiagnosticPreservesNilAndExistingCode(t *testing.T) {
	assert.NoError(t, WithDiagnostic(DiagnosticRepositoryInvalid, nil))
	first := WithDiagnostic(DiagnosticOwnershipInvalid, errors.New("owned"))
	assert.Same(t, first, WithDiagnostic(DiagnosticComposition, first))
	assert.Equal(t, DiagnosticOwnershipInvalid, DiagnosticCode(first))
	assert.Empty(t, DiagnosticCode(errors.New("plain")))
}
