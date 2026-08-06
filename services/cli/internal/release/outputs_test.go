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

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/cli/internal/model"
	"github.com/yohimik/dispat/services/cli/internal/plan"
	"github.com/yohimik/dispat/services/cli/internal/script"
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
		Consumers: map[string][]string{},
	}, dir
}

func TestScriptOutputsFlowDownstream(t *testing.T) {
	// Every script and hook can export; each export reaches everything that
	// runs later as DISPAT_OUTPUT_<NAME>, accumulating across the pipeline,
	// with a later re-export overriding the earlier value.
	space := &model.Space{
		Name:            "libs",
		BuildScript:     []string{`echo "FROM_BUILD=one" >> "$DISPAT_OUTPUT"`},
		PostBuildScript: []string{`env > postbuild.env && echo "FROM_HOOK=two" >> "$DISPAT_OUTPUT"`},
		PublishScript:   []string{`env > publish.env && echo "FROM_BUILD=three" >> "$DISPAT_OUTPUT"`},
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
	assert.Contains(t, post, "DISPAT_OUTPUTS=FROM_BUILD\n")

	pub := read("publish.env")
	assert.Contains(t, pub, "DISPAT_OUTPUT_FROM_BUILD=one\n")
	assert.Contains(t, pub, "DISPAT_OUTPUT_FROM_HOOK=two\n", "hook exports accumulate too")
	assert.Contains(t, pub, "DISPAT_OUTPUTS=FROM_BUILD FROM_HOOK\n")

	ann := read("announce.env")
	assert.Contains(t, ann, "DISPAT_OUTPUT_FROM_BUILD=three\n", "a re-export overrides, like a shell re-assignment")
	assert.Contains(t, ann, "DISPAT_OUTPUT_FROM_HOOK=two\n")

	assert.Equal(t, []plan.Output{{Name: "FROM_BUILD", Value: "three"}, {Name: "FROM_HOOK", Value: "two"}},
		p.Releases["core"].Outputs)
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
		outs, err := parseOutputs(write("A=one two three", "", "B=with=equals"))
		require.NoError(t, err)
		assert.Equal(t, []plan.Output{{Name: "A", Value: "one two three"}, {Name: "B", Value: "with=equals"}}, outs)
	})
	t.Run("reexport_overrides_in_place", func(t *testing.T) {
		outs, err := parseOutputs(write("A=1", "B=2", "A=3"))
		require.NoError(t, err)
		assert.Equal(t, []plan.Output{{Name: "A", Value: "3"}, {Name: "B", Value: "2"}}, outs)
	})
	t.Run("malformed_line", func(t *testing.T) {
		_, err := parseOutputs(write("just some text"))
		assert.ErrorContains(t, err, "NAME=value")
	})
	t.Run("bad_variable_name", func(t *testing.T) {
		_, err := parseOutputs(write("1BAD=x"))
		assert.ErrorContains(t, err, "NAME=value")
	})
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
		Consumers: map[string][]string{"core": {"app"}},
	}

	env := CommandEnv(p, "app", "run:lint", zerolog.Nop())
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
