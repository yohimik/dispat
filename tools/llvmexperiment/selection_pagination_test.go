package main

import (
	"fmt"
	"reflect"
	"testing"
)

const (
	selectionPaginationShrinkCaseName       = "shrinking-total"
	selectionPaginationGrowCaseName         = "growing-total"
	selectionPaginationSamePageCaseName     = "same-page"
	selectionPaginationAcrossPagesCaseName  = "across-pages"
	selectionPaginationFinalPageCount       = 1
	selectionPaginationGrowthCount          = 2
	selectionPaginationPairCount            = 2
	selectionPaginationFirstIdentity        = 1
	selectionPaginationArtifactNameFormat   = "retained-artifact-%d"
	selectionPaginationRunDriftErrorFormat  = "changed run total %d admitted %d runs or returned a partial inventory: %v"
	selectionPaginationFileDriftErrorFormat = "changed artifact total %d admitted %d artifacts or returned a partial inventory: %v"
	selectionPaginationRunLossErrorFormat   = "stable paginated run inventory changed or was rejected: count %d, error %v"
	selectionPaginationFileLossErrorFormat  = "stable paginated artifact inventory changed or was rejected: count %d, error %v"
	selectionPaginationDuplicateErrorFormat = "artifact ID %d repeated under different names admitted %d artifacts or returned a partial inventory: %v"
	selectionPaginationPairDriftErrorFormat = "changed run total %d produced a campaign selection or no error: selection %+v, error %v"
)

type campaignPaginationDriftCase struct {
	Name       string
	FinalTotal int
}

type campaignPaginationRunPage struct {
	Number int
	Total  int
	Runs   []campaignWorkflowRun
}

type campaignPaginationArtifactPage struct {
	Number    int
	Total     int
	Artifacts []campaignWorkflowArtifact
}

func listCampaignPaginationDriftCases() []campaignPaginationDriftCase {
	return []campaignPaginationDriftCase{
		{Name: selectionPaginationShrinkCaseName, FinalTotal: campaignMetadataPageSizeCount},
		{Name: selectionPaginationGrowCaseName, FinalTotal: campaignMetadataPageSizeCount + selectionPaginationGrowthCount},
	}
}

func (fixture *campaignSelectionFixture) makePaginationRuns(count int) []campaignWorkflowRun {
	runs := make([]campaignWorkflowRun, count)
	for i := range runs {
		runs[i] = fixture.makeRun(selectionPaginationFirstIdentity + int64(i))
	}
	return runs
}

func (fixture *campaignSelectionFixture) makePaginationArtifacts(count int) []campaignWorkflowArtifact {
	artifacts := make([]campaignWorkflowArtifact, count)
	for i := range artifacts {
		artifacts[i] = fixture.makeArtifact(fixture.LLVM, campaignLLVMArtifactPrefix)
		artifacts[i].ID += int64(i)
		artifacts[i].Name = fmt.Sprintf(selectionPaginationArtifactNameFormat, i)
	}
	return artifacts
}

func (fixture *campaignSelectionFixture) setPaginationRunPage(t *testing.T, page campaignPaginationRunPage) {
	t.Helper()
	endpoint := fmt.Sprintf(campaignRunsEndpointFormat, fixture.Selector.Source.Repository, fixture.Selector.Source.Revision,
		campaignMetadataPageSizeCount, page.Number)
	fixture.setReply(t, endpoint, struct {
		Total int                   `json:"total_count"`
		Runs  []campaignWorkflowRun `json:"workflow_runs"`
	}{Total: page.Total, Runs: page.Runs})
}

func (fixture *campaignSelectionFixture) setPaginationArtifactPage(t *testing.T, page campaignPaginationArtifactPage) {
	t.Helper()
	endpoint := fmt.Sprintf(campaignArtifactsEndpointFormat, fixture.Selector.Source.Repository, fixture.LLVM.ID,
		campaignMetadataPageSizeCount, page.Number)
	fixture.setReply(t, endpoint, struct {
		Total     int                        `json:"total_count"`
		Artifacts []campaignWorkflowArtifact `json:"artifacts"`
	}{Total: page.Total, Artifacts: page.Artifacts})
}

func TestCampaignSelectionPaginationRejectsRunTotalDrift(t *testing.T) {
	for _, scenario := range listCampaignPaginationDriftCases() {
		t.Run(scenario.Name, func(t *testing.T) {
			fixture := newCampaignSelectionFixture(t)
			runs := fixture.makePaginationRuns(scenario.FinalTotal)
			fixture.setPaginationRunPage(t, campaignPaginationRunPage{
				Number: campaignMetadataFirstPageNumber, Total: campaignMetadataPageSizeCount + selectionPaginationFinalPageCount,
				Runs: runs[:campaignMetadataPageSizeCount],
			})
			fixture.setPaginationRunPage(t, campaignPaginationRunPage{
				Number: campaignMetadataFirstPageNumber + selectionFixturePageIncrement, Total: scenario.FinalTotal,
				Runs: runs[campaignMetadataPageSizeCount:],
			})
			actual, err := fixture.Selector.readRuns(t.Context())
			if err == nil || actual != nil {
				t.Fatalf(selectionPaginationRunDriftErrorFormat, scenario.FinalTotal, len(actual), err)
			}
		})
	}
}

