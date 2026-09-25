// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"bytes"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	public "github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// TestWorkerFindsItsMailboxInTheRepositoryItRunsIn: a worker that states no
// endpoint reads its work from the push URL of the remote the repository it
// runs in releases to, and one that states an endpoint keeps it. Outside a
// repository, with a remote that pushes to several places, and with a push URL
// carrying a credential, it is refused with E225 naming both remedies and
// never the credential.
func TestWorkerFindsItsMailboxInTheRepositoryItRunsIn(t *testing.T) {
	const secret = "ghs_itFAKE"
	settings := &public.ExecutionConfig{Name: "build-a", SecretEnv: executionSecretEnv}

	t.Run("an endpoint it states", func(t *testing.T) {
		a := New(t.TempDir(), &config.File{}, zerolog.Nop())
		endpoint, err := a.resolveWorkerEndpoint(t.Context(),
			&public.ExecutionConfig{Name: "build-a", Endpoint: "/srv/mailbox.git"})
		require.NoError(t, err)
		assert.Equal(t, "/srv/mailbox.git", endpoint)
	})

	t.Run("the remote of the checkout it runs in", func(t *testing.T) {
		root, a := guardRepo(t, &config.File{})
		recordGit(t, root, "remote", "add", "origin", "/srv/origin.git")
		endpoint, err := a.resolveWorkerEndpoint(t.Context(), settings)
		require.NoError(t, err)
		assert.Equal(t, "/srv/origin.git", endpoint)
	})

	for name, setup := range map[string]func(t *testing.T) *App{
		"outside a repository": func(t *testing.T) *App {
			return New(t.TempDir(), &config.File{}, zerolog.Nop())
		},
		"a remote that pushes to several places": func(t *testing.T) *App {
			root, a := guardRepo(t, &config.File{})
			recordGit(t, root, "remote", "add", "origin", "/srv/one.git")
			recordGit(t, root, "remote", "set-url", "--add", "--push", "origin", "/srv/one.git")
			recordGit(t, root, "remote", "set-url", "--add", "--push", "origin", "/srv/two.git")
			return a
		},
		"a push URL carrying a credential": func(t *testing.T) *App {
			root, a := guardRepo(t, &config.File{})
			recordGit(t, root, "remote", "add", "origin",
				"https://x-access-token:"+secret+"@example.invalid/acme/project.git")
			return a
		},
		"a remote name that is not configured": func(t *testing.T) *App {
			_, a := guardRepo(t, &config.File{Commit: &config.CommitConfig{Remote: "upstream"}})
			return a
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := setup(t)
			var logs bytes.Buffer
			a.log = zerolog.New(&logs)

			_, err := a.resolveWorkerEndpoint(t.Context(), settings)

			require.Error(t, err)
			assert.Equal(t, execution.CodeConfiguration, config.DiagnosticCode(err))
			assert.Contains(t, err.Error(), "execution.endpoint")
			assert.Contains(t, err.Error(), "start the worker in a checkout")
			assert.NotContains(t, err.Error()+logs.String(), secret)
		})
	}
}
