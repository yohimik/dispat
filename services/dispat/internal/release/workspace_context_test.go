package release

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/workspaceenv"
)

func TestWorkspaceOwnersDoNotAuthorizeAmbiguousPackageKeys(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a.b", "a-b", "lib", "tool"}})
	p.Releases["a.b"].Pkg.Repository = "first"
	p.Releases["a-b"].Pkg.Repository = "second"
	p.Releases["lib"].Pkg.Repository = "first"
	p.Releases["tool"].Pkg.Repository = "first"
	env := WorkspaceEnv(p, zerolog.Nop())
	var owners map[string]string
	for _, pair := range env {
		if value, ok := strings.CutPrefix(pair, workspaceenv.Owners+"="); ok {
			require.NoError(t, json.Unmarshal([]byte(value), &owners))
		}
	}
	assert.Equal(t, map[string]string{"PACKAGE_LIB": "first", "PACKAGE_TOOL": "first"}, owners)
}

func TestImportedSpacesWithEqualNamesHaveIndependentLoginGates(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a", "b", "c", "d"}})
	spaces := make(map[string]*model.Space)
	for _, name := range p.Order {
		owner := "first"
		if name == "c" || name == "d" {
			owner = "second"
		}
		if spaces[owner] == nil {
			space := *p.Releases[name].Pkg.Space
			space.Repository = owner
			space.LoginScript = []string{"login-" + owner}
			spaces[owner] = &space
		}
		p.Releases[name].Pkg.Repository = owner
		p.Releases[name].Pkg.Space = spaces[owner]
	}
	runner := &fakeRunner{}
	results := newExecutor(execSpec{Runner: runner, Tagger: &fakeTagger{}, Changelog: &fakeChangelog{}, Build: 4, Publish: 4}).Run(t.Context(), p)
	for _, name := range p.Order {
		assert.Equal(t, StatusPublished, results[name].Status)
	}
	assert.Equal(t, 1, runner.countPrefix("login-first"))
	assert.Equal(t, 1, runner.countPrefix("login-second"))
}
