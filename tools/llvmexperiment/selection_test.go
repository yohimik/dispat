package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	selectionSourceFieldName                  = "source"
	selectionWorkflowFieldName                = "workflow"
	selectionEventFieldName                   = "event"
	selectionStatusFieldName                  = "status"
	selectionConclusionFieldName              = "conclusion"
	selectionRunFieldName                     = "run"
	selectionAttemptFieldName                 = "attempt"
	selectionRepositoryFieldName              = "repository"
	selectionRepositoryIDFieldName            = "repository-id"
	selectionHeadRepositoryFieldName          = "head-repository"
	selectionHeadIDFieldName                  = "head-id"
	selectionExpiredFieldName                 = "expired"
	selectionArtifactIDFieldName              = "artifact-id"
	selectionHeadFieldName                    = "head"
	selectionMissingExpiryFieldName           = "missing-expiry"
	selectionDigestFieldName                  = "digest"
	selectionSizeFieldName                    = "size"
	selectionIdentityMismatchErrorFormat      = "selected identities changed: %+v"
	selectionOutputMismatchErrorFormat        = "outputs do not pin exact selected identities: %s"
	selectionMissingPairAcceptedErrorMessage  = "missing kernel campaign produced usable outputs"
	selectionAPIErrorLostErrorFormat          = "API failure was replaced: %v"
	selectionCancellationErrorFormat          = "cancellation was ignored: %v"
	selectionInvalidInventoryErrorMessage     = "invalid metadata inventory was admitted"
	selectionUnboundedInventoryErrorMessage   = "unbounded metadata inventory was admitted"
	selectionOversizedResponseErrorMessage    = "oversized response was retained"
	selectionMissingPairEntryErrorMessage     = "missing pair passed the process entry point"
	selectionFailedOutputChangedErrorFormat   = "failed selection altered previous workflow outputs: %q, %v"
	selectionManualArgumentsErrorMessage      = "manual selection arguments were accepted"
	selectionReceiptMismatchErrorFormat       = "selection receipt lost source, identity or scope: %+v"
	selectionReceiptOverwriteErrorMessage     = "existing selection receipt was overwritten"
	selectionReceiptOutputFailureErrorMessage = "receipt failure still published workflow outputs"
	selectionStreamLimitErrorMessage          = "stream copy bypassed the metadata response bound"
	selectionPagedInventoryErrorFormat        = "complete bounded inventory was lost: %d, %v"
	selectionDuplicateRunAcceptedErrorMessage = "duplicate run in paginated inventory was admitted"
	selectionFixtureArtifactSizeBytes         = 1024
	selectionFixtureArtifactDigest            = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	selectionFixtureInvalidDigest             = "sha256:invalid"
	selectionFixtureRepository                = "owner/dispat"
	selectionFixtureOtherRepository           = "other/dispat"
	selectionFixtureRevision                  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	selectionFixtureOtherRevision             = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	selectionFixtureRepositoryID              = 7
	selectionFixtureOtherRepositoryID         = 8
	selectionFixtureLLVMRunID                 = 120
	selectionFixtureKernelRunID               = 110
	selectionFixtureSmokeRunID                = 130
	selectionFixtureOlderRunID                = 100
	selectionFixtureAttempt                   = 2
	selectionFixtureOtherAttempt              = 3
	selectionFixtureArtifactOffset            = 1000
	selectionFixtureOldWorkflow               = ".github/workflows/release.yml"
	selectionFixtureOtherEvent                = "pull_request"
	selectionFixtureFailedConclusion          = "failure"
	selectionFixturePendingStatus             = "in_progress"
	selectionFixtureSmokeName                 = "llvm-gce-report-smoke-130-2"
	selectionFixtureErrorText                 = "fixture GitHub request rejected"
	selectionFixtureUnknownRequest            = "unexpected metadata request %q"
	selectionFixtureInvalidJSON               = "{invalid"
	selectionFixtureMissingInventory          = "{}"
	selectionFixtureOutputName                = "outputs"
	selectionFixtureOutputBefore              = "previous-step=retained\n"
	selectionFixtureEmptyGHScript             = "#!/bin/sh\nprintf '%s\\n' '{\"total_count\":0,\"workflow_runs\":[]}'\n"
	selectionFixturePathEnvironment           = "PATH"
	selectionFixtureIgnoredArgument           = "--source=ignored"
	selectionFixtureOversizeByte              = "x"
	selectionFixturePageIncrement             = 1
	selectionFixtureIsExpired                 = true
	selectionFixtureMutatedRunError           = "mutated run metadata was admitted"
	selectionFixtureMutatedFileError          = "mutated artifact metadata was admitted"
)

