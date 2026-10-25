// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// specsOf states the stages of a package: a bare name is unconstrained, and a
// constrained stage is given whole.
func specsOf(items ...any) []StageSpec {
	var specs []StageSpec
	for _, item := range items {
		switch v := item.(type) {
		case string:
			specs = append(specs, StageSpec{Name: v})
		case StageSpec:
			specs = append(specs, v)
		}
	}
	return specs
}

// resolve resolves specs that are expected to be realisable.
func resolve(t *testing.T, specs ...any) StageGraph {
	t.Helper()
	graph, refusals := ResolveStageGraph(specsOf(specs...))
	require.Empty(t, refusals)
	return graph
}

// edgesOf renders a graph's edges as "before>after" strings.
func edgesOf(graph StageGraph) []string {
	var out []string
	for _, edge := range graph.Edges {
		out = append(out, edge.Before+">"+edge.After)
	}
	return out
}

// gatingOf lists the stages that precede the record.
func gatingOf(graph StageGraph) []string {
	var out []string
	for _, node := range graph.Nodes {
		if node.IsGating {
			out = append(out, node.Name)
		}
	}
	return out
}

// nodesOf lists the node names in order.
func nodesOf(graph StageGraph) []string {
	var out []string
	for _, node := range graph.Nodes {
		out = append(out, node.Name)
	}
	return out
}

// TestStageGraphDefaultChain: with no constraint the built-in stages form the
// default chain, every user stage sits where the test sit and is unordered
// with them and with each other, the record follows the publish, and the
// announce follows the record. Each optional stage left out joins its
// neighbours directly.
func TestStageGraphDefaultChain(t *testing.T) {
	graph := resolve(t, "publish", "zeta", "announce", "build", "version", "alpha", "test")
	assert.Equal(t, []string{"version", "build", "alpha", "test", "zeta", "publish", "record", "announce"}, nodesOf(graph))
	assert.Equal(t, []string{"version", "build", "alpha", "test", "zeta", "publish"}, gatingOf(graph))
	assert.ElementsMatch(t, []string{
		"version>build", "version>alpha", "version>test", "version>zeta", "version>publish", "version>announce",
		"build>alpha", "build>test", "build>zeta", "build>publish",
		"alpha>publish", "test>publish", "zeta>publish",
		"publish>announce", "publish>record", "record>announce",
	}, edgesOf(graph))
	assert.False(t, graph.IsBefore("alpha", "test"))
	assert.False(t, graph.IsBefore("test", "alpha"))
	assert.Equal(t, []string{"version", "build", "alpha", "test", "zeta"}, graph.Predecessors("publish"))
	assert.Equal(t, []string{"version", "build", "alpha", "test", "zeta", "publish", "announce"}, graph.Stages())
	assert.True(t, DefaultStageGraph(nil).IsEmpty())

	for name, row := range map[string]struct {
		stages []string
		nodes  []string
		edges  []string
	}{
		"every built-in stage": {
			[]string{"sign", "version", "syncLock", "build", "test", "publish", "announce"},
			[]string{"sign", "version", "syncLock", "build", "test", "publish", "record", "announce"},
			[]string{
				"sign>version", "sign>build", "sign>test", "sign>publish", "sign>announce",
				"version>syncLock", "version>build", "version>test", "version>publish", "version>announce",
				"syncLock>build", "syncLock>test", "syncLock>publish", "syncLock>announce",
				"build>test", "build>publish", "test>publish", "publish>record", "publish>announce", "record>announce",
			}},
		"the version stage and the build alone": {
			[]string{"version", "build"},
			[]string{"version", "build", "record"},
			[]string{"version>build", "version>record", "build>record"}},
		"no publish": {
			[]string{"version", "build", "test"},
			[]string{"version", "build", "test", "record"},
			[]string{"version>build", "version>test", "version>record", "build>test", "build>record", "test>record"}},
		"no test": {
			[]string{"version", "build", "publish"},
			[]string{"version", "build", "publish", "record"},
			[]string{"version>build", "version>publish", "build>publish", "publish>record"}},
		"an announce and no publish": {
			[]string{"version", "build", "announce"},
			[]string{"version", "build", "record", "announce"},
			[]string{"version>build", "version>record", "version>announce", "build>record", "build>announce", "record>announce"}},
	} {
		t.Run(name, func(t *testing.T) {
			chain := DefaultStageGraph(row.stages)
			assert.Equal(t, row.nodes, nodesOf(chain))
			assert.ElementsMatch(t, row.edges, edgesOf(chain))
		})
	}
}

