package app

import (
	"bytes"
	"errors"
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
	failed, critical := a.summarize(pl, results, 3*time.Second)
	assert.Equal(t, 1, failed)
	assert.Zero(t, critical)
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
	failed, critical = a.summarize(pl, results, time.Second)
	assert.Zero(t, failed)
	assert.Zero(t, critical)
	assert.NotContains(t, buf.String(), `"critical"`,
		"a healthy run does not carry the field, so the eye does not learn to skip it")
	out = buf.String()
	assert.Contains(t, out, `"published":1`)
	assert.Contains(t, out, `"skipped":1`)
	assert.Contains(t, out, `"cancelled":1`)
	assert.Contains(t, out, `"blockedBy":"changed"`)
}

// TestSummarizeReportsCriticals: a package that published but could not be
// tagged is still published, and the summary has to say both things. The line
// is an error rather than the usual info, because "released, and nothing
// recorded it" is not something to find later by reading carefully.
func TestSummarizeReportsCriticals(t *testing.T) {
	a, buf := loggedApp(t)
	pl := reportPlan()
	results := map[string]*release.Result{
		"changed": {Name: "changed", Status: release.StatusPublished,
			Critical: []error{errors.New("E220: tagging failed: tag refused")},
			From:     ccme.Version{Major: 1}, To: ccme.Version{Major: 1, Minor: 1}},
	}

	failed, critical := a.summarize(pl, results, time.Second)
	assert.Zero(t, failed, "a released package is not a failed one")
	assert.Equal(t, 1, critical)

	out := buf.String()
	assert.Contains(t, out, `"status":"published"`)
	assert.Contains(t, out, `"level":"error"`, "the package's own line is an error")
	assert.Contains(t, out, "tag refused")
	assert.Contains(t, out, `"critical":1`, "and the totals carry the count")
}

func TestPreviewOne(t *testing.T) {
	a, _ := loggedApp(t)
	pl := reportPlan()
	assert.Empty(t, a.previewOne(pl.Releases["quiet"], PreviewOptions{}),
		"nothing pending, nothing rendered")

	changed := pl.Releases["changed"]
	changed.Units = []*ccme.Unit{{
		Header: ccme.Header{Type: "feat", Description: "add streaming"},
		Bump:   ccme.BumpMinor,
		Valid:  true,
	}}
	notes := a.previewOne(changed, PreviewOptions{})
	assert.Contains(t, notes, "## changed@1.1.0", "the header carries the tag")
	assert.Contains(t, notes, "add streaming")
}

// TestPreviewOneBodies: which bodies a preview renders, and how it labels them
// when it renders both. Neither flag is the changelog entry, which is what a
// bare preview has always printed.
func TestPreviewOneBodies(t *testing.T) {
	newPreview := func(t *testing.T) (*App, *plan.Release) {
		t.Helper()
		a, _ := loggedApp(t)
		rel := reportPlan().Releases["changed"]
		// The channel the planner resolves for every release; the record
		// policies are read against it.
		rel.Channel, rel.BaselineChannel = ccme.ChannelStable, ccme.ChannelStable
		rel.Units = []*ccme.Unit{{
			Header: ccme.Header{Type: "feat", Description: "add streaming"},
			Bump:   ccme.BumpMinor,
			Valid:  true,
		}}
		rel.Pkg.Changelog = model.ChangelogSpec{Enabled: true,
			Format: model.RecordFormat{Footer: []model.EntryLine{{Line: []string{"from the changelog"}}}}}
		rel.Pkg.GitHub = model.GitHubSpec{Enabled: true,
			Format: model.RecordFormat{
				ReleaseName: "changed ${DISPAT_VERSION}",
				Footer:      []model.EntryLine{{Line: []string{"from the release"}}},
			}}
		return a, rel
	}

	t.Run("neither flag prints the changelog entry", func(t *testing.T) {
		a, rel := newPreview(t)
		notes := a.previewOne(rel, PreviewOptions{})
		assert.Equal(t, notes, a.previewOne(rel, PreviewOptions{Changelog: true}),
			"which is what --changelog prints too")
		assert.Contains(t, notes, "from the changelog")
		assert.NotContains(t, notes, "from the release")
		assert.NotContains(t, notes, "---", "one body needs no label")
	})

	t.Run("the github body carries the github format", func(t *testing.T) {
		a, rel := newPreview(t)
		notes := a.previewOne(rel, PreviewOptions{GitHub: true})
		assert.Contains(t, notes, "from the release")
		assert.Contains(t, notes, "### changed 1.1.0", "the release name heads it")
		assert.NotContains(t, notes, "from the changelog")
	})

	t.Run("both are printed under one header, labelled", func(t *testing.T) {
		a, rel := newPreview(t)
		notes := a.previewOne(rel, PreviewOptions{Changelog: true, GitHub: true})
		assert.Equal(t, 1, strings.Count(notes, "## changed@1.1.0"), "one package, one header")
		assert.Contains(t, notes, "--- changelog ---")
		assert.Contains(t, notes, "--- github release ---")
		assert.Less(t, strings.Index(notes, "--- changelog ---"),
			strings.Index(notes, "--- github release ---"), "the changelog comes first")
		assert.Contains(t, notes, "from the changelog")
		assert.Contains(t, notes, "from the release")
	})

	t.Run("a policy that would write nothing says so", func(t *testing.T) {
		a, rel := newPreview(t)
		rel.Pkg.GitHub.Channels = []string{"beta"}
		assert.Contains(t, a.previewOne(rel, PreviewOptions{GitHub: true}),
			"github release withheld: the channels do not admit stable")

		rel.Pkg.GitHub.Enabled = false
		assert.Contains(t, a.previewOne(rel, PreviewOptions{GitHub: true}),
			"github release withheld: disabled by config")

		rel.Pkg.Changelog.Channels = []string{"beta"}
		assert.Contains(t, a.previewOne(rel, PreviewOptions{Changelog: true}),
			"changelog entry withheld: the channels do not admit stable")
		assert.Contains(t, a.previewOne(rel, PreviewOptions{}), "from the changelog",
			"a bare preview shows the notes rather than the policy")
	})
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
