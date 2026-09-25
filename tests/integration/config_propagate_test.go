// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 10: the propagate stage's two names. `autoPropagate` is `autoVersion`
// and `flow.propagate`, `flow.beforePropagate` and `flow.postPropagate` are the
// version entries, each pair one setting. A file stating both spellings of a
// pair in one object is refused before any work, in every format the
// configuration is read from.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestConfigPropagateSynonymsInOneObjectAreRefused: one object naming both
// spellings of a pair stops the run before a script or a tag, and the message
// names both keys and the object that wrote them. A layer naming one spelling
// beside an inherited other loads, because a nearer layer replaces both.
func TestConfigPropagateSynonymsInOneObjectAreRefused(t *testing.T) {
	for _, c := range []struct {
		name, file, body string
		want             []string
	}{
		{"json space entry", "dispat.json", `{
  "scripts": {"build": "echo building", "publish": "echo publishing", "sync": "echo sync"},
  "spaces": {"libs": {"path": "packages", "flow": {"build": "build", "publish": "publish"},
    "autoVersion": {"range": "exact"}, "autoPropagate": {"range": "tilde"}}}
}`, []string{"libs", "autoVersion and autoPropagate are mutually exclusive"}},
		{"yaml package entry", "dispat.yaml", `scripts:
  build: echo building
  publish: echo publishing
  sync: echo sync
spaces:
  libs:
    path: packages
    flow: {build: build, publish: publish}
packages:
  core:
    flow:
      version: sync
      propagate: sync
`, []string{"core", "flow.version and flow.propagate are mutually exclusive"}},
		{"yaml top level", "dispat.yaml", `scripts:
  build: echo building
  publish: echo publishing
spaces:
  libs:
    path: packages
    flow: {build: build, publish: publish}
autoVersion: {range: exact}
autoPropagate: {range: tilde}
`, []string{"autoVersion and autoPropagate are mutually exclusive"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := harness.New(t)
			r.WriteFile(c.file, c.body)
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): first release")

			res := r.Release()
			assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
			for _, want := range c.want {
				assert.Contains(t, res.Stdout+res.Stderr, want)
			}
			assert.Empty(t, r.TagList(), "a refused configuration releases nothing")
		})
	}

	// One spelling per layer is the ordinary override: the package's
	// flow.propagate replaces the version entry its space states.
	r := harness.New(t)
	r.WriteFile("dispat.yaml", `scripts:
  build: echo building
  publish: echo publishing
  sync: echo sync
  mine: echo "$DISPAT_STAGE" > ../../stage.txt
spaces:
  libs:
    path: packages
    flow: {build: build, publish: publish, version: sync}
    autoVersion: {manifests: none}
packages:
  core:
    flow: {propagate: mine}
`)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	assert.Equal(t, "version\n", readFile(t, r, "stage.txt"),
		"the package's propagate entry ran, under the stage's runtime name")
}
