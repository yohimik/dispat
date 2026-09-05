package release

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme/v2"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// shellPlan builds a one-package plan rooted in a real temp dir, for tests
// that need actual shells (the output flow runs through DISPAT_OUTPUT, a real
// file only a real process can append to).
func shellPlan(t *testing.T, space *model.Space) (*plan.Plan, string) {
	t.Helper()
	dir := t.TempDir()
	rel := &plan.Release{
		Pkg:     &model.Package{Name: "core", Dir: dir, Space: space},
		Current: ccme.Version{Major: 1},
		OwnBump: ccme.BumpPatch,
		Bump:    ccme.BumpPatch,
		NewWork: true,
	}
	rel.Next = rel.Current.Bumped(rel.Bump)
	return &plan.Plan{
		Order:     []string{"core"},
		Releases:  map[string]*plan.Release{"core": rel},
		Providers: map[string][]string{},
	}, dir
}

func TestScriptOutputsFlowDownstream(t *testing.T) {
	// Every script and hook can export — bare NAME=value and the
	// DISPAT_OUTPUT_-prefixed spelling address the same output; each export
	// reaches everything that runs later as DISPAT_OUTPUT_<NAME> together
	// with DISPAT_OUTPUT_SOURCE_<NAME> naming the exporting script,
	// accumulating across the pipeline, with a later re-export overriding
	// the earlier value (and its source).
	space := &model.Space{
		Name:            "libs",
		BuildScript:     []string{`echo "FROM_BUILD=one" >> "$DISPAT_OUTPUT"`},
		PostBuildScript: []string{`env > postbuild.env && echo "DISPAT_OUTPUT_FROM_HOOK=two" >> "$DISPAT_OUTPUT"`},
		PublishScript:   []string{`env > publish.env && echo "DISPAT_OUTPUT_FROM_BUILD=three" >> "$DISPAT_OUTPUT"`},
		AnnounceScript:  []string{`env > announce.env`},
	}
	p, dir := shellPlan(t, space)

	exec := &Executor{Runner: &script.ShellRunner{}, Log: zerolog.Nop()}
	results := exec.Run(context.Background(), p)
	require.Equal(t, StatusPublished, results["core"].Status, "err: %v", results["core"].Err)

	read := func(f string) string {
		data, err := os.ReadFile(filepath.Join(dir, f))
		require.NoError(t, err, f)
		return string(data)
	}
	post := read("postbuild.env")
	assert.Contains(t, post, "DISPAT_OUTPUT_FROM_BUILD=one\n", "the build's export reaches its own postBuild hook")
	assert.Contains(t, post, "DISPAT_OUTPUT_SOURCE_FROM_BUILD=core:build\n", "the provenance travels alongside")
	assert.Contains(t, post, "DISPAT_OUTPUTS=FROM_BUILD\n")

	pub := read("publish.env")
	assert.Contains(t, pub, "DISPAT_OUTPUT_FROM_BUILD=one\n")
	assert.Contains(t, pub, "DISPAT_OUTPUT_FROM_HOOK=two\n", "hook exports accumulate too, prefixed spelling included")
	assert.Contains(t, pub, "DISPAT_OUTPUT_SOURCE_FROM_HOOK=core:postBuild\n")
	assert.Contains(t, pub, "DISPAT_OUTPUTS=FROM_BUILD FROM_HOOK\n")

	ann := read("announce.env")
	assert.Contains(t, ann, "DISPAT_OUTPUT_FROM_BUILD=three\n", "a re-export overrides, like a shell re-assignment")
	assert.Contains(t, ann, "DISPAT_OUTPUT_SOURCE_FROM_BUILD=core:publish\n", "the source follows the override")
	assert.Contains(t, ann, "DISPAT_OUTPUT_FROM_HOOK=two\n")

	assert.Equal(t, []plan.Output{
		{Name: "FROM_BUILD", Value: "three", Source: "core:publish"},
		{Name: "FROM_HOOK", Value: "two", Source: "core:postBuild"},
	}, p.Releases["core"].Outputs)
}

