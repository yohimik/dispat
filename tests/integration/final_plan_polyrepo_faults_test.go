// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Final production-review fault scenarios for an orchestrated workspace.
//
// Composition and planning ask different Git questions. A source can have a
// valid checkout while any one of its identity, completeness, pin, ref, or
// history inquiries fails. These cases keep those boundaries observable at the
// real CLI and prove a second run converges once Git answers again.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

type finalPolyrepoFixture struct {
	control *harness.Repo
}

func finalPolyrepo(t *testing.T) finalPolyrepoFixture {
	t.Helper()
	source := harness.New(t)
	source.SeedPackage("packages", "core")
	source.Commit("feat(core): bootstrap source")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: assemble workspace")
	return finalPolyrepoFixture{control: control}
}

// TestFinalPolyrepoCompositionFaultsNameTheUntrustedBoundary: before package
// discovery, composition proves the control repository, submodule inventory,
// source identity, source completeness, and exact control pin. Every failed
// proof is E330 and a healthy retry composes the same checkout.
func TestFinalPolyrepoCompositionFaultsNameTheUntrustedBoundary(t *testing.T) {
	type faultCase struct {
		name, pattern, want string
	}
	for _, tc := range []faultCase{
		{
			name:    "control repository completeness",
			pattern: "*rev-parse --is-shallow-repository*",
			want:    "is not initialized",
		},
		{
			name:    "control HEAD",
			pattern: "* rev-parse HEAD",
			want:    "control repository has no HEAD",
		},
		{
			name:    "submodule inventory",
			pattern: "*config --file * --null --get-regexp*",
			want:    "read .gitmodules",
		},
		{
			name:    "source Git root",
			pattern: "*-C */sources/lib rev-parse --show-toplevel*",
			want:    "is not initialized at",
		},
		{
			name:    "source repository completeness",
			pattern: "*-C */sources/lib rev-parse --is-shallow-repository*",
			want:    "is not initialized",
		},
		{
			name:    "control gitlink pin",
			pattern: "*rev-parse *:sources/lib*",
			want:    "is not pinned by control HEAD",
		},
		{
			name:    "source HEAD",
			pattern: "*-C */sources/lib rev-parse HEAD",
			want:    "has no HEAD",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := finalPolyrepo(t)
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: tc.pattern})

			res := f.control.CommandEnv(fault.Env(), "status")
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			combined := res.Stdout + res.Stderr
			assert.Contains(t, combined, tc.want)
			assert.Contains(t, combined, "E330")
			assert.NotContains(t, combined, "release plan ready")
			assert.Equal(t, 1, fault.Matches(), "the selected composition inquiry ran once")

			healed := f.control.StatusOK()
			assert.Equal(t, "0.0.0 -> 0.1.0", harness.GraphLine(healed.Events, "core").Str("version"),
				"the unchanged checkout composes and plans once Git answers")
		})
	}
}

// TestFinalPolyrepoMalformedSubmoduleInventoryCannotEraseARepository: a
// successful git-config process is not enough; each record must still carry
// the key/path framing Git promises. Corrupt output cannot be treated as an
// empty inventory and thereby remove a source from the workspace.
func TestFinalPolyrepoMalformedSubmoduleInventoryCannotEraseARepository(t *testing.T) {
	f := finalPolyrepo(t)
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*config --file * --null --get-regexp*",
		Output:  "submodule.lib-source.path",
	})

	res := f.control.CommandEnv(fault.Env(), "status")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "malformed git config output")
	assert.Contains(t, combined, "E330")
	assert.NotContains(t, combined, "release plan ready")
	assert.Equal(t, 1, fault.Matches(), "the corrupt inventory reply was consumed once")
	assert.Empty(t, polyrepoTags(f.control, "sources/lib"), "composition failure records no release")
}

