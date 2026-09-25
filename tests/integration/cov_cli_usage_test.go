// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the command lines dispat refuses to run at all.
//
// A usage mistake must cost the reader a usage message, not a configuration
// error two phases later, and it must be told apart from a run that failed:
// dispat exits 2 for "this command line does not mean anything" and 1 for
// "the work did not succeed". Every arity rule, every flag that belongs to
// another command, and every enumerated flag value is a place those two can
// be confused, so each is driven through the binary and each asserts the
// exit code as well as the sentence.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestCovNestedWorkspaceContextIsRefusedWhenItCannotBeRead: an owner-aware
// script runner hands a nested dispat the composed invocation through the
// environment. A context that does not decode is a usage refusal rather than
// a silent fall back to this folder's own configuration, because the two
// would plan different things.
func TestCovNestedWorkspaceContextIsRefusedWhenItCannotBeRead(t *testing.T) {
	r := usageRepo(t)
	script := strings.Join([]string{
		"DISPAT_INTERNAL_WORKSPACE_ROOT=" + harness.ShQuote(r.Root),
		"DISPAT_INTERNAL_WORKSPACE_CONFIG=" + harness.ShQuote(r.Path("dispat.json")),
		"DISPAT_INTERNAL_WORKSPACE_CONFIGS='[not json'",
		"dispat status",
	}, " ")

	res := r.Shell(script)
	assert.Equal(t, 2, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stderr, "invalid nested workspace context")
}
