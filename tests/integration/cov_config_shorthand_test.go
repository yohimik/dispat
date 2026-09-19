// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the scalar spellings a config file may use.
//
// Almost every list in a dispat config may be written as the one value it
// holds, and almost every object as the one name that identifies it: a space
// with one folder writes `path: packages`, a script with one command writes
// the command, a changelog with one header line writes the line. The typed
// model always writes the long form, so these shorthands are only ever
// exercised by a file somebody wrote by hand — which is every real config —
// and they are asserted here through a run that uses them.
//
// Their refusals belong here for the same reason: a value the decoder cannot
// read as either spelling must name the key it was written under, because the
// reader is looking at a file, not at a struct.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovConfigScalarSpellingsRelease: a configuration written entirely in
// the one-value spellings loads, discovers, plans and releases exactly as its
// long-form twin does.
func TestCovConfigScalarSpellingsRelease(t *testing.T) {
	r := harness.New(t)
	r.WriteConfigRaw(map[string]any{
		"logLevel":    "info",
		"logFormat":   "json",
		"updateCheck": false,
		"github":      map[string]any{"enabled": false},
		// One value where a pair is allowed, one command where a list is,
		// one folder where a list of folders is, one script reference where
		// a sequence is.
		"concurrency": 1,
		"scripts": map[string]any{
			"build":   "echo building",
			"publish": "echo publishing",
		},
		"spaces": map[string]any{
			"libs": map[string]any{
				"path": "packages",
				"flow": map[string]any{"build": "build", "publish": "publish"},
			},
		},
		"changelog": map[string]any{
			// One entry line where a list is allowed, one built-in section
			// named on its own where a list of section objects is, and a
			// number written as text.
			"header":       "written by the shorthand config",
			"sections":     "Features",
			"entrySpacing": "2",
		},
		// One provider where a list is allowed, on both spellings of the
		// dependency declaration.
		"dependencies": map[string]any{"app": "core"},
		"packages":     map[string]any{"tool": map[string]any{"dependencies": "core"}},
	})
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "app")
	r.SeedPackage("packages", "tool")
	r.Commit("feat(core,app,tool): bootstrap the shorthand repository")

	res := r.ReleaseOK("--log-format", "json")
	for _, name := range []string{"core", "app", "tool"} {
		assert.True(t, r.IsTagged(name+"@0.1.0"), "%s must be released; tags: %v", name, r.TagList())
	}
	assert.Contains(t, changelogOf(t, r, "core"), "written by the shorthand config",
		"the single header line reached the record")
	assert.Contains(t, changelogOf(t, r, "core"), "Features",
		"the single named section reached the record")
	require.NotEmpty(t, res.Events)
}

// TestCovConfigRefusesValuesNeitherSpellingCanRead: a value that is neither
// the one thing nor a list of them names the key it was written under, so a
// reader can find it in the file.
func TestCovConfigRefusesValuesNeitherSpellingCanRead(t *testing.T) {
	r := harness.New(t)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	base := func() map[string]any {
		return map[string]any{
			"logFormat":   "json",
			"updateCheck": false,
			"github":      map[string]any{"enabled": false},
			"scripts":     map[string]any{"build": "echo building", "publish": "echo publishing"},
			"spaces": map[string]any{
				"libs": map[string]any{
					"path": "packages",
					"flow": map[string]any{"build": "build", "publish": "publish"},
				},
			},
		}
	}

	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"a script that is an object", func(c map[string]any) {
			c["scripts"] = map[string]any{"build": map[string]any{"run": "echo"}}
		}, "scripts"},
		{"a space path that is an object", func(c map[string]any) {
			c["spaces"] = map[string]any{"libs": map[string]any{
				"path": map[string]any{"first": "packages"},
				"flow": map[string]any{"build": "build", "publish": "publish"},
			}}
		}, "path"},
		{"a section that is a number", func(c map[string]any) {
			c["changelog"] = map[string]any{"sections": []any{7}}
		}, "a built-in section name or an object"},
		{"an entry line that is a number", func(c map[string]any) {
			c["changelog"] = map[string]any{"header": []any{7}}
		}, "header"},
		{"an entry spacing that is not a number", func(c map[string]any) {
			c["changelog"] = map[string]any{"entrySpacing": "many"}
		}, "entrySpacing"},
		{"a dependency object that is a number", func(c map[string]any) {
			c["dependencies"] = 7
		}, "dependencies"},
		{"a provider list that is an object", func(c map[string]any) {
			c["packages"] = map[string]any{"core": map[string]any{
				"dependencies": map[string]any{"provider": map[string]any{"name": "x"}},
			}}
		}, "dependencies"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(cfg)
			r.WriteConfigRaw(cfg)
			res := r.Status("--log-format", "json")
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, diagnosticText(res), tc.want)
			assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
		})
	}
}