// TestStageGraphDefaultIsTheUnconstrainedResolution: the graph of a space
// built by hand is the graph its stages would resolve to with no constraint.
func TestStageGraphDefaultIsTheUnconstrainedResolution(t *testing.T) {
	for _, stages := range [][]string{
		{"version", "build"},
		{"version", "build", "test", "publish"},
		{"sign", "version", "syncLock", "build", "test", "publish", "announce"},
		{"version", "build", "lint", "announce"},
		{"version", "build", "docs", "lint", "publish"},
	} {
		specs := make([]any, 0, len(stages))
		for _, name := range stages {
			specs = append(specs, name)
		}
		assert.Equal(t, resolve(t, specs...), DefaultStageGraph(stages), "%v", stages)
	}
}

// TestStageGraphRecordWithoutAPublish: a package with no publish stage is
// recorded after every stage but an announce that does not place itself,
// which follows the record; an announce that places itself precedes the
// record like any other stage.
func TestStageGraphRecordWithoutAPublish(t *testing.T) {
	graph := resolve(t, "version", "build", "lint", "announce")
	assert.Equal(t, []string{"version", "build", "lint", "record", "announce"}, nodesOf(graph))
	assert.Equal(t, []string{"version", "build", "lint"}, gatingOf(graph))
	assert.Equal(t, []string{"version", "build", "lint"}, graph.Predecessors(RecordStage))
	assert.Equal(t, []string{"announce"}, graph.Successors(RecordStage))
	assert.True(t, graph.IsBefore("lint", "announce"), "the announce keeps its default edge from the stages before it")

	moved := resolve(t, "version", "build", StageSpec{Name: "announce", After: []string{"build"}})
	assert.Equal(t, []string{"version", "build", "announce", "record"}, nodesOf(moved))
	assert.Equal(t, []string{"version", "build", "announce"}, gatingOf(moved))
	assert.True(t, moved.IsBefore("announce", RecordStage))

	dropped := resolve(t, "version", "build", "lint", StageSpec{Name: "announce", After: []string{"publish"}})
	assert.Equal(t, edgesOf(graph), edgesOf(dropped), "naming the publish the package lacks places nothing")
}

