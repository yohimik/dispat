// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// `parser.propagation.kinds`: which dependency edges a propagation walks.
//
// The list has three states, and the whole point of these scenarios is that
// they are three rather than two. An absent key is the specification default
// of §8.4 (every field but `devDependencies`); a list naming fields walks
// those fields; and a list written empty walks nothing at all, which is how a
// repository turns the propagation graph off without touching a single commit
// message. "Absent" and "empty" are opposite instructions, so a run that
// confuses them propagates along every edge exactly where the author asked for
// none.
//
// Each case is one repository with two consumers of `core` — one over a plain
// `dependencies` edge, one over a `devDependencies` edge — so a single history
// shows both which edges were walked and which were passed over.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// propagationKindsConfig is the fixture config: `near` consumes `core` as a
// runtime dependency, `tool` consumes it as a development one. Passing kinds
// writes the `parser.propagation.kinds` key; passing nothing leaves the whole
// parser object out, which is the "absent" case.
func propagationKindsConfig(kinds ...string) models.File {
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{
		{Consumer: "near", Provider: "core"},
		{Consumer: "tool", Provider: "core", Kind: "devDependencies"},
	}
	if kinds != nil {
		cfg.Parser = &models.ParserConfig{
			Propagation: &models.ParserPropagationConfig{Kinds: kinds},
		}
	}
	return cfg
}

// propagationKindsRepo seeds the three packages and commits the history every
// case shares: a seeding commit, then one caret commit on `core` that offers a
// propagated patch one edge out.
func propagationKindsRepo(t *testing.T, write func(*harness.Repo)) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	write(r)
	for _, name := range []string{"core", "near", "tool"} {
		r.SeedPackage("packages", name)
	}
	r.Commit("chore: seed the fleet")
	r.WriteFile("packages/core/work.txt", "work\n")
	r.Commit("feat(core)^: work that propagates one level")
	return r
}

// TestPropagationKindsSelectTheEdgesTraversed: the three states of
// `parser.propagation.kinds`, read off one history each.
func TestPropagationKindsSelectTheEdgesTraversed(t *testing.T) {
	t.Run("an absent list walks the default kinds", func(t *testing.T) {
		r := propagationKindsRepo(t, func(r *harness.Repo) {
			r.WriteConfigModel(propagationKindsConfig())
		})

		res := r.StatusOK()
		assert.Equal(t, "minor", harness.GraphLine(res.Events, "core").Str("bump"),
			"the package that changed is bumped: %s", res.Stdout)
		assert.Equal(t, "propagated from core", harness.GraphLine(res.Events, "near").Str("reason"),
			"the runtime dependent is reached: %s", res.Stdout)
		assert.Equal(t, "unchanged", harness.GraphLine(res.Events, "tool").Str("message"),
			"and the development dependent is not, which is the §8.4 default: %s", res.Stdout)
	})

	t.Run("an empty list walks no edge at all", func(t *testing.T) {
		r := propagationKindsRepo(t, func(r *harness.Repo) {
			r.WriteConfigRaw(rawPropagationKinds(t, propagationKindsConfig(), []any{}))
		})

		res := r.StatusOK()
		assert.Equal(t, "minor", harness.GraphLine(res.Events, "core").Str("bump"),
			"the package that changed is still bumped: %s", res.Stdout)
		assert.Equal(t, "unchanged", harness.GraphLine(res.Events, "near").Str("message"),
			"the empty list is an instruction, not an absence: %s", res.Stdout)
		assert.Equal(t, "unchanged", harness.GraphLine(res.Events, "tool").Str("message"),
			"so nothing downstream moves: %s", res.Stdout)
	})

	t.Run("the wildcard walks every kind", func(t *testing.T) {
		r := propagationKindsRepo(t, func(r *harness.Repo) {
			r.WriteConfigModel(propagationKindsConfig("*"))
		})

		res := r.StatusOK()
		assert.Equal(t, "propagated from core", harness.GraphLine(res.Events, "near").Str("reason"),
			"the runtime dependent is reached: %s", res.Stdout)
		assert.Equal(t, "propagated from core", harness.GraphLine(res.Events, "tool").Str("reason"),
			"and so is the development one the default leaves out: %s", res.Stdout)
	})

	t.Run("an empty list stops the channel axis as well", func(t *testing.T) {
		// Both axes walk the same edges (§8.4), so the empty list has to
		// reach the channel traversal too. The provider still takes the
		// channel it asked for, which is what keeps this from passing on a
		// directive nobody read.
		r := harness.New(t)
		r.WriteConfigRaw(rawPropagationKinds(t, propagationKindsConfig(), []any{}))
		for _, name := range []string{"core", "near", "tool"} {
			r.SeedPackage("packages", name)
		}
		r.Commit("chore: seed the fleet")
		r.CommitEmpty("release(core)%beta%%beta: propose beta downstream")

		res := r.StatusOK()
		assert.Equal(t, "stable -> beta", harness.GraphLine(res.Events, "core").Str("channel"),
			"the package the directive names still moves: %s", res.Stdout)
		assert.Equal(t, "unchanged", harness.GraphLine(res.Events, "near").Str("message"),
			"and no channel travels down an edge the empty list excludes: %s", res.Stdout)
	})

	t.Run("a named list walks those kinds alone", func(t *testing.T) {
		r := propagationKindsRepo(t, func(r *harness.Repo) {
			r.WriteConfigModel(propagationKindsConfig("devDependencies"))
		})

		res := r.StatusOK()
		assert.Equal(t, "unchanged", harness.GraphLine(res.Events, "near").Str("message"),
			"the kind the list leaves out is not walked: %s", res.Stdout)
		assert.Equal(t, "propagated from core", harness.GraphLine(res.Events, "tool").Str("reason"),
			"and the one it names is: %s", res.Stdout)
	})
}

// rawPropagationKinds renders the typed config and writes one key back into it
// as a raw value. `kinds: []` is the shape the model cannot express — the
// field's `omitempty` drops a present empty list on the way out — so the empty
// case, and only the empty case, goes through WriteConfigRaw.
func rawPropagationKinds(t *testing.T, cfg models.File, kinds any) map[string]any {
	t.Helper()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	raw["parser"] = map[string]any{"propagation": map[string]any{"kinds": kinds}}
	return raw
}