type campaignSelectionFixture struct {
	Selector campaignSelector
	Replies  map[string][]byte
	Requests []string
	LLVM     campaignWorkflowRun
	Kernel   campaignWorkflowRun
}

func newCampaignSelectionFixture(t *testing.T) *campaignSelectionFixture {
	t.Helper()
	fixture := &campaignSelectionFixture{Replies: make(map[string][]byte)}
	fixture.Selector.Source = campaignSelectionSource{Repository: selectionFixtureRepository, Revision: selectionFixtureRevision}
	fixture.Selector.Request = func(ctx context.Context, endpoint string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fixture.Requests = append(fixture.Requests, endpoint)
		content, exists := fixture.Replies[endpoint]
		if !exists {
			return nil, fmt.Errorf(selectionFixtureUnknownRequest, endpoint)
		}
		return content, nil
	}
	fixture.LLVM = fixture.makeRun(selectionFixtureLLVMRunID)
	fixture.Kernel = fixture.makeRun(selectionFixtureKernelRunID)
	fixture.setRuns(t, []campaignWorkflowRun{fixture.Kernel, fixture.LLVM})
	fixture.setArtifacts(t, fixture.LLVM, []campaignWorkflowArtifact{fixture.makeArtifact(fixture.LLVM, campaignLLVMArtifactPrefix)})
	fixture.setArtifacts(t, fixture.Kernel, []campaignWorkflowArtifact{fixture.makeArtifact(fixture.Kernel, campaignKernelArtifactPrefix)})
	fixture.setAttempt(t, fixture.LLVM, fixture.LLVM)
	fixture.setAttempt(t, fixture.Kernel, fixture.Kernel)
	return fixture
}

func (fixture *campaignSelectionFixture) makeRun(identity int64) campaignWorkflowRun {
	repository := campaignRepository{ID: selectionFixtureRepositoryID, FullName: fixture.Selector.Source.Repository}
	return campaignWorkflowRun{ID: identity, Attempt: selectionFixtureAttempt, Revision: fixture.Selector.Source.Revision,
		Path: campaignWorkflowPath, Event: campaignWorkflowEvent, Status: campaignWorkflowStatus,
		Conclusion: campaignWorkflowConclusion, Repository: repository, HeadRepository: repository}
}

func (fixture *campaignSelectionFixture) makeArtifact(run campaignWorkflowRun, prefix string) campaignWorkflowArtifact {
	isExpired := isRetainedCampaignArtifactExpired
	size, digest := int64(selectionFixtureArtifactSizeBytes), selectionFixtureArtifactDigest
	artifact := campaignWorkflowArtifact{IsExpired: &isExpired, SizeBytes: &size, Digest: &digest, ID: run.ID + selectionFixtureArtifactOffset, Name: fmt.Sprintf(campaignArtifactNameFormat, prefix, run.ID, run.Attempt)}
	artifact.WorkflowRun.ID, artifact.WorkflowRun.Revision = run.ID, run.Revision
	artifact.WorkflowRun.RepositoryID, artifact.WorkflowRun.HeadRepositoryID = run.Repository.ID, run.HeadRepository.ID
	return artifact
}

func (fixture *campaignSelectionFixture) setRuns(t *testing.T, runs []campaignWorkflowRun) {
	t.Helper()
	fixture.setReply(t, fmt.Sprintf(campaignRunsEndpointFormat, fixture.Selector.Source.Repository, fixture.Selector.Source.Revision,
		campaignMetadataPageSizeCount, campaignMetadataFirstPageNumber), struct {
		Total int                   `json:"total_count"`
		Runs  []campaignWorkflowRun `json:"workflow_runs"`
	}{Total: len(runs), Runs: runs})
}

