package app

import (
	"bytes"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

func commitValidator(t *testing.T, parserConfig ccme.Config) (*App, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	log := zerolog.New(&logs).Level(zerolog.DebugLevel)
	return New(t.TempDir(), &config.File{ResolvedParser: parserConfig}, log), &logs
}

func TestValidateCommitMessageAcceptsConfiguredMultiUnitMessage(t *testing.T) {
	a, logs := commitValidator(t, ccme.Config{
		Separator: "%%%",
		Types:     map[string]ccme.Bump{"add": ccme.BumpMinor, "repair": ccme.BumpPatch},
	})

	err := a.ValidateCommitMessage([]byte("add(core): stream events\n%%%\nrepair(cli): retain exit code"))

	require.NoError(t, err)
	assert.Contains(t, logs.String(), `"message":"commit message validated"`)
	assert.Contains(t, logs.String(), `"units":2`)
}

func TestValidateCommitMessageWarningsAreVisibleAndNonBlocking(t *testing.T) {
	a, logs := commitValidator(t, ccme.Config{})

	err := a.ValidateCommitMessage([]byte("unknown(core): keep local convention"))

	require.NoError(t, err)
	assert.Contains(t, logs.String(), `"level":"warn"`)
	assert.Contains(t, logs.String(), `"code":"W140"`)
	assert.Contains(t, logs.String(), `"line":1`)
	assert.NotContains(t, logs.String(), "unknown(core)", "message bytes must not be logged")
}

func TestValidateCommitMessageErrorsBlockWithoutLoggingMessage(t *testing.T) {
	a, logs := commitValidator(t, ccme.Config{})
	secret := "this text must stay out of logs"

	err := a.ValidateCommitMessage([]byte("not a header: " + secret))

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidCommitMessage)
	var invalid *CommitMessageError
	require.True(t, errors.As(err, &invalid))
	assert.Positive(t, invalid.Errors)
	assert.Contains(t, logs.String(), `"message":"commit message refused"`)
	assert.NotContains(t, logs.String(), secret)
}

func TestValidateCommitMessageHonorsStrictTypes(t *testing.T) {
	a, _ := commitValidator(t, ccme.Config{StrictTypes: true})

	assert.ErrorIs(t,
		a.ValidateCommitMessage([]byte("unknown(core): rejected here")),
		ErrInvalidCommitMessage)
}

func TestValidateCommitMessageRejectsInvalidParserConfiguration(t *testing.T) {
	a, logs := commitValidator(t, ccme.Config{Separator: "not one line\n"})

	err := a.ValidateCommitMessage([]byte("feat(core): valid message"))

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInvalidCommitMessage)
	assert.Contains(t, logs.String(), "cannot configure commit-message validator")
}