func TestScriptOutputsReachOnFail(t *testing.T) {
	// A failing sequence still surrenders what it exported before the
	// failure, so the outcome scripts can report with it.
	space := &model.Space{
		Name:         "libs",
		BuildScript:  []string{`echo "STEP=compiled" >> "$DISPAT_OUTPUT" && exit 1`},
		OnFailScript: []string{`env > onfail.env`},
	}
	p, dir := shellPlan(t, space)

	exec := &Executor{Runner: &script.ShellRunner{}, Log: zerolog.Nop()}
	results := exec.Run(context.Background(), p)
	require.Equal(t, StatusFailed, results["core"].Status)

	data, err := os.ReadFile(filepath.Join(dir, "onfail.env"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "DISPAT_OUTPUT_STEP=compiled\n")
}

func TestMalformedOutputFailsAGatingStage(t *testing.T) {
	space := &model.Space{
		Name:        "libs",
		BuildScript: []string{`echo "not a key value line" >> "$DISPAT_OUTPUT"`},
	}
	p, _ := shellPlan(t, space)

	exec := &Executor{Runner: &script.ShellRunner{}, Log: zerolog.Nop()}
	results := exec.Run(context.Background(), p)
	require.Equal(t, StatusFailed, results["core"].Status)
	assert.Contains(t, results["core"].Err.Error(), "NAME=value")
}

func TestNoOutputsMeansEmptyListingVariable(t *testing.T) {
	// DISPAT_OUTPUTS is set (empty) even when nothing was exported, so a
	// shell loop iterates zero times instead of reading an unset variable.
	space := &model.Space{
		Name:          "libs",
		BuildScript:   []string{`true`},
		PublishScript: []string{`env > publish.env`},
	}
	p, dir := shellPlan(t, space)

	exec := &Executor{Runner: &script.ShellRunner{}, Log: zerolog.Nop()}
	results := exec.Run(context.Background(), p)
	require.Equal(t, StatusPublished, results["core"].Status)

	data, err := os.ReadFile(filepath.Join(dir, "publish.env"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "DISPAT_OUTPUTS=\n")
	assert.Empty(t, p.Releases["core"].Outputs)
}

func TestParseOutputs(t *testing.T) {
	dir := t.TempDir()
	write := func(lines ...string) string {
		f := filepath.Join(dir, "out")
		require.NoError(t, os.WriteFile(f, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
		return f
	}

	t.Run("values_kept_verbatim_blank_lines_skipped", func(t *testing.T) {
		outs, err := parseOutputs(write("A=one two three", "", "B=with=equals"), "core:build")
		require.NoError(t, err)
		assert.Equal(t, []plan.Output{
			{Name: "A", Value: "one two three", Source: "core:build"},
			{Name: "B", Value: "with=equals", Source: "core:build"},
		}, outs)
	})
	t.Run("prefixed_spelling_addresses_the_same_output", func(t *testing.T) {
		outs, err := parseOutputs(write("DISPAT_OUTPUT_A=1", "A=2"), "core:build")
		require.NoError(t, err)
		assert.Equal(t, []plan.Output{{Name: "A", Value: "2", Source: "core:build"}}, outs,
			"DISPAT_OUTPUT_A and A are one output, the prefix is stripped on parse")
	})
	t.Run("github_export_kept_under_its_full_name", func(t *testing.T) {
		outs, err := parseOutputs(write("DISPAT_EXPORT_GITHUB=/a /b"), "core:publish")
		require.NoError(t, err)
		assert.Equal(t, []plan.Output{{Name: plan.GitHubExport, Value: "/a /b", Source: "core:publish"}}, outs)
	})
	t.Run("reexport_overrides_in_place", func(t *testing.T) {
		outs, err := parseOutputs(write("A=1", "B=2", "A=3"), "core:build")
		require.NoError(t, err)
		assert.Equal(t, []plan.Output{
			{Name: "A", Value: "3", Source: "core:build"},
			{Name: "B", Value: "2", Source: "core:build"},
		}, outs)
	})
	t.Run("malformed_line", func(t *testing.T) {
		_, err := parseOutputs(write("just some text"), "core:build")
		assert.ErrorContains(t, err, "NAME=value")
	})
	t.Run("bad_variable_name", func(t *testing.T) {
		_, err := parseOutputs(write("1BAD=x"), "core:build")
		assert.ErrorContains(t, err, "NAME=value")
	})
	t.Run("reserved_dispat_names_rejected", func(t *testing.T) {
		// Anything else DISPAT_-prefixed would override a real DISPAT_*
		// variable in every later script's environment.
		for _, line := range []string{"DISPAT_PACKAGE=evil", "DISPAT_EXPORT_NPM=x", "DISPAT_OUTPUT_=empty"} {
			_, err := parseOutputs(write(line), "core:build")
			assert.ErrorContains(t, err, "NAME=value", line)
		}
	})
}

func TestLoginExportsReachTheSpacePublishes(t *testing.T) {
	// The login script exports like any other script; its exports are
	// space-scoped and merged into each package at its publish — the one
	// stage that gates on the login — so they reach the publish and
	// everything after it, stamped with the space-level source.
	space := &model.Space{
		Name:          "libs",
		LoginScript:   []string{`echo "DISPAT_OUTPUT_REGISTRY_TOKEN=tkn-123" >> "$DISPAT_OUTPUT"`},
		PublishScript: []string{`env > publish.env`},
	}
	p, dir := shellPlan(t, space)

	exec := &Executor{Runner: &script.ShellRunner{}, Log: zerolog.Nop()}
	results := exec.Run(context.Background(), p)
	require.Equal(t, StatusPublished, results["core"].Status, "err: %v", results["core"].Err)

	data, err := os.ReadFile(filepath.Join(dir, "publish.env"))
	require.NoError(t, err)
	env := string(data)
	assert.Contains(t, env, "DISPAT_OUTPUT_REGISTRY_TOKEN=tkn-123\n")
	assert.Contains(t, env, "DISPAT_OUTPUT_SOURCE_REGISTRY_TOKEN=libs:login\n",
		"the login's exports carry the space-level source")
	assert.Equal(t, []plan.Output{{Name: "REGISTRY_TOKEN", Value: "tkn-123", Source: "libs:login"}},
		p.Releases["core"].Outputs)
}

func TestLoginMalformedExportFailsThePublish(t *testing.T) {
	// The login is a gating sequence: a malformed export fails it — and with
	// it every publish of the space — exactly like a failing login command.
	space := &model.Space{
		Name:          "libs",
		LoginScript:   []string{`echo "not a key value line" >> "$DISPAT_OUTPUT"`},
		PublishScript: []string{`echo publishing`},
	}
	p, _ := shellPlan(t, space)

	exec := &Executor{Runner: &script.ShellRunner{}, Log: zerolog.Nop()}
	results := exec.Run(context.Background(), p)
	require.Equal(t, StatusFailed, results["core"].Status)
	assert.Contains(t, results["core"].Err.Error(), "NAME=value")
}

func TestGitHubExportTravelsUnderItsFullName(t *testing.T) {
	// DISPAT_EXPORT_GITHUB is a directive, not an ordinary output: later
	// scripts read it back under its full name, and the DISPAT_OUTPUTS
	// listing does not carry it.
	space := &model.Space{
		Name:          "libs",
		BuildScript:   []string{`echo "DISPAT_EXPORT_GITHUB=/dist/app.tgz" >> "$DISPAT_OUTPUT"`},
		PublishScript: []string{`env > publish.env`},
	}
	p, dir := shellPlan(t, space)

	exec := &Executor{Runner: &script.ShellRunner{}, Log: zerolog.Nop()}
	results := exec.Run(context.Background(), p)
	require.Equal(t, StatusPublished, results["core"].Status, "err: %v", results["core"].Err)

	data, err := os.ReadFile(filepath.Join(dir, "publish.env"))
	require.NoError(t, err)
	env := string(data)
	assert.Contains(t, env, "DISPAT_EXPORT_GITHUB=/dist/app.tgz\n")
	assert.Contains(t, env, "DISPAT_OUTPUTS=\n", "the directive is not an ordinary output")
	value, ok := p.Releases["core"].Output(plan.GitHubExport)
	require.True(t, ok, "the recorder reads the export from the release")
	assert.Equal(t, "/dist/app.tgz", value)
}

func TestCommandEnv(t *testing.T) {
	// The environment `dispat run` hands a run script: the package variables,
	// the workspace listing, the provider updates (all considered live) and
	// the accumulated outputs.
	libs := &model.Space{Name: "libs"}
	provider := &plan.Release{
		Pkg:     &model.Package{Name: "core", Dir: "core", Space: libs},
		Current: ccme.Version{Major: 1}, OwnBump: ccme.BumpMinor, Bump: ccme.BumpMinor, NewWork: true,
	}
	provider.Next = provider.Current.Bumped(provider.Bump)
	consumer := &plan.Release{
		Pkg:     &model.Package{Name: "app", Dir: "app", Space: libs},
		Current: ccme.Version{Major: 2}, Bump: ccme.BumpPatch, NewWork: true,
		DueTo:   []string{"core"},
		Outputs: []plan.Output{{Name: "STEP", Value: "done"}},
	}
	consumer.Next = consumer.Current.Bumped(consumer.Bump)
	p := &plan.Plan{
		Order:     []string{"core", "app"},
		Releases:  map[string]*plan.Release{"core": provider, "app": consumer},
		Providers: map[string][]string{"app": {"core"}},
	}
	fillUpdates(p)

	env := CommandEnv(p, "app", "run:lint", WorkspaceEnv(p, zerolog.Nop()))
	got := map[string]bool{}
	for _, e := range env {
		got[e] = true
	}
	assert.True(t, got["DISPAT_PACKAGE=app"])
	assert.True(t, got["DISPAT_STAGE=run:lint"])
	assert.True(t, got["DISPAT_NEW_VERSION=2.0.1"])
	assert.True(t, got["DISPAT_WORKSPACE_CORE_VERSION=1.1.0"], "the workspace listing travels")
	assert.True(t, got["DISPAT_UPDATED_CORE_NEW_VERSION=1.1.0"], "provider updates are all considered live")
	assert.True(t, got["DISPAT_OUTPUT_STEP=done"], "accumulated outputs travel too")
}
