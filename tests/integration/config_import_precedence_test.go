// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestConfigImportReferencesRespectMergePrecedence exercises the actual
// workspace composition after root-object references merge. The location of
// the winning declaration matters: each imported file is resolved from the
// fragment that contributed its value, not from the control root.
func TestConfigImportReferencesRespectMergePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, second, direct, wanted string
	}{
		{name: "later reference wins", second: `{"configs":"../sources/two/dispat.json"}`, wanted: "two"},
		{name: "unrelated later reference leaves import in place", second: `{"logLevel":"debug"}`, wanted: "one"},
		{name: "root declaration wins", second: `{"configs":"../sources/two/dispat.json"}`, direct: "sources/one/dispat.json", wanted: "one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			control := importFleet(t)
			control.WriteFile("cfg/first.json", `{"configs":"../sources/one/dispat.json"}`+"\n")
			control.WriteFile("cfg/second.json", tc.second+"\n")
			root := map[string]any{
				"polyrepo": true, "logFormat": "json", "updateCheck": false,
				"github": map[string]any{"enabled": false},
				"$ref":   []string{"cfg/first.json", "cfg/second.json"},
			}
			if tc.direct != "" {
				root["configs"] = tc.direct
			}
			control.WriteConfigRaw(root)
			control.Commit("chore: choose workspace imports")

			found := importedPackages(control.StatusOK())
			assert.True(t, found[tc.wanted], "winning declaration must compose %s: %v", tc.wanted, found)
			other := "one"
			if tc.wanted == "one" {
				other = "two"
			}
			assert.False(t, found[other], "the shadowed declaration must not import %s: %v", other, found)
		})
	}
}
