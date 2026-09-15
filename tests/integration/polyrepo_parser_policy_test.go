// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// Parser display policy belongs to the commit's source, independently of
// the control policy; an explicit invocation flag overrides every source.
func TestPolyrepoParserQuietFollowsDiagnosticRepository(t *testing.T) {
	for _, controlQuiet := range []bool{false, true} {
		t.Run(fmt.Sprintf("control quiet %t", controlQuiet), func(t *testing.T) {
			control := harness.New(t)
			var imports []string
			for _, sourceName := range []string{"quiet-source", "loud-source"} {
				source := harness.New(t)
				source.SeedPackage("packages", sourceName)
				cfg := polyrepoFile()
				delete(cfg, "polyrepo")
				cfg["spaces"] = centralSpaces(map[string]string{"packages": "packages"})
				cfg["parser"] = map[string]any{
					"quiet": sourceName == "quiet-source", "strictTypes": true,
					"types": map[string]string{"feat": "minor", "fix": "patch", "chore": "none"},
				}
				writePolyrepoJSON(t, source, "dispat.json", cfg)
				source.Commit("feat(" + sourceName + "): initial source")
				source.CommitEmpty("wat(" + sourceName + "): undeclared source type")
				path := "sources/" + sourceName
				addPolyrepoSource(t, control, sourceName, path, source)
				imports = append(imports, path+"/dispat.json")
			}
			cfg := polyrepoFile()
			cfg["configs"] = imports
			cfg["parser"] = map[string]any{"quiet": controlQuiet}
			writePolyrepoJSON(t, control, "dispat.json", cfg)
			control.Commit("chore: assemble parser policies")

			for _, tc := range []struct {
				name, flag string
				want       []string
			}{
				{name: "source configuration", want: []string{"loud-source"}},
				{name: "show all", flag: "--quiet-parser=false", want: []string{"quiet-source", "loud-source"}},
				{name: "hide all", flag: "--quiet-parser=true"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					args := []string{"status"}
					if tc.flag != "" {
						args = append(args, tc.flag)
					}
					result := control.Command(args...)
					require.Zero(t, result.Code, "%s\n%s", result.Stdout, result.Stderr)
					var reported []string
					for _, event := range result.Events {
						if event.Str("code") == "E140" {
							reported = append(reported, event.Str("repository"))
							assert.Len(t, event.Str("commit"), 12, "public commit abbreviations stay unchanged")
						}
					}
					assert.ElementsMatch(t, tc.want, reported, "%s", result.Stdout)
				})
			}
		})
	}
}