func (fixture *campaignSelectionFixture) setArtifacts(t *testing.T, run campaignWorkflowRun, artifacts []campaignWorkflowArtifact) {
	t.Helper()
	fixture.setReply(t, fmt.Sprintf(campaignArtifactsEndpointFormat, fixture.Selector.Source.Repository, run.ID,
		campaignMetadataPageSizeCount, campaignMetadataFirstPageNumber), struct {
		Total     int                        `json:"total_count"`
		Artifacts []campaignWorkflowArtifact `json:"artifacts"`
	}{Total: len(artifacts), Artifacts: artifacts})
}

func (fixture *campaignSelectionFixture) setAttempt(t *testing.T, expected, actual campaignWorkflowRun) {
	t.Helper()
	fixture.setReply(t, fmt.Sprintf(campaignAttemptEndpointFormat, fixture.Selector.Source.Repository, expected.ID, expected.Attempt), actual)
}

func (fixture *campaignSelectionFixture) setReply(t *testing.T, endpoint string, response any) {
	t.Helper()
	content, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	fixture.Replies[endpoint] = content
}

func TestCampaignSelectionPinsNewestFullRunAttemptAndArtifact(t *testing.T) {
	fixture := newCampaignSelectionFixture(t)
	smoke, older := fixture.makeRun(selectionFixtureSmokeRunID), fixture.makeRun(selectionFixtureOlderRunID)
	fixture.setRuns(t, []campaignWorkflowRun{older, fixture.Kernel, fixture.LLVM, smoke})
	fixture.setArtifacts(t, smoke, []campaignWorkflowArtifact{{Name: selectionFixtureSmokeName}})
	selected, err := fixture.Selector.selectPair(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if selected.LLVM.Run != fixture.LLVM || selected.Kernel.Run != fixture.Kernel ||
		selected.LLVM.Artifact.ID != fixture.LLVM.ID+selectionFixtureArtifactOffset || selected.Kernel.Artifact.ID != fixture.Kernel.ID+selectionFixtureArtifactOffset {
		t.Fatalf(selectionIdentityMismatchErrorFormat, selected)
	}
	var output bytes.Buffer
	if err := selected.writeOutputs(&output); err != nil {
		t.Fatal(err)
	}
	expected := fmt.Sprintf(campaignOutputFormat, fixture.LLVM.ID, fixture.LLVM.Attempt, fixture.LLVM.ID+selectionFixtureArtifactOffset,
		fixture.Kernel.ID, fixture.Kernel.Attempt, fixture.Kernel.ID+selectionFixtureArtifactOffset)
	if output.String() != expected {
		t.Fatalf(selectionOutputMismatchErrorFormat, output.String())
	}
}

func TestCampaignSelectionRejectsRunIdentityDrift(t *testing.T) {
	for name, mutate := range map[string]func(*campaignWorkflowRun){
		selectionSourceFieldName:         func(run *campaignWorkflowRun) { run.Revision = selectionFixtureOtherRevision },
		selectionWorkflowFieldName:       func(run *campaignWorkflowRun) { run.Path = selectionFixtureOldWorkflow },
		selectionEventFieldName:          func(run *campaignWorkflowRun) { run.Event = selectionFixtureOtherEvent },
		selectionStatusFieldName:         func(run *campaignWorkflowRun) { run.Status = selectionFixturePendingStatus },
		selectionConclusionFieldName:     func(run *campaignWorkflowRun) { run.Conclusion = selectionFixtureFailedConclusion },
		selectionRunFieldName:            func(run *campaignWorkflowRun) { run.ID = campaignEmptyIdentity },
		selectionAttemptFieldName:        func(run *campaignWorkflowRun) { run.Attempt = campaignEmptyIdentity },
		selectionRepositoryFieldName:     func(run *campaignWorkflowRun) { run.Repository.FullName = selectionFixtureOtherRepository },
		selectionRepositoryIDFieldName:   func(run *campaignWorkflowRun) { run.Repository.ID = campaignEmptyIdentity },
		selectionHeadRepositoryFieldName: func(run *campaignWorkflowRun) { run.HeadRepository.FullName = selectionFixtureOtherRepository },
		selectionHeadIDFieldName:         func(run *campaignWorkflowRun) { run.HeadRepository.ID = selectionFixtureOtherRepositoryID },
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newCampaignSelectionFixture(t)
			mutate(&fixture.LLVM)
			fixture.setRuns(t, []campaignWorkflowRun{fixture.LLVM, fixture.Kernel})
			selected, err := fixture.Selector.selectPair(t.Context())
			if err == nil || selected != (campaignSelection{}) {
				t.Fatal(selectionFixtureMutatedRunError)
			}
		})
	}
}