// TestFinalPolyrepoMalformedRepositoryFactsFailClosed: completeness and HEAD
// are small Git replies but still untrusted protocol. Unknown booleans, an
// invalid object id, and whitespace in place of a control log cannot be
// accepted as a complete repository, a real revision, or an empty history.
func TestFinalPolyrepoMalformedRepositoryFactsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fault   harness.GitFault
		want    string
		matches int
	}{
		{
			name:  "control completeness",
			fault: harness.GitFault{Pattern: "*rev-parse --is-shallow-repository*", Output: "unknown"},
			want:  "malformed completeness reply",
		},
		{
			name:  "source composition completeness",
			fault: harness.GitFault{Pattern: "*-C */sources/lib rev-parse --is-shallow-repository*", Output: "unknown"},
			want:  "malformed completeness reply",
		},
		{
			name: "source planning completeness",
			fault: harness.GitFault{
				Pattern: "*-C */sources/lib rev-parse --is-shallow-repository*", Nth: 2, Output: "unknown",
			},
			want:    "malformed shallow-repository reply",
			matches: 2,
		},
		{
			name:  "source planning HEAD",
			fault: harness.GitFault{Pattern: "*-C */sources/lib *rev-parse HEAD^{commit}*", Output: "not-an-object-id"},
			want:  "malformed commit object id",
		},
		{
			name:  "blank control history",
			fault: harness.GitFault{Pattern: "*log --topo-order*", Output: " "},
			want:  "malformed empty control history",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := finalPolyrepo(t)
			fault := harness.NewGitFault(t, tc.fault)

			res := f.control.CommandEnv(fault.Env(), "status")
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			combined := res.Stdout + res.Stderr
			assert.Contains(t, combined, tc.want)
			assert.NotContains(t, combined, "release plan ready")
			wantMatches := tc.matches
			if wantMatches == 0 {
				wantMatches = 1
			}
			assert.Equal(t, wantMatches, fault.Matches(), "the malformed Git reply was consumed at its boundary")
			assert.Empty(t, polyrepoTags(f.control, "sources/lib"), "an invalid repository fact records no release")
		})
	}
}

// TestFinalPolyrepoBaselineFaultsRefuseAnUnprovenRevision: an explicit
// repository baseline is configuration only after its revision resolves to a
// commit reachable from the named repository's HEAD. A failed object lookup or
// ancestry proof is E333 and never reaches release planning.
func TestFinalPolyrepoBaselineFaultsRefuseAnUnprovenRevision(t *testing.T) {
	for _, tc := range []struct {
		name, pattern, want string
	}{
		{
			name:    "revision object",
			pattern: "*-C */sources/lib rev-parse --verify HEAD^{commit}*",
			want:    "is not a commit",
		},
		{
			name:    "revision reachability",
			pattern: "*-C */sources/lib merge-base --is-ancestor * HEAD*",
			want:    "is not reachable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := finalPolyrepo(t)
			cfg := polyrepoFile()
			cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
			cfg["repositoryBaselines"] = []any{map[string]any{
				"consumer": "core", "releaseTag": "core@0.1.0",
				"repository": "lib-source", "revision": "HEAD",
			}}
			writePolyrepoJSON(t, f.control, "dispat.json", cfg)
			f.control.Git("-C", "sources/lib", "tag", "-a", "core@0.1.0", "-m", "known release")
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: tc.pattern})

			res := f.control.CommandEnv(fault.Env(), "status")
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			combined := res.Stdout + res.Stderr
			assert.Contains(t, combined, tc.want)
			assert.NotContains(t, combined, "release plan ready")
			assert.Equal(t, 1, fault.Matches(), "the baseline proof was attempted once")
			assert.Contains(t, combined, "E333")
		})
	}
}

// TestFinalPolyrepoPlanningFaultsDoNotShrinkTheFleetSnapshot: after successful
// composition, planning still has to recheck history completeness and read the
// source refs, source commits, and control gitlink history. None of those may
// be interpreted as an empty repository when Git refuses the inquiry.
func TestFinalPolyrepoPlanningFaultsDoNotShrinkTheFleetSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fault   harness.GitFault
		want    string
		matches int
	}{
		{
			name:    "planner source completeness",
			fault:   harness.GitFault{Pattern: "*-C */sources/lib rev-parse --is-shallow-repository*", Nth: 2},
			want:    "checking repository lib-source completeness",
			matches: 2,
		},
		{
			name:  "source release refs",
			fault: harness.GitFault{Pattern: "*tag --list --merged HEAD*"},
			want:  "repository lib-source loading tags",
		},
		{
			name:  "source planning HEAD",
			fault: harness.GitFault{Pattern: "*-C */sources/lib *rev-parse HEAD^{commit}*"},
			want:  "reading repository lib-source HEAD",
		},
		{
			name:  "source pending commits",
			fault: harness.GitFault{Pattern: "*log --format=*--diff-merges=first-parent HEAD*"},
			want:  "lib-source history for core",
		},
		{
			name:  "control gitlink history",
			fault: harness.GitFault{Pattern: "*log --topo-order*"},
			want:  "indexing control checkpoints",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := finalPolyrepo(t)
			fault := harness.NewGitFault(t, tc.fault)

			res := f.control.CommandEnv(fault.Env(), "status")
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			combined := res.Stdout + res.Stderr
			assert.Contains(t, combined, harness.GitFaultMarker)
			assert.Contains(t, combined, tc.want)
			assert.NotContains(t, combined, "release plan ready")
			wantMatches := tc.matches
			if wantMatches == 0 {
				wantMatches = 1
			}
			assert.Equal(t, wantMatches, fault.Matches(), "the selected planning inquiry count")
			assert.Empty(t, polyrepoTags(f.control, "sources/lib"), "status records no source release")
		})
	}
}

