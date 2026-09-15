// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package cli

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExplicitWorkspaceFlagsDoNotInheritRunPins(t *testing.T) {
	t.Setenv(nestedWorkspaceRootEnv, t.TempDir())
	t.Setenv(nestedWorkspaceConfigEnv, "dispat.json")
	t.Setenv(nestedWorkspaceImportsEnv, `[]`)
	for _, flag := range []string{"", "root", "config", "configs", "polyrepo"} {
		t.Run(flag, func(t *testing.T) {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			o := declareFlags(fs)
			if flag != "" {
				value := "alternate"
				if flag == "polyrepo" {
					value = "true"
				}
				require.NoError(t, fs.Set(flag, value))
			}
			require.NoError(t, applyNestedWorkspace(fs, o))
			assert.Equal(t, flag == "", o.nestedWorkspace)
		})
	}
}
