// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for webhooks: the resolution a trigger falls back to when
// the workspace cannot be walked, the flush deadline that abandons deliveries
// rather than holding a command open, and a secret variable nobody set.
//
// All three are about a webhook failing to be ideal without any of it reaching
// the command's exit code, which is the feature's whole contract: a script
// must not be able to fail its stage by reporting progress.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestWebhookTriggerFallsBackWhenTheWorkspaceCannotBeWalked: a trigger is a
// leaf command and must not fail over what a release would refuse. With the
// workspace unreadable there is no per-package routing to resolve, so the
// top-level list is resolved unrestricted — every event reaches it — and the
// command says why it could not do better.
func TestWebhookTriggerFallsBackWhenTheWorkspaceCannotBeWalked(t *testing.T) {
	sink := newWebhookSink(t)
	r := harness.New(t)
	cfg := webhooksConfig(echoBuild, models.WebhookConfig{URL: sink.srv.URL})
	// A space whose folder is not there: a load concern nobody has, and a
	// discovery failure every command that walks the workspace meets.
	cfg.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: models.PathList{"packages-that-were-moved"}, Flow: buildPublish()},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	// The release the same configuration would refuse, for contrast: the
	// discovery failure is real, and only the trigger tolerates it.
	res := r.Release()
	assert.NotEqual(t, 0, res.Code, "a release will not run against a workspace it cannot walk")

	res = r.Command("trigger", "smoke-passed", "all", "green")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "workspace discovery failed")

	payload := sink.find(t, "script.smoke-passed")
	assert.Equal(t, "all green", payload["message"])
	assert.Nil(t, payload["package"], "outside a run there is no package to name")
}

// TestWebhookWithoutItsSecretDeliversUnsigned: a secret named in the
// configuration and missing from the environment is the shape a receiver
// silently stops verifying under. The deliveries still go out, because a
// notification is not a security boundary, and the run says out loud that
// nothing is signing them.
func TestWebhookWithoutItsSecretDeliversUnsigned(t *testing.T) {
	sink := newWebhookSink(t)
	r := harness.New(t)
	r.WriteConfigModel(webhooksConfig(echoBuild,
		models.WebhookConfig{URL: sink.srv.URL, SecretEnv: "DISPAT_IT_SECRET_NOBODY_SET"}))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	res := r.ReleaseOK()
	assert.Contains(t, res.Stdout, "deliveries are unsigned")
	assert.Contains(t, res.Stdout, "DISPAT_IT_SECRET_NOBODY_SET", "the variable to set is named")

	deliveries := sink.all()
	require.NotEmpty(t, deliveries, "the release still reported itself")
	for _, d := range deliveries {
		assert.Empty(t, d.Header.Get("X-Dispat-Signature"),
			"nothing may look signed when nothing signed it")
	}
	assert.True(t, r.IsTagged("core@0.1.0"), "and the release is unaffected; tags: %v", r.TagList())
}
