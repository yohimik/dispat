// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// logLines renders what a composition logged as one decoded line per event,
// so an assertion names a field rather than matching console text.
func logLines(t *testing.T, workspace *config.Workspace) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	logWorkspaceComposition(newLogger("trace", "json", &out), workspace)
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		lines = append(lines, event)
	}
	return lines
}

func firstLine(lines []map[string]any, message string) map[string]any {
	for _, line := range lines {
		if line["message"] == message {
			return line
		}
	}
	return nil
}

// TestWorkspaceLogNamesTheSagaAndTheWalk: the composed line is where a reader
// finds out which fleet ran, and for a choreographed one that means the saga
// and the repository the run started in.
func TestWorkspaceLogNamesTheSagaAndTheWalk(t *testing.T) {
	workspace := &config.Workspace{
		ControlRoot: "/w/api",
		Saga:        config.SagaChoreography,
		Repositories: []config.Repository{
			{Name: "api", Root: "/w/api", Entry: true, Imported: true,
				Links: map[string]string{"sdk": ".links/sdk"}},
			{Name: "sdk", Root: "/w/api/.links/sdk", Imported: true, Linker: "api",
				GitlinkPath: ".links/sdk", Links: map[string]string{"api": ".links/api"}},
		},
		Findings: []config.LinkFinding{{Code: "W332", Repository: "api", Peer: "sdk", Message: "W332: one-sided"}},
	}
	lines := logLines(t, workspace)

	composed := firstLine(lines, "polyrepo workspace composed")
	require.NotNil(t, composed)
	assert.Equal(t, config.SagaChoreography, composed["saga"])
	assert.Equal(t, "api", composed["entry"])
	assert.Equal(t, []any{"api", "sdk"}, composed["repositories"])

	finding := firstLine(lines, "W332: one-sided")
	require.NotNil(t, finding)
	assert.Equal(t, "warn", finding["level"], "every W diagnostic is a warning")
	assert.Equal(t, "W332", finding["code"])
	assert.Equal(t, "sdk", finding["peer"])

	walked := firstLine(lines, "fleet repository reached through a link")
	require.NotNil(t, walked)
	assert.Equal(t, "debug", walked["level"], "how the walk decided is a debug line")
	assert.Equal(t, "sdk", walked["repository"])
	assert.Equal(t, "api", walked["linker"])

	var links []map[string]any
	for _, line := range lines {
		if line["message"] == "fleet link" {
			assert.Equal(t, "trace", line["level"], "every link is one trace line")
			links = append(links, line)
		}
	}
	require.Len(t, links, 2)
	assert.Equal(t, ".links/sdk", links[0]["path"])
	assert.Equal(t, true, links[1]["backLink"], "the back-link is where the walk turned round")
}

// TestWorkspaceLogLeavesAnOrchestratedCompositionAlone: the orchestrated line
// is the one every existing run reads, and it gains nothing.
func TestWorkspaceLogLeavesAnOrchestratedCompositionAlone(t *testing.T) {
	workspace := &config.Workspace{
		ControlRoot: "/w",
		Repositories: []config.Repository{
			{Name: "control", Root: "/w", Control: true, Entry: true},
			{Name: "sdk", Root: "/w/sources/sdk", GitlinkPath: "sources/sdk"},
		},
	}
	lines := logLines(t, workspace)
	composed := firstLine(lines, "polyrepo workspace composed")
	require.NotNil(t, composed)
	assert.NotContains(t, composed, "saga")
	assert.NotContains(t, composed, "entry")
	assert.Nil(t, firstLine(lines, "fleet link"))
	assert.Nil(t, firstLine(lines, "fleet repository reached through a link"))
	for _, line := range lines {
		if line["message"] == "repository composed" {
			assert.NotContains(t, line, "linker")
		}
	}
	assert.Empty(t, logLines(t, nil), "no workspace is no composition line")
}

// TestNestedWorkspaceRestoresTheSaga: a package script running dispat inside a
// choreographed release must compose the fleet the release is holding, even
// when the saga was chosen on the command line rather than in the file.
func TestNestedWorkspaceRestoresTheSaga(t *testing.T) {
	t.Setenv(nestedWorkspaceRootEnv, t.TempDir())
	t.Setenv(nestedWorkspaceConfigEnv, "dispat.json")
	t.Setenv(nestedWorkspaceImportsEnv, `[]`)
	t.Setenv(nestedWorkspaceSagaEnv, config.SagaChoreography)

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	o := declareFlags(fs)
	require.NoError(t, applyNestedWorkspace(fs, o))
	assert.True(t, o.nestedWorkspace)
	assert.Equal(t, config.SagaChoreography, *o.saga)
	assert.True(t, fs.Changed("saga"), "the restored saga reaches the config as an override")

	// An explicit --saga is the operator pointing somewhere else, and it keeps
	// the invocation out of the enclosing workspace entirely.
	explicit := pflag.NewFlagSet("test", pflag.ContinueOnError)
	eo := declareFlags(explicit)
	require.NoError(t, explicit.Set("saga", config.SagaOrchestration))
	require.NoError(t, applyNestedWorkspace(explicit, eo))
	assert.False(t, eo.nestedWorkspace)

	// An orchestrated release exports no saga, and nothing is restored.
	t.Setenv(nestedWorkspaceSagaEnv, "")
	plain := pflag.NewFlagSet("test", pflag.ContinueOnError)
	po := declareFlags(plain)
	require.NoError(t, applyNestedWorkspace(plain, po))
	assert.True(t, po.nestedWorkspace)
	assert.Empty(t, *po.saga)
	assert.False(t, plain.Changed("saga"))
}

// TestSagaFlagOverridesTheConfiguredSaga: --saga is a configuration override
// like every other bound flag, so it is applied while the file is loaded
// rather than patched in afterwards.
func TestSagaFlagOverridesTheConfiguredSaga(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	declareFlags(fs)
	require.NotNil(t, fs.Lookup("saga"))
	assert.True(t, globalFlagTakesValue("--saga"))
	assert.True(t, globalFlagInline("--saga=choreography"))
}