func TestCampaignSelectionRejectsInvalidExactArtifactWithoutFallback(t *testing.T) {
	for name, mutate := range map[string]func(*campaignWorkflowArtifact){
		selectionMissingExpiryFieldName: func(artifact *campaignWorkflowArtifact) { artifact.IsExpired = nil },
		selectionDigestFieldName: func(artifact *campaignWorkflowArtifact) {
			digest := selectionFixtureInvalidDigest
			artifact.Digest = &digest
		},
		selectionSizeFieldName: func(artifact *campaignWorkflowArtifact) {
			size := int64(campaignEmptyCount)
			artifact.SizeBytes = &size
		},
		selectionExpiredFieldName: func(artifact *campaignWorkflowArtifact) {
			isExpired := selectionFixtureIsExpired
			artifact.IsExpired = &isExpired
		},
		selectionArtifactIDFieldName: func(artifact *campaignWorkflowArtifact) { artifact.ID = campaignEmptyIdentity },
		selectionRunFieldName:        func(artifact *campaignWorkflowArtifact) { artifact.WorkflowRun.ID = selectionFixtureOlderRunID },
		selectionRepositoryFieldName: func(artifact *campaignWorkflowArtifact) {
			artifact.WorkflowRun.RepositoryID = selectionFixtureOtherRepositoryID
		},
		selectionHeadFieldName: func(artifact *campaignWorkflowArtifact) {
			artifact.WorkflowRun.HeadRepositoryID = selectionFixtureOtherRepositoryID
		},
		selectionSourceFieldName: func(artifact *campaignWorkflowArtifact) {
			artifact.WorkflowRun.Revision = selectionFixtureOtherRevision
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newCampaignSelectionFixture(t)
			older := fixture.makeRun(selectionFixtureOlderRunID)
			fixture.setRuns(t, []campaignWorkflowRun{older, fixture.Kernel, fixture.LLVM})
			artifact := fixture.makeArtifact(fixture.LLVM, campaignLLVMArtifactPrefix)
			mutate(&artifact)
			fixture.setArtifacts(t, fixture.LLVM, []campaignWorkflowArtifact{artifact})
			selected, err := fixture.Selector.selectPair(t.Context())
			if err == nil || selected != (campaignSelection{}) {
				t.Fatal(selectionFixtureMutatedFileError)
			}
		})
	}
}

func TestCampaignSelectionRejectsDuplicateArtifactAndChangedAttempt(t *testing.T) {
	fixture := newCampaignSelectionFixture(t)
	artifact := fixture.makeArtifact(fixture.LLVM, campaignLLVMArtifactPrefix)
	fixture.setArtifacts(t, fixture.LLVM, []campaignWorkflowArtifact{artifact, artifact})
	if _, err := fixture.Selector.selectPair(t.Context()); err == nil {
		t.Fatal(selectionFixtureMutatedFileError)
	}
	fixture = newCampaignSelectionFixture(t)
	changed := fixture.LLVM
	changed.Attempt = selectionFixtureOtherAttempt
	fixture.setAttempt(t, fixture.LLVM, changed)
	if _, err := fixture.Selector.selectPair(t.Context()); err == nil {
		t.Fatal(selectionFixtureMutatedRunError)
	}
}

