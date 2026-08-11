package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// loggedApp returns an App whose JSON log lines land in the buffer, which is
// what the report helpers render into.
func loggedApp(t *testing.T) (*App, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return New(t.TempDir(), &config.File{}, zerolog.New(&buf)), &buf
}

// reportPlan is a three-package plan: one releasing, one held, one unchanged.
func reportPlan() *plan.Plan {
	libs := &model.Space{Name: "libs"}
	rel := func(n string) *plan.Release {
		return &plan.Release{
			Pkg:     &model.Package{Name: n, Dir: n, Space: libs},
			Current: ccme.Version{Major: 1},
		}
	}
	changed := rel("changed")
	changed.Bump, changed.OwnBump, changed.NewWork = ccme.BumpMinor, ccme.BumpMinor, true
	changed.Next = ccme.Version{Major: 1, Minor: 1}
	held := rel("held")
	held.Bump, held.NewWork, held.Held = ccme.BumpPatch, true, true
	held.Next = ccme.Version{Major: 1, Patch: 1}
	return &plan.Plan{
		Order: []string{"changed", "held", "quiet"},
		Releases: map[string]*plan.Release{
			"changed": changed, "held": held, "quiet": rel("quiet"),
		},
		Providers: map[string][]string{"changed": {"quiet"}},
	}
}

func TestShortCommit(t *testing.T) {
	assert.Equal(t, "abc123", shortCommit("abc123"))
	long := strings.Repeat("0123456789ab", 4)
	assert.Equal(t, long[:12], shortCommit(long))
}

func TestPrintDiagnostics(t *testing.T) {
	a, buf := loggedApp(t)
	a.printDiagnostics(&plan.Plan{Diagnostics: []plan.Diagnostic{
		{Code: "W131", Level: plan.LevelWarn, Pkg: "core",
			Commit: strings.Repeat("a", 40), Message: "unit addresses no package"},
		{Code: "E200", Level: plan.LevelError, Message: "dependency cycle"},
	}})
	out := buf.String()
	assert.Contains(t, out, `"code":"W131"`)
	assert.Contains(t, out, `"commit":"aaaaaaaaaaaa"`, "commits render shortened")
	assert.Contains(t, out, `"code":"E200"`)
	assert.Contains(t, out, `"warnings":1`)
	assert.Contains(t, out, `"errors":1`)

	buf.Reset()
	a.printDiagnostics(&plan.Plan{})
	assert.Empty(t, buf.String(), "a clean plan reports nothing")
}

func TestPrintGraph(t *testing.T) {
	a, buf := loggedApp(t)
	a.printGraph(reportPlan())
	out := buf.String()
	assert.Contains(t, out, "unchanged")
	assert.Contains(t, out, "held (Release-As: none)")
	assert.Contains(t, out, "changed")
	assert.Contains(t, out, `"version":"1.0.0 -> 1.1.0"`)
	assert.Contains(t, out, `"dependsOn":["quiet"]`)
	assert.Contains(t, out, `"packages":3`)
	assert.Contains(t, out, `"releasing":1`)
	assert.Contains(t, out, `"held":1`)
}

func TestSummarize(t *testing.T) {
	a, buf := loggedApp(t)
	pl := reportPlan()
	results := map[string]*release.Result{
		"changed": {Name: "changed", Status: release.StatusFailed,
			FailedStage: "build", Err: assert.AnError,
			From: ccme.Version{Major: 1}, To: ccme.Version{Major: 1, Minor: 1}},
	}
	failed := a.summarize(pl, results, 3*time.Second)
	assert.Equal(t, 1, failed)
	out := buf.String()
	assert.Contains(t, out, `"status":"failed"`)
	assert.Contains(t, out, `"failedStage":"build"`)
	assert.Contains(t, out, `"failed":1`)
	assert.Contains(t, out, `"held":1`)

	buf.Reset()
	results["changed"].Status = release.StatusPublished
	results["quiet"] = &release.Result{Name: "quiet", Status: release.StatusSkipped,
		Blocked: true, BlockedBy: "changed"}
	results["held"] = &release.Result{Name: "held", Status: release.StatusCancelled}
	assert.Equal(t, 0, a.summarize(pl, results, time.Second))
	out = buf.String()
	assert.Contains(t, out, `"published":1`)
	assert.Contains(t, out, `"skipped":1`)
	assert.Contains(t, out, `"cancelled":1`)
	assert.Contains(t, out, `"blockedBy":"changed"`)
}

