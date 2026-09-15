package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestPrintDiagnosticsUsesRepositoryQuietPolicy(t *testing.T) {
	for _, controlQuiet := range []bool{false, true} {
		t.Run(fmt.Sprintf("control quiet %t", controlQuiet), func(t *testing.T) {
			a, output := loggedApp(t)
			a.cfg.Parser = &config.ParserConfig{Quiet: controlQuiet}
			a.workspace = &config.Workspace{Repositories: []config.Repository{
				{Name: "control", Config: a.cfg, Control: true},
				{Name: "silent", Config: &config.File{Parser: &config.ParserConfig{Quiet: true}}},
				{Name: "loud", Config: &config.File{Parser: &config.ParserConfig{Quiet: false}}},
				{Name: "defaults", Config: &config.File{}},
				{Name: "no-config"},
			}}
			var diagnostics []plan.Diagnostic
			for _, owner := range []string{"control", "silent", "loud", "defaults", ""} {
				diagnostics = append(diagnostics, plan.Diagnostic{
					Code: "E140", Level: plan.LevelError, Repository: owner,
					Commit: strings.Repeat("a", 40), Message: "parser for " + owner,
				})
			}
			diagnostics = append(diagnostics, plan.Diagnostic{
				Code: "E330", Level: plan.LevelError, Repository: "silent", Message: "invalid repository",
			})
			a.printDiagnostics(&plan.Plan{Diagnostics: diagnostics})
			text := output.String()
			assert.NotContains(t, text, `"message":"parser for silent"`)
			assert.Contains(t, text, `"message":"parser for loud"`)
			assert.Contains(t, text, `"repository":"loud"`)
			assert.Contains(t, text, `"commit":"aaaaaaaaaaaa"`)
			assert.Contains(t, text, `"code":"E330","repository":"silent"`)
			assert.Contains(t, text, `"errors":6`, "display never changes error accounting")
			if controlQuiet {
				assert.NotContains(t, text, `"message":"parser for control"`)
				assert.NotContains(t, text, `"message":"parser for defaults"`)
				assert.Contains(t, text, `"hidden":4`)
			} else {
				assert.Contains(t, text, `"message":"parser for control"`)
				assert.Contains(t, text, `"message":"parser for defaults"`)
				assert.Contains(t, text, `"hidden":1`)
			}
		})
	}
}