// TestStageGraphConstraints: the consequences the model names. Restating a
// default is neutral; a stage placing itself only drops the default edges
// into itself, never its neighbours', so the stages around it keep theirs;
// every stage the publish does not reach precedes it; and a parallel
// constraint alone leaves a stage in its default place.
func TestStageGraphConstraints(t *testing.T) {
	plain := resolve(t, "version", "syncLock", "build", "test", "publish")
	restated := resolve(t, "version", "syncLock", StageSpec{Name: "build", After: []string{"syncLock"}}, "test", "publish")
	assert.Equal(t, edgesOf(plain), edgesOf(restated), "restating a default position changes nothing")

	movedPublish := resolve(t, "version", "build", "test", StageSpec{Name: "publish", After: []string{"build"}}, "announce")
	assert.True(t, movedPublish.IsBefore("publish", "announce"), "the announce keeps its own default edge")
	assert.Contains(t, edgesOf(movedPublish), "test>publish", "the publication edge keeps the test before the publish")
	assert.Equal(t, []string{"version", "build", "test", "publish"}, gatingOf(movedPublish))

	movedAnnounce := resolve(t, "version", "build", "test", "publish", StageSpec{Name: "announce", After: []string{"build"}})
	assert.Contains(t, edgesOf(movedAnnounce), "announce>publish", "the announce lands before the publication")
	assert.Contains(t, edgesOf(movedAnnounce), "test>publish", "the publish keeps its own default edge")
	assert.NotContains(t, edgesOf(movedAnnounce), "publish>announce")
	assert.True(t, movedAnnounce.IsBefore("announce", RecordStage))
	assert.Equal(t, []string{"version", "build", "test", "publish", "announce"}, gatingOf(movedAnnounce))
	assert.Equal(t, []string{"version", "build", "test", "publish", "record", "announce"}, nodesOf(movedAnnounce),
		"the record stays right after the publish in the stage order")
	assert.False(t, movedAnnounce.IsBefore("announce", "test") || movedAnnounce.IsBefore("test", "announce"), "unordered with the test")

	testFirst := resolve(t, "version", "build", "publish", StageSpec{Name: "test", Before: []string{"build"}})
	assert.ElementsMatch(t, []string{
		"version>build", "version>test", "version>publish", "build>publish",
		"test>build", "test>publish", "publish>record",
	}, edgesOf(testFirst), "the build's own default edge comes from the version stage, so the test may precede it")

	beside := resolve(t, "version", "build", "test", StageSpec{Name: "lint", Parallel: []string{"test"}}, "publish")
	assert.Contains(t, edgesOf(beside), "build>lint", "a parallel constraint keeps the default edges")
	assert.Contains(t, edgesOf(beside), "lint>publish")
	assert.False(t, beside.IsBefore("lint", "test") || beside.IsBefore("test", "lint"))

	plainLint := resolve(t, "version", "build", "lint", "publish")
	for name, lint := range map[string]StageSpec{
		"the test the package lacks":     {Name: "lint", After: []string{"test"}},
		"an announce the package lacks":  {Name: "lint", Parallel: []string{"announce"}, Before: []string{"sign"}},
		"the lock-file stage it lacks":   {Name: "lint", After: []string{"syncLock"}},
		"the version stage's other name": {Name: "lint", After: []string{"propagate", "build"}},
	} {
		dropped := resolve(t, "version", "build", lint, "publish")
		assert.Equal(t, edgesOf(plainLint), edgesOf(dropped), name)
	}
	placedByTheRest := resolve(t, "version", "build", "test", StageSpec{Name: "lint", After: []string{"test", "sign"}}, "publish")
	assert.Contains(t, edgesOf(placedByTheRest), "test>lint")
	assert.NotContains(t, edgesOf(placedByTheRest), "build>lint", "the remaining constraint still places the stage")

	noPublish := resolve(t, "version", "build", StageSpec{Name: "lint", Before: []string{"publish"}})
	assert.Equal(t, edgesOf(resolve(t, "version", "build", "lint")), edgesOf(noPublish))
}