func TestCampaignSelectionPaginationRejectsArtifactTotalDrift(t *testing.T) {
	for _, scenario := range listCampaignPaginationDriftCases() {
		t.Run(scenario.Name, func(t *testing.T) {
			fixture := newCampaignSelectionFixture(t)
			artifacts := fixture.makePaginationArtifacts(scenario.FinalTotal)
			fixture.setPaginationArtifactPage(t, campaignPaginationArtifactPage{
				Number: campaignMetadataFirstPageNumber, Total: campaignMetadataPageSizeCount + selectionPaginationFinalPageCount,
				Artifacts: artifacts[:campaignMetadataPageSizeCount],
			})
			fixture.setPaginationArtifactPage(t, campaignPaginationArtifactPage{
				Number: campaignMetadataFirstPageNumber + selectionFixturePageIncrement, Total: scenario.FinalTotal,
				Artifacts: artifacts[campaignMetadataPageSizeCount:],
			})
			actual, err := fixture.Selector.readArtifacts(t.Context(), fixture.LLVM)
			if err == nil || actual != nil {
				t.Fatalf(selectionPaginationFileDriftErrorFormat, scenario.FinalTotal, len(actual), err)
			}
		})
	}
}

func TestCampaignSelectionPaginationPreservesStableInventories(t *testing.T) {
	fixture := newCampaignSelectionFixture(t)
	total := campaignMetadataPageSizeCount + selectionPaginationFinalPageCount
	runs := fixture.makePaginationRuns(total)
	fixture.setPaginationRunPage(t, campaignPaginationRunPage{
		Number: campaignMetadataFirstPageNumber, Total: total, Runs: runs[:campaignMetadataPageSizeCount],
	})
	fixture.setPaginationRunPage(t, campaignPaginationRunPage{
		Number: campaignMetadataFirstPageNumber + selectionFixturePageIncrement, Total: total, Runs: runs[campaignMetadataPageSizeCount:],
	})
	actualRuns, err := fixture.Selector.readRuns(t.Context())
	if err != nil || !reflect.DeepEqual(actualRuns, runs) {
		t.Fatalf(selectionPaginationRunLossErrorFormat, len(actualRuns), err)
	}
	artifacts := fixture.makePaginationArtifacts(total)
	fixture.setPaginationArtifactPage(t, campaignPaginationArtifactPage{
		Number: campaignMetadataFirstPageNumber, Total: total, Artifacts: artifacts[:campaignMetadataPageSizeCount],
	})
	fixture.setPaginationArtifactPage(t, campaignPaginationArtifactPage{
		Number: campaignMetadataFirstPageNumber + selectionFixturePageIncrement, Total: total, Artifacts: artifacts[campaignMetadataPageSizeCount:],
	})
	actualArtifacts, err := fixture.Selector.readArtifacts(t.Context(), fixture.LLVM)
	if err != nil || !reflect.DeepEqual(actualArtifacts, artifacts) {
		t.Fatalf(selectionPaginationFileLossErrorFormat, len(actualArtifacts), err)
	}
}

func TestCampaignSelectionPaginationRejectsDuplicateArtifactIDs(t *testing.T) {
	for _, scenario := range []struct {
		Name       string
		FirstCount int
	}{
		{Name: selectionPaginationSamePageCaseName, FirstCount: selectionPaginationFinalPageCount},
		{Name: selectionPaginationAcrossPagesCaseName, FirstCount: campaignMetadataPageSizeCount},
	} {
		t.Run(scenario.Name, func(t *testing.T) {
			fixture := newCampaignSelectionFixture(t)
			artifacts := fixture.makePaginationArtifacts(scenario.FirstCount + selectionPaginationFinalPageCount)
			artifacts[scenario.FirstCount].ID = artifacts[campaignEmptyCount].ID
			firstCount := min(campaignMetadataPageSizeCount, len(artifacts))
			fixture.setPaginationArtifactPage(t, campaignPaginationArtifactPage{
				Number: campaignMetadataFirstPageNumber, Total: len(artifacts), Artifacts: artifacts[:firstCount],
			})
			fixture.setPaginationArtifactPage(t, campaignPaginationArtifactPage{
				Number: campaignMetadataFirstPageNumber + selectionFixturePageIncrement, Total: len(artifacts), Artifacts: artifacts[firstCount:],
			})
			actual, err := fixture.Selector.readArtifacts(t.Context(), fixture.LLVM)
			if err == nil || actual != nil {
				t.Fatalf(selectionPaginationDuplicateErrorFormat, artifacts[campaignEmptyCount].ID, len(actual), err)
			}
		})
	}
}

func TestCampaignSelectionPaginationReturnsNoPairAfterInventoryDrift(t *testing.T) {
	for _, scenario := range listCampaignPaginationDriftCases() {
		t.Run(scenario.Name, func(t *testing.T) {
			fixture := newCampaignSelectionFixture(t)
			runs := append([]campaignWorkflowRun{fixture.LLVM, fixture.Kernel}, fixture.makePaginationRuns(scenario.FinalTotal-selectionPaginationPairCount)...)
			fixture.setPaginationRunPage(t, campaignPaginationRunPage{
				Number: campaignMetadataFirstPageNumber, Total: campaignMetadataPageSizeCount + selectionPaginationFinalPageCount,
				Runs: runs[:campaignMetadataPageSizeCount],
			})
			fixture.setPaginationRunPage(t, campaignPaginationRunPage{
				Number: campaignMetadataFirstPageNumber + selectionFixturePageIncrement, Total: scenario.FinalTotal,
				Runs: runs[campaignMetadataPageSizeCount:],
			})
			selected, err := fixture.Selector.selectPair(t.Context())
			if err == nil || !reflect.DeepEqual(selected, campaignSelection{}) {
				t.Fatalf(selectionPaginationPairDriftErrorFormat, scenario.FinalTotal, selected, err)
			}
		})
	}
}
