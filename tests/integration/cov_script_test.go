// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// The arguments an operator types after `--` are appended to a script's
// command text, because a dispat script is an opaque string and the shell it
// runs under is configurable. Appending is the one mechanism every shell
// shares — and it is also the one that turns an argument carrying a space, a
// quote or nothing at all into something else entirely unless it is quoted on
// the way.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestRunQuotesForwardedArgumentsTheShellWouldOtherwiseRead: an ordinary flag
// goes through verbatim, which is what keeps the assembled command readable
// and keeps it correct under a shell that is not POSIX. An argument that a
// shell would split, take a quote out of, or lose entirely is quoted, and each
// one arrives as the single argument it was typed as.
func TestRunQuotesForwardedArgumentsTheShellWouldOtherwiseRead(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	// The script prints each of its arguments in brackets, so an argument
	// that was split in two, or vanished, is visible as such.
	cfg.Scripts["show"] = models.Script{`printf '<%s>'`}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	res := r.RunScriptOK("show", "--since", "all", "--",
		"--reporter=dot", "two words", "it's", "")
	require.NotEmpty(t, res.Stdout)
	assert.Contains(t, res.Stdout, "<--reporter=dot><two words><it's><>",
		"every argument arrives as the one word it was typed as")
}