// TestFinalPolyrepoMalformedControlHistoryIsNotAnEmptyCheckpointIndex: the
// control log is a framed protocol between Git and the planner. A successful
// process with corrupt framing is an error, never an empty checkpoint index
// that would change every source boundary.
func TestFinalPolyrepoMalformedControlHistoryIsNotAnEmptyCheckpointIndex(t *testing.T) {
	f := finalPolyrepo(t)
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*log --topo-order*",
		Output:  "unexpected-control-history",
	})

	res := f.control.CommandEnv(fault.Env(), "status")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "malformed control history marker")
	assert.Contains(t, combined, "indexing control checkpoints")
	assert.NotContains(t, combined, "release plan ready")
	assert.Equal(t, 1, fault.Matches(), "the corrupt control history reply was consumed once")
	assert.Empty(t, polyrepoTags(f.control, "sources/lib"), "planning failure records no release")
}

// TestFinalPolyrepoMalformedSourceHistoryCannotShrinkThePendingWindow: Git may
// exit successfully while a proxy, wrapper, or damaged process supplies a
// truncated framed log. That reply is not an empty source history; status
// fails before presenting or recording a plan with the pending commit erased.
func TestFinalPolyrepoMalformedSourceHistoryCannotShrinkThePendingWindow(t *testing.T) {
	f := finalPolyrepo(t)
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*log --format=*--diff-merges=first-parent HEAD*",
		Output:  "truncated-unframed-commit",
	})

	res := f.control.CommandEnv(fault.Env(), "status")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "malformed commit log record")
	assert.Contains(t, combined, "lib-source history for core")
	assert.NotContains(t, combined, "release plan ready")
	assert.Equal(t, 1, fault.Matches(), "the corrupt source history reply was consumed once")
	assert.Empty(t, polyrepoTags(f.control, "sources/lib"), "a truncated window records no release")
}

// TestFinalPolyrepoMalformedTagInventoryCannotEraseThePublishedBaseline: the
// tag inventory establishes both the released version and the lower bound of
// its history window. A truncated record cannot turn an existing 0.1.0 release
// into a fresh package and reinterpret its pending fix as another first release.
func TestFinalPolyrepoMalformedTagInventoryCannotEraseThePublishedBaseline(t *testing.T) {
	f := finalPolyrepo(t)
	f.control.Git("-C", "sources/lib", "tag", "-a", "core@0.1.0", "-m", "known release")
	f.control.WriteFile("sources/lib/packages/core/fix.txt", "pending fix\n")
	commitPolyrepoSource(t, f.control, "sources/lib", "fix(core): pending correction")
	checkpointPolyrepoSource(t, f.control, "sources/lib")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*tag --list --merged HEAD*",
		Output:  "core@0.1.0-without-object-fields",
	})

	res := f.control.CommandEnv(fault.Env(), "status")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "malformed tag inventory record")
	assert.Contains(t, combined, "repository lib-source loading tags")
	assert.NotContains(t, combined, "release plan ready")
	assert.Equal(t, 1, fault.Matches(), "the corrupt ref inventory reply was consumed once")
	assert.Equal(t, []string{"core@0.1.0"}, polyrepoTags(f.control, "sources/lib"),
		"planning failure preserves the only published release")

	healed := f.control.StatusOK()
	assert.Equal(t, "0.1.0 -> 0.1.1", harness.GraphLine(healed.Events, "core").Str("version"),
		"the real ref inventory restores the fix release window")
}

// TestFinalPolyrepoImportedConfigFaultRefusesUnattributedOwnership: an import
// is accepted only after Git attributes the file to the same initialized
// source named by .gitmodules. The source was already validated as a checkout,
// so this selects the later identity inquiry made for the imported file.
func TestFinalPolyrepoImportedConfigFaultRefusesUnattributedOwnership(t *testing.T) {
	f := finalPolyrepo(t)
	sourceCfg := polyrepoFile()
	sourceCfg["polyrepo"] = false
	sourceCfg["spaces"] = centralSpaces(map[string]string{"libs": "packages"})
	writePolyrepoJSON(t, f.control, "sources/lib/dispat.json", sourceCfg)
	commitPolyrepoSource(t, f.control, "sources/lib", "chore: add repository-local release config")
	checkpointPolyrepoSource(t, f.control, "sources/lib")

	controlCfg := polyrepoFile()
	controlCfg["configs"] = []string{"sources/lib/dispat.json"}
	writePolyrepoJSON(t, f.control, "dispat.json", controlCfg)
	f.control.Commit("chore: import source release config")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C */sources/lib rev-parse --show-toplevel*", Nth: 2,
	})

	res := f.control.CommandEnv(fault.Env(), "status")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.Contains(t, combined, "is not inside an initialized Git repository")
	assert.Contains(t, combined, "E330")
	assert.NotContains(t, combined, "release plan ready")
	assert.Equal(t, 2, fault.Matches(), "checkout validation precedes imported-config ownership")
	assert.Empty(t, polyrepoTags(f.control, "sources/lib"), "composition failure records no release")

	healed := f.control.StatusOK()
	assert.Equal(t, "0.0.0 -> 0.1.0", harness.GraphLine(healed.Events, "core").Str("version"),
		"the same imported configuration composes after Git answers")
}