func TestPreviewOne(t *testing.T) {
	a, _ := loggedApp(t)
	pl := reportPlan()
	assert.Empty(t, a.previewOne(pl.Releases["quiet"]), "nothing pending, nothing rendered")

	changed := pl.Releases["changed"]
	changed.Units = []*ccme.Unit{{
		Header: ccme.Header{Type: "feat", Description: "add streaming"},
		Bump:   ccme.BumpMinor,
		Valid:  true,
	}}
	notes := a.previewOne(changed)
	assert.Contains(t, notes, "## changed@1.1.0", "the header carries the tag")
	assert.Contains(t, notes, "add streaming")
}

// TestPrintDiagnosticsQuietParser: parser.quiet hides the commit-message
// parser's findings and nothing else. The planner's own diagnostics still
// print, every diagnostic is still counted, and the summary says how many
// lines went unprinted — a silent hidden error would be worse than a noisy
// one.
func TestPrintDiagnosticsQuietParser(t *testing.T) {
	diags := []plan.Diagnostic{
		{Code: ccme.CodeE100, Level: plan.LevelError, Commit: "abcdef1234567890", Message: "header does not match"},
		{Code: ccme.CodeW140, Level: plan.LevelWarn, Commit: "abcdef1234567890", Message: "unknown type"},
		{Code: plan.CodeCatchUp, Level: plan.LevelWarn, Pkg: "core", Message: "catching up"},
		{Code: plan.CodeDependencyCycle, Level: plan.LevelError, Message: "cycle"},
	}

	for name, tc := range map[string]struct {
		quiet   bool
		printed []string
		absent  []string
		summary string
	}{
		"loud, the default": {
			quiet:   false,
			printed: []string{"E100", "W140", plan.CodeCatchUp, plan.CodeDependencyCycle},
			summary: `"warnings":2,"errors":2`,
		},
		"quiet": {
			quiet:   true,
			printed: []string{plan.CodeCatchUp, plan.CodeDependencyCycle},
			absent:  []string{"E100", "W140", "header does not match", "unknown type"},
			summary: `"warnings":2,"errors":2,"hidden":2`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			cfg := &config.File{Parser: &config.ParserConfig{Quiet: tc.quiet}}
			a := New(t.TempDir(), cfg, zerolog.New(&buf))
			a.printDiagnostics(&plan.Plan{Diagnostics: diags})

			out := buf.String()
			for _, want := range tc.printed {
				assert.Contains(t, out, want)
			}
			for _, unwanted := range tc.absent {
				assert.NotContains(t, out, unwanted)
			}
			assert.Contains(t, out, tc.summary,
				"a hidden diagnostic is still counted, and the count says how many are hidden")
		})
	}
}

// TestPrintDiagnosticsQuietWithNothingHidden: the hidden count is only
// reported when something actually was, so an ordinary quiet run reads the
// same as a loud one.
func TestPrintDiagnosticsQuietWithNothingHidden(t *testing.T) {
	var buf bytes.Buffer
	cfg := &config.File{Parser: &config.ParserConfig{Quiet: true}}
	a := New(t.TempDir(), cfg, zerolog.New(&buf))
	a.printDiagnostics(&plan.Plan{Diagnostics: []plan.Diagnostic{
		{Code: plan.CodeCatchUp, Level: plan.LevelWarn, Pkg: "core", Message: "catching up"},
	}})
	assert.Contains(t, buf.String(), `"warnings":1,"errors":0`)
	assert.NotContains(t, buf.String(), "hidden")
}