func TestCampaignSelectionFailsOnMissingPairAPIErrorAndCancellation(t *testing.T) {
	fixture := newCampaignSelectionFixture(t)
	fixture.setArtifacts(t, fixture.Kernel, []campaignWorkflowArtifact{})
	selected, err := fixture.Selector.selectPair(t.Context())
	if err == nil || selected != (campaignSelection{}) {
		t.Fatal(selectionMissingPairAcceptedErrorMessage)
	}
	wanted := errors.New(selectionFixtureErrorText)
	fixture.Selector.Request = func(context.Context, string) ([]byte, error) { return nil, wanted }
	if _, err := fixture.Selector.selectPair(t.Context()); !errors.Is(err, wanted) {
		t.Fatalf(selectionAPIErrorLostErrorFormat, err)
	}
	fixture = newCampaignSelectionFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := fixture.Selector.selectPair(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf(selectionCancellationErrorFormat, err)
	}
}

func TestCampaignSelectionRejectsMalformedAndUnboundedMetadata(t *testing.T) {
	for _, content := range []string{selectionFixtureInvalidJSON, selectionFixtureMissingInventory} {
		fixture := newCampaignSelectionFixture(t)
		fixture.Selector.Request = func(context.Context, string) ([]byte, error) { return []byte(content), nil }
		if _, err := fixture.Selector.selectPair(t.Context()); err == nil {
			t.Fatal(selectionInvalidInventoryErrorMessage)
		}
	}
	total := campaignMetadataPageSizeCount*campaignMetadataMaximumPagesCount + selectionFixturePageIncrement
	if isCampaignPageInventoryValid(campaignPageInventory{Total: &total, PageCount: campaignMetadataPageSizeCount}) {
		t.Fatal(selectionUnboundedInventoryErrorMessage)
	}
	buffer := campaignMetadataBuffer{}
	if _, err := buffer.Write([]byte(strings.Repeat(selectionFixtureOversizeByte, campaignMetadataMaximumBytes+selectionFixturePageIncrement))); err == nil || buffer.Content.Len() != campaignEmptyIdentity {
		t.Fatal(selectionOversizedResponseErrorMessage)
	}
}

func TestCampaignSelectionWritesNothingWhenPairIsUnavailable(t *testing.T) {
	directory := t.TempDir()
	outputPath := filepath.Join(directory, selectionFixtureOutputName)
	if err := os.WriteFile(outputPath, []byte(selectionFixtureOutputBefore), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, campaignMetadataCommandName), []byte(selectionFixtureEmptyGHScript), executableFileMode); err != nil {
		t.Fatal(err)
	}
	t.Setenv(selectionFixturePathEnvironment, directory)
	t.Setenv(campaignRepositoryEnvironmentName, selectionFixtureRepository)
	t.Setenv(campaignRevisionEnvironmentName, selectionFixtureRevision)
	t.Setenv(campaignOutputEnvironmentName, outputPath)
	t.Setenv(campaignTemporaryEnvironmentName, directory)
	if err := selectCampaigns(t.Context(), nil); err == nil {
		t.Fatal(selectionMissingPairEntryErrorMessage)
	}
	output, err := os.ReadFile(outputPath)
	if err != nil || string(output) != selectionFixtureOutputBefore {
		t.Fatalf(selectionFailedOutputChangedErrorFormat, output, err)
	}
	if err := selectCampaigns(t.Context(), []string{selectionFixtureIgnoredArgument}); err == nil {
		t.Fatal(selectionManualArgumentsErrorMessage)
	}
}