// TestFinalPolyrepoFaultDoesNotDegradeSourceAncestryToHistoryOrder: a source
// correction is valid only when its target is an ancestor in that source's
// DAG. If the source graph itself is unreadable, the composed plan must fail
// rather than treating log order as equivalent to ancestry.
func TestFinalPolyrepoFaultDoesNotDegradeSourceAncestryToHistoryOrder(t *testing.T) {
	f := finalPolyrepo(t)
	target := f.control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	f.control.Git("-C", "sources/lib", "commit", "--allow-empty", "-q", "-m",
		"fix(core): correct bootstrap\n\nEdits: "+target)
	checkpointPolyrepoSource(t, f.control, "sources/lib")
	fault := harness.NewGitFault(t, harness.GitFault{
		Pattern: "*-C */sources/lib *rev-list --parents HEAD*",
	})

	res := f.control.CommandEnv(fault.Env(), "status")
	require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, harness.GitFaultMarker)
	assert.Contains(t, combined, "ancestry query failed")
	assert.NotContains(t, combined, "release plan ready")
	assert.Equal(t, 1, fault.Matches(), "the source DAG was requested once")
	assert.Empty(t, polyrepoTags(f.control, "sources/lib"), "an untrusted source plan records nothing")
}

// TestFinalPolyrepoRunSinceFaultStopsBeforeTheSelectedScript: projecting a
// control revision into source windows first reads the control gitlink history
// and then each source window. Either failure aborts the command before its
// script can act on an incomplete selection.
func TestFinalPolyrepoRunSinceFaultStopsBeforeTheSelectedScript(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fault   harness.GitFault
		want    string
		matches int
		marker  bool
	}{
		{
			name:  "control projection history",
			fault: harness.GitFault{Pattern: "*log --topo-order*"},
			want:  "indexing control checkpoints",
		},
		{
			name: "source selection window",
			fault: harness.GitFault{
				// Repository windows may be read in either order. Select the
				// owner explicitly instead of relying on a shared call ordinal.
				Pattern: "*-C */sources/lib log --format=*--diff-merges=first-parent *..HEAD*",
			},
			want:    "resolving repository lib-source commits",
			matches: 1,
			marker:  true,
		},
		{
			name:   "control snapshot inquiry",
			fault:  harness.GitFault{Pattern: "*ls-tree -r --full-tree -z *"},
			want:   "resolving control gitlinks",
			marker: true,
		},
		{
			name:  "malformed control snapshot record",
			fault: harness.GitFault{Pattern: "*ls-tree -r --full-tree -z *", Output: "not-a-tree-record"},
			want:  "malformed ls-tree record",
		},
		{
			name:  "malformed control snapshot metadata",
			fault: harness.GitFault{Pattern: "*ls-tree -r --full-tree -z *", Output: "160000 commit\tsources/lib"},
			want:  "malformed ls-tree metadata",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := finalPolyrepo(t)
			marker := f.control.Path("ran.txt")
			cfg := polyrepoFile()
			cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
			cfg["scripts"] = map[string]any{
				"build":   []string{"printf 'ran\\n' >> " + harness.ShQuote(marker)},
				"publish": []string{"true"},
			}
			writePolyrepoJSON(t, f.control, "dispat.json", cfg)
			fault := harness.NewGitFault(t, tc.fault)
			since := f.control.Git("rev-parse", "HEAD")

			res := f.control.CommandEnv(fault.Env(), "run", "build", "--since", since)
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			combined := res.Stdout + res.Stderr
			if tc.marker || tc.fault.Output == "" {
				assert.Contains(t, combined, harness.GitFaultMarker)
			}
			assert.Contains(t, combined, tc.want)
			assert.NotContains(t, combined, "run finished")
			wantMatches := tc.matches
			if wantMatches == 0 {
				wantMatches = 1
			}
			assert.Equal(t, wantMatches, fault.Matches(), "the selected history inquiry count")
			assert.NoFileExists(t, marker, "no script runs from an incomplete projected window")
		})
	}
}