// TestStageGraphRefusals: every condition is refused with its own code, in
// the order §19.2a gives, and a package that fails one condition is not
// checked against a later one.
func TestStageGraphRefusals(t *testing.T) {
	for name, row := range map[string]struct {
		specs []StageSpec
		want  []StageDiagnostic
	}{
		"a word that is no stage, once per stage and name in the stage order": {
			specsOf("version", "build", StageSpec{Name: "lint", After: []string{"record", "zz", "docs", "zz"}},
				StageSpec{Name: "alpha", Before: []string{"nowhere"}}),
			[]StageDiagnostic{
				{Code: CodeStageUnknown, Condition: 1, Stage: "alpha", Names: []string{"nowhere"}},
				{Code: CodeStageUnknown, Condition: 1, Stage: "lint", Names: []string{"docs"}},
				{Code: CodeStageUnknown, Condition: 1, Stage: "lint", Names: []string{"zz"}},
				{Code: CodeStageUnknown, Condition: 1, Stage: "lint", Names: []string{"record"}},
			}},
		"a stage naming itself": {
			specsOf("version", StageSpec{Name: "build", After: []string{"build"}}, StageSpec{Name: "lint", Parallel: []string{"lint"}}),
			[]StageDiagnostic{
				{Code: CodeStageUnknown, Condition: 1, Stage: "build", Names: []string{"build"}},
				{Code: CodeStageUnknown, Condition: 1, Stage: "lint", Names: []string{"lint"}},
			}},
		"the version stage naming itself by its other name": {
			specsOf(StageSpec{Name: "version", Before: []string{"propagate"}}, "build"),
			[]StageDiagnostic{{Code: CodeStageUnknown, Condition: 1, Stage: "version", Names: []string{"version"}}}},
		"before and after naming one stage is a cycle": {
			specsOf("version", "build", StageSpec{Name: "lint", Before: []string{"build"}, After: []string{"build"}}),
			[]StageDiagnostic{{Code: CodeStageCycle, Condition: 2, Names: []string{"build", "lint"}}}},
		"a cycle of two user stages": {
			specsOf("version", "build", StageSpec{Name: "lint", After: []string{"docs"}}, StageSpec{Name: "docs", After: []string{"lint"}}),
			[]StageDiagnostic{{Code: CodeStageCycle, Condition: 2, Names: []string{"docs", "lint"}}}},
		"a preparing stage moved behind the build": {
			specsOf(StageSpec{Name: "version", After: []string{"build"}}, "build"),
			[]StageDiagnostic{{Code: CodeStageCycle, Condition: 2, Names: []string{"version", "build"}}}},
		"a stage after an announce that does not place itself": {
			specsOf("version", "build", "announce", StageSpec{Name: "notify", After: []string{"announce"}}),
			[]StageDiagnostic{{Code: CodeStageCycle, Condition: 2, Names: []string{"notify", "announce"}}}},
		"a user stage after a publish that does not place itself": {
			specsOf("version", "build", "publish", StageSpec{Name: "lint", After: []string{"publish"}}),
			[]StageDiagnostic{{Code: CodeStageCycle, Condition: 2, Names: []string{"lint", "publish"}}}},
		"a parallel pair the required edges relate": {
			specsOf("version", "build", StageSpec{Name: "lint", Parallel: []string{"version"}}),
			[]StageDiagnostic{{Code: CodeStageParallelRelated, Condition: 3, Stage: "version", Names: []string{"lint"}}}},
		"a parallel pair a constraint relates": {
			specsOf("version", "build", StageSpec{Name: "lint", After: []string{"build"}, Parallel: []string{"build"}}),
			[]StageDiagnostic{{Code: CodeStageParallelRelated, Condition: 3, Stage: "build", Names: []string{"lint"}}}},
		"a stage beside the publish it precedes by default": {
			specsOf("version", "build", "publish", StageSpec{Name: "lint", Parallel: []string{"publish"}}),
			[]StageDiagnostic{{Code: CodeStageParallelRelated, Condition: 3, Stage: "lint", Names: []string{"publish"}}}},
		"a stage beside the publish it precedes by the publication edge": {
			specsOf("version", "build", StageSpec{Name: "publish", After: []string{"build"}},
				StageSpec{Name: "lint", Parallel: []string{"publish"}}),
			[]StageDiagnostic{{Code: CodeStageParallelRelated, Condition: 3, Stage: "lint", Names: []string{"publish"}}}},
		"a stage beside the announce that follows the record": {
			specsOf("version", "build", StageSpec{Name: "publish", After: []string{"build"}}, "announce",
				StageSpec{Name: "lint", After: []string{"build"}, Parallel: []string{"announce"}}),
			[]StageDiagnostic{{Code: CodeStageParallelRelated, Condition: 3, Stage: "lint", Names: []string{"announce"}}}},
		"several pairs in the stage order of the lesser stage": {
			specsOf("version", "build", "publish", StageSpec{Name: "lint", Parallel: []string{"publish", "version"}},
				StageSpec{Name: "alpha", After: []string{"build"}, Parallel: []string{"build"}}),
			[]StageDiagnostic{
				{Code: CodeStageParallelRelated, Condition: 3, Stage: "version", Names: []string{"lint"}},
				{Code: CodeStageParallelRelated, Condition: 3, Stage: "build", Names: []string{"alpha"}},
				{Code: CodeStageParallelRelated, Condition: 3, Stage: "lint", Names: []string{"publish"}},
			}},
		"the first condition stops the others": {
			specsOf("version", "build", StageSpec{Name: "lint", After: []string{"docs"}},
				StageSpec{Name: "docs", After: []string{"lint"}, Parallel: []string{"ghost"}}),
			[]StageDiagnostic{{Code: CodeStageUnknown, Condition: 1, Stage: "docs", Names: []string{"ghost"}}}},
		"the second condition stops the third": {
			specsOf("version", "build", StageSpec{Name: "lint", After: []string{"docs"}},
				StageSpec{Name: "docs", After: []string{"lint"}, Parallel: []string{"version"}}),
			[]StageDiagnostic{{Code: CodeStageCycle, Condition: 2, Names: []string{"docs", "lint"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			graph, refusals := ResolveStageGraph(row.specs)
			assert.True(t, graph.IsEmpty())
			require.Len(t, refusals, len(row.want))
			for i, want := range row.want {
				got := refusals[i]
				assert.Equal(t, want.Code, got.Code)
				assert.Equal(t, want.Condition, got.Condition)
				assert.Equal(t, want.Stage, got.Stage)
				assert.Equal(t, want.Names, got.Names)
				assert.Equal(t, want.Code, got.DiagnosticCode())
				assert.Equal(t, "execution-configuration", got.DiagnosticCategory())
				assert.Contains(t, got.Message, "stage order condition")
			}
		})
	}
}

// TestStageGraphParallelRefusalSaysHowToRunBesideTheRelease: a pair only the
// publication edges relate is refused with the way out, placing the stage
// after the publish, which then resolves.
func TestStageGraphParallelRefusalSaysHowToRunBesideTheRelease(t *testing.T) {
	_, refusals := ResolveStageGraph(specsOf("version", "build", StageSpec{Name: "publish", After: []string{"build"}},
		StageSpec{Name: "lint", Parallel: []string{"publish"}}))
	require.Len(t, refusals, 1)
	assert.Contains(t, refusals[0].Message, "runs lint before publish")
	assert.Contains(t, refusals[0].Message, "place lint after the publish")

	observing := resolve(t, "version", "build", StageSpec{Name: "publish", After: []string{"build"}},
		StageSpec{Name: "lint", After: []string{"publish"}, Parallel: []string{"announce"}}, "announce")
	assert.Equal(t, []string{"version", "build", "publish"}, gatingOf(observing), "a stage after the publish only reports")
	assert.True(t, observing.IsBefore(RecordStage, "lint"))
	assert.False(t, observing.IsBefore("lint", "announce") || observing.IsBefore("announce", "lint"))
}

func TestStageGraphRejectsRemovedPluralTestName(t *testing.T) {
	for _, specs := range [][]StageSpec{
		{{Name: "version"}, {Name: "build"}, {Name: "tests"}},
		{{Name: "version"}, {Name: "build"}, {Name: "lint", After: []string{"tests"}}},
	} {
		graph, refusals := ResolveStageGraph(specs)
		assert.True(t, graph.IsEmpty())
		require.Len(t, refusals, 1)
		assert.Equal(t, CodeStageUnknown, refusals[0].Code)
		assert.Contains(t, refusals[0].Message, "test")
	}
}

// TestStageGraphRanks: the stage order the tie-break reads.
func TestStageGraphRanks(t *testing.T) {
	assert.Less(t, StageRank("sign"), StageRank("version"))
	assert.Less(t, StageRank("version"), StageRank("syncLock"))
	assert.Less(t, StageRank("syncLock"), StageRank("build"))
	assert.Equal(t, StageRank("test"), StageRank("anything"))
	assert.Equal(t, StageRank("publish"), StageRank(RecordStage))
	assert.Less(t, StageRank("publish"), StageRank("announce"))
	assert.Equal(t, StageRank("version"), StageRank("propagate"))
	for _, name := range []string{"sign", "version", "propagate", "syncLock"} {
		assert.True(t, IsPreparingStage(name), name)
	}
	assert.False(t, IsPreparingStage("build"))
}