func TestCampaignSelectionRetainsReceiptBeforeWorkflowOutputs(t *testing.T) {
	fixture := newCampaignSelectionFixture(t)
	selected, err := fixture.Selector.selectPair(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	outputPath := filepath.Join(directory, selectionFixtureOutputName)
	if err := os.WriteFile(outputPath, []byte(selectionFixtureOutputBefore), fileMode); err != nil {
		t.Fatal(err)
	}
	paths := campaignSelectionOutputPaths{Output: outputPath, Temporary: directory}
	if err := selected.retainOutputs(paths); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(directory, campaignSelectionReceiptFileName))
	if err != nil {
		t.Fatal(err)
	}
	var receipt campaignSelectionReceipt
	if err := json.Unmarshal(content, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Repository != selectionFixtureRepository || receipt.Revision != selectionFixtureRevision || receipt.Workflow != campaignWorkflowPath ||
		receipt.LLVM.Run != fixture.LLVM || receipt.Kernel.Run != fixture.Kernel || receipt.CapturedAt == campaignSelectionEmptyText ||
		receipt.LLVM.Artifact.ID != selected.LLVM.Artifact.ID || receipt.Kernel.Artifact.ID != selected.Kernel.Artifact.ID ||
		receipt.LLVM.Artifact.Digest == nil || *receipt.LLVM.Artifact.Digest != selectionFixtureArtifactDigest ||
		receipt.LLVM.Artifact.SizeBytes == nil || *receipt.LLVM.Artifact.SizeBytes != selectionFixtureArtifactSizeBytes ||
		receipt.PageSizeCount != campaignMetadataPageSizeCount || receipt.MaximumPagesCount != campaignMetadataMaximumPagesCount {
		t.Fatalf(selectionReceiptMismatchErrorFormat, receipt)
	}
	before, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := selected.retainOutputs(paths); err == nil {
		t.Fatal(selectionReceiptOverwriteErrorMessage)
	}
	after, err := os.ReadFile(outputPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal(selectionReceiptOutputFailureErrorMessage)
	}
}

func TestCampaignSelectionBoundsStreamedCommandOutput(t *testing.T) {
	content := strings.Repeat(selectionFixtureOversizeByte, campaignMetadataMaximumBytes+selectionFixturePageIncrement)
	reader := io.LimitReader(strings.NewReader(content), int64(len(content)))
	buffer := campaignMetadataBuffer{}
	if _, err := io.Copy(&buffer, reader); err == nil || buffer.Content.Len() > campaignMetadataMaximumBytes {
		t.Fatal(selectionStreamLimitErrorMessage)
	}
}

func TestCampaignSelectionReadsBoundedPagesAndRejectsDuplicateRuns(t *testing.T) {
	fixture := newCampaignSelectionFixture(t)
	total := campaignMetadataPageSizeCount + selectionFixturePageIncrement
	runs := make([]campaignWorkflowRun, total)
	for i := campaignEmptyCount; i < total; i++ {
		runs[i] = fixture.makeRun(selectionFixtureArtifactOffset + int64(i))
	}
	firstEndpoint := fmt.Sprintf(campaignRunsEndpointFormat, selectionFixtureRepository, selectionFixtureRevision, campaignMetadataPageSizeCount, campaignMetadataFirstPageNumber)
	secondEndpoint := fmt.Sprintf(campaignRunsEndpointFormat, selectionFixtureRepository, selectionFixtureRevision, campaignMetadataPageSizeCount, campaignMetadataFirstPageNumber+selectionFixturePageIncrement)
	response := struct {
		Total int                   `json:"total_count"`
		Runs  []campaignWorkflowRun `json:"workflow_runs"`
	}{Total: total, Runs: runs[:campaignMetadataPageSizeCount]}
	fixture.setReply(t, firstEndpoint, response)
	response.Runs = runs[campaignMetadataPageSizeCount:]
	fixture.setReply(t, secondEndpoint, response)
	actual, err := fixture.Selector.readRuns(t.Context())
	if err != nil || len(actual) != total {
		t.Fatalf(selectionPagedInventoryErrorFormat, len(actual), err)
	}
	response.Runs = []campaignWorkflowRun{runs[campaignEmptyCount]}
	fixture.setReply(t, secondEndpoint, response)
	if _, err := fixture.Selector.readRuns(t.Context()); err == nil {
		t.Fatal(selectionDuplicateRunAcceptedErrorMessage)
	}
}
