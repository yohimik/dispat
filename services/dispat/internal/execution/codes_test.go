package execution

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// TestDiagnosticCarriesBothNames: a refusal has to answer to the logger that
// prints numbered codes and to the reader that switches on the outcome class,
// and the configuration package's reader finds the code without knowing this
// type exists.
func TestDiagnosticCarriesBothNames(t *testing.T) {
	err := NewDiagnostic(CodeAuthority, CategoryAuthority,
		"%s cannot run under worker authority", "release")

	require.Error(t, err)
	assert.Equal(t, "release cannot run under worker authority", err.Error())
	assert.Equal(t, CodeAuthority, config.DiagnosticCode(err))
	assert.Equal(t, CategoryAuthority, DiagnosticCategory(err))
}

// TestDiagnosticKeepsTheWrappedErrorFindable: a code is attached to a
// failure, not substituted for it, so a caller testing for a sentinel still
// finds it through the diagnostic.
func TestDiagnosticKeepsTheWrappedErrorFindable(t *testing.T) {
	sentinel := errors.New("the mailbox is unreachable")

	err := NewDiagnostic(CodeTransport, CategoryTransportCleanup, "closing the branch: %w", sentinel)

	assert.True(t, errors.Is(err, sentinel))
	assert.Equal(t, CodeTransport, config.DiagnosticCode(err))
	assert.Equal(t, CategoryTransportCleanup, DiagnosticCategory(err))
}

// TestDiagnosticCategoryIsAbsentFromOtherErrors: the category is read by
// interface, so an error that carries none answers the empty string rather
// than a guess, and a logger asking the question leaves such an error exactly
// as it found it.
func TestDiagnosticCategoryIsAbsentFromOtherErrors(t *testing.T) {
	for name, err := range map[string]error{
		"a plain error":              errors.New("no"),
		"a wrapped plain error":      fmt.Errorf("context: %w", errors.New("no")),
		"an error with a code alone": config.WithDiagnostic("E330", errors.New("no")),
		"no error at all":            nil,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, DiagnosticCategory(err))
		})
	}
}

// TestExecutionCodesAreDistinct: the numbers are dispat's own and each names
// one outcome class. Two codes sharing a number, or a code repeated across
// classes, would make a CI filter silently wrong.
func TestExecutionCodesAreDistinct(t *testing.T) {
	classOf := map[string]string{
		CodeConfiguration:      CategoryConfiguration,
		CodeAuthority:          CategoryAuthority,
		CodeIntegrity:          CategoryIntegrity,
		CodePublicationUnknown: CategoryPublicationUnknown,
		CodeTransport:          CategoryTransportCleanup,
		CodeTransportRetained:  CategoryTransportCleanup,
	}

	assert.Len(t, classOf, 6, "every code is its own number")
	assert.Equal(t, config.DiagnosticExecution, CodeConfiguration,
		"a file refused at load and a run refused for the same settings carry one code")
	for code, category := range classOf {
		assert.Regexp(t, `^[EW][0-9]{3}$`, code)
		assert.NotEmpty(t, category)
	}
}
