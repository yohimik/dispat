package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

const (
	campaignTemporaryEnvironmentName         = "RUNNER_TEMP"
	campaignSelectionReceiptFileName         = "dispat-build-campaign-selection.json"
	campaignSelectionReceiptPolicy           = "Newest successful run ID per workload at the exact checkout with one unexpired full report; invalid matching evidence fails without choosing an older report."
	campaignSelectionReceiptIndent           = "  "
	campaignArtifactDigestPattern            = `^sha256:[a-f0-9]{64}$`
	campaignSelectionReceiptErrorFormat      = "campaign selection receipt %q cannot be retained: %w"
	campaignSelectCommandName                = "select"
	campaignSelectArgumentCount              = 0
	campaignSelectArgumentOffset             = 1
	campaignRepositoryEnvironmentName        = "GITHUB_REPOSITORY"
	campaignRevisionEnvironmentName          = "GITHUB_SHA"
	campaignOutputEnvironmentName            = "GITHUB_OUTPUT"
	campaignRepositoryPattern                = `^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`
	campaignRevisionPattern                  = `^[a-f0-9]{40}$`
	campaignWorkflowPath                     = ".github/workflows/build-experiments.yml"
	campaignWorkflowEvent                    = "workflow_dispatch"
	campaignWorkflowStatus                   = "completed"
	campaignWorkflowConclusion               = "success"
	campaignLLVMArtifactPrefix               = "llvm-gce"
	campaignKernelArtifactPrefix             = "kernel-gce"
	campaignArtifactNameFormat               = "%s-report-full-%d-%d"
	campaignOutputFormat                     = "llvm-run=%d\nllvm-attempt=%d\nllvm-artifact=%d\nkernel-run=%d\nkernel-attempt=%d\nkernel-artifact=%d\n"
	campaignSelectionSummaryFormat           = "LLVM campaign run=%d attempt=%d artifact=%d\nLinux kernel campaign run=%d attempt=%d artifact=%d\n"
	isRetainedCampaignArtifactExpired        = false
	campaignEmptyCount                       = 0
	campaignSelectCommandIndex               = 0
	campaignEmptyIdentity                    = 0
	campaignSingleArtifactCount              = 1
	campaignSelectionEmptyText               = ""
	campaignSelectionFileMode                = 0
	campaignSelectionArgumentsErrorMessage   = "campaign selection accepts no arguments; it uses the workflow repository and revision"
	campaignSelectionEnvironmentErrorMessage = "campaign selection requires GITHUB_REPOSITORY, a full lowercase GITHUB_SHA, GITHUB_OUTPUT, and absolute RUNNER_TEMP"
	campaignSelectionRunErrorFormat          = "campaign run %d attempt %d does not match successful manual workflow %s at %s in %s"
	campaignSelectionArtifactErrorFormat     = "campaign run %d artifact %q is expired, ambiguous, or bound to a different source or repository"
	campaignSelectionPairErrorFormat         = "no successful full LLVM and Linux kernel campaign pair exists for %s at %s"
	campaignSelectionSameRunErrorMessage     = "LLVM and Linux kernel reports unexpectedly share a campaign run or artifact identity"
	campaignSelectionAttemptErrorFormat      = "campaign run %d changed identity or attempt during selection"
	campaignSelectionOutputErrorFormat       = "campaign selection output %q cannot be written: %w"
)

type campaignRepository struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
}

type campaignWorkflowRun struct {
	ID             int64              `json:"id"`
	Attempt        int64              `json:"run_attempt"`
	Revision       string             `json:"head_sha"`
	Path           string             `json:"path"`
	Event          string             `json:"event"`
	Status         string             `json:"status"`
	Conclusion     string             `json:"conclusion"`
	Repository     campaignRepository `json:"repository"`
	HeadRepository campaignRepository `json:"head_repository"`
}

type campaignWorkflowArtifact struct {
	Digest      *string `json:"digest"`
	SizeBytes   *int64  `json:"size_in_bytes"`
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	IsExpired   *bool   `json:"expired"`
	WorkflowRun struct {
		ID               int64  `json:"id"`
		RepositoryID     int64  `json:"repository_id"`
		HeadRepositoryID int64  `json:"head_repository_id"`
		Revision         string `json:"head_sha"`
	} `json:"workflow_run"`
}

type campaignSelectedArtifact struct {
	Run      campaignWorkflowRun      `json:"run"`
	Artifact campaignWorkflowArtifact `json:"artifact"`
}

type campaignSelection struct {
	LLVM   campaignSelectedArtifact
	Kernel campaignSelectedArtifact
}

type campaignSelectionReceipt struct {
	Repository        string                   `json:"repository"`
	Revision          string                   `json:"sourceRevision"`
	Workflow          string                   `json:"workflowPath"`
	Policy            string                   `json:"selectionPolicy"`
	CapturedAt        string                   `json:"capturedAt"`
	PageSizeCount     int                      `json:"pageSizeCount"`
	MaximumPagesCount int                      `json:"maximumPagesPerListingCount"`
	LLVM              campaignSelectedArtifact `json:"llvm"`
	Kernel            campaignSelectedArtifact `json:"kernel"`
}

type campaignSelectionOutputPaths struct {
	Output    string
	Temporary string
}

type campaignSelectionSource struct {
	Repository string
	Revision   string
}

type campaignMetadataRequest func(context.Context, string) ([]byte, error)

type campaignSelector struct {
	Source  campaignSelectionSource
	Request campaignMetadataRequest
}

func selectCampaigns(ctx context.Context, args []string) error {
	if len(args) != campaignSelectArgumentCount {
		return errors.New(campaignSelectionArgumentsErrorMessage)
	}
	source := campaignSelectionSource{Repository: os.Getenv(campaignRepositoryEnvironmentName), Revision: os.Getenv(campaignRevisionEnvironmentName)}
	paths := campaignSelectionOutputPaths{Output: os.Getenv(campaignOutputEnvironmentName), Temporary: os.Getenv(campaignTemporaryEnvironmentName)}
	if !regexp.MustCompile(campaignRepositoryPattern).MatchString(source.Repository) ||
		!regexp.MustCompile(campaignRevisionPattern).MatchString(source.Revision) || paths.Output == campaignSelectionEmptyText || !filepath.IsAbs(paths.Temporary) {
		return errors.New(campaignSelectionEnvironmentErrorMessage)
	}
	selectionContext, cancel := context.WithTimeout(ctx, campaignSelectionTimeoutDuration)
	defer cancel()
	selector := campaignSelector{Source: source, Request: requestCampaignMetadata}
	selected, err := selector.selectPair(selectionContext)
	if err != nil {
		return err
	}
	if err := selected.retainOutputs(paths); err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stderr, campaignSelectionSummaryFormat, selected.LLVM.Run.ID, selected.LLVM.Run.Attempt, selected.LLVM.Artifact.ID, selected.Kernel.Run.ID, selected.Kernel.Run.Attempt, selected.Kernel.Artifact.ID)
	return err
}

func (selection campaignSelection) retainOutputs(paths campaignSelectionOutputPaths) error {
	if err := selection.writeReceipt(paths.Temporary); err != nil {
		return err
	}
	output, err := os.OpenFile(paths.Output, os.O_WRONLY|os.O_APPEND, campaignSelectionFileMode)
	if err != nil {
		return fmt.Errorf(campaignSelectionOutputErrorFormat, paths.Output, err)
	}
	writeError := selection.writeOutputs(output)
	if err := errors.Join(writeError, output.Close()); err != nil {
		return fmt.Errorf(campaignSelectionOutputErrorFormat, paths.Output, err)
	}
	return nil
}

func (selection campaignSelection) writeReceipt(directory string) error {
	path := filepath.Join(directory, campaignSelectionReceiptFileName)
	receipt := campaignSelectionReceipt{Repository: selection.LLVM.Run.Repository.FullName, Revision: selection.LLVM.Run.Revision,
		Workflow: campaignWorkflowPath, Policy: campaignSelectionReceiptPolicy, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano),
		PageSizeCount: campaignMetadataPageSizeCount, MaximumPagesCount: campaignMetadataMaximumPagesCount, LLVM: selection.LLVM, Kernel: selection.Kernel}
	content, err := json.MarshalIndent(receipt, campaignSelectionEmptyText, campaignSelectionReceiptIndent)
	if err != nil {
		return fmt.Errorf(campaignSelectionReceiptErrorFormat, path, err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		return fmt.Errorf(campaignSelectionReceiptErrorFormat, path, err)
	}
	_, writeError := file.Write(content)
	if err := errors.Join(writeError, file.Close()); err != nil {
		return fmt.Errorf(campaignSelectionReceiptErrorFormat, path, err)
	}
	return nil
}

func (selection campaignSelection) writeOutputs(output io.Writer) error {
	_, err := fmt.Fprintf(output, campaignOutputFormat, selection.LLVM.Run.ID, selection.LLVM.Run.Attempt, selection.LLVM.Artifact.ID, selection.Kernel.Run.ID, selection.Kernel.Run.Attempt, selection.Kernel.Artifact.ID)
	return err
}

func (selector campaignSelector) selectPair(ctx context.Context) (campaignSelection, error) {
	selected := campaignSelection{}
	runs, err := selector.readRuns(ctx)
	if err != nil {
		return selected, err
	}
	slices.SortFunc(runs, func(left, right campaignWorkflowRun) int { return compareCampaignRunIDs(right.ID, left.ID) })
	for _, run := range runs {
		artifacts, err := selector.readArtifacts(ctx, run)
		if err != nil {
			return campaignSelection{}, err
		}
		if selected.LLVM.Run.ID == campaignEmptyIdentity {
			selected.LLVM, err = selectCampaignArtifact(run, artifacts, campaignLLVMArtifactPrefix)
			if err != nil {
				return campaignSelection{}, err
			}
		}
		if selected.Kernel.Run.ID == campaignEmptyIdentity {
			selected.Kernel, err = selectCampaignArtifact(run, artifacts, campaignKernelArtifactPrefix)
			if err != nil {
				return campaignSelection{}, err
			}
		}
		if selected.LLVM.Run.ID != campaignEmptyIdentity && selected.Kernel.Run.ID != campaignEmptyIdentity {
			break
		}
	}
	if selected.LLVM.Run.ID == campaignEmptyIdentity || selected.Kernel.Run.ID == campaignEmptyIdentity {
		return campaignSelection{}, fmt.Errorf(campaignSelectionPairErrorFormat, selector.Source.Repository, selector.Source.Revision)
	}
	if selected.LLVM.Run.ID == selected.Kernel.Run.ID || selected.LLVM.Artifact.ID == selected.Kernel.Artifact.ID {
		return campaignSelection{}, errors.New(campaignSelectionSameRunErrorMessage)
	}
	for _, run := range []campaignWorkflowRun{selected.LLVM.Run, selected.Kernel.Run} {
		if err := selector.verifyAttempt(ctx, run); err != nil {
			return campaignSelection{}, err
		}
	}
	return selected, nil
}

func compareCampaignRunIDs(left, right int64) int {
	if left < right {
		return campaignRunOrderBefore
	}
	if left > right {
		return campaignRunOrderAfter
	}
	return campaignRunOrderEqual
}

func validateCampaignRun(source campaignSelectionSource, run campaignWorkflowRun) error {
	if run.ID <= campaignEmptyIdentity || run.Attempt <= campaignEmptyIdentity || run.Revision != source.Revision ||
		run.Path != campaignWorkflowPath || run.Event != campaignWorkflowEvent || run.Status != campaignWorkflowStatus ||
		run.Conclusion != campaignWorkflowConclusion || run.Repository.FullName != source.Repository ||
		run.HeadRepository.FullName != source.Repository || run.Repository.ID <= campaignEmptyIdentity || run.HeadRepository.ID != run.Repository.ID {
		return fmt.Errorf(campaignSelectionRunErrorFormat, run.ID, run.Attempt, campaignWorkflowPath, source.Revision, source.Repository)
	}
	return nil
}

func selectCampaignArtifact(run campaignWorkflowRun, artifacts []campaignWorkflowArtifact, prefix string) (campaignSelectedArtifact, error) {
	name := fmt.Sprintf(campaignArtifactNameFormat, prefix, run.ID, run.Attempt)
	matched := campaignEmptyCount
	selected := campaignSelectedArtifact{}
	for _, artifact := range artifacts {
		if artifact.Name != name {
			continue
		}
		matched++
		if matched > campaignSingleArtifactCount || artifact.ID <= campaignEmptyIdentity || artifact.IsExpired == nil || *artifact.IsExpired != isRetainedCampaignArtifactExpired ||
			artifact.WorkflowRun.ID != run.ID || artifact.WorkflowRun.RepositoryID != run.Repository.ID ||
			artifact.WorkflowRun.HeadRepositoryID != run.HeadRepository.ID || artifact.WorkflowRun.Revision != run.Revision ||
			artifact.SizeBytes != nil && *artifact.SizeBytes <= campaignEmptyIdentity ||
			artifact.Digest != nil && !regexp.MustCompile(campaignArtifactDigestPattern).MatchString(*artifact.Digest) {
			return campaignSelectedArtifact{}, fmt.Errorf(campaignSelectionArtifactErrorFormat, run.ID, name)
		}
		selected = campaignSelectedArtifact{Run: run, Artifact: artifact}
	}
	return selected, nil
}

func (selector campaignSelector) verifyAttempt(ctx context.Context, expected campaignWorkflowRun) error {
	endpoint := fmt.Sprintf(campaignAttemptEndpointFormat, selector.Source.Repository, expected.ID, expected.Attempt)
	content, err := selector.Request(ctx, endpoint)
	if err != nil {
		return err
	}
	var actual campaignWorkflowRun
	if err := json.Unmarshal(content, &actual); err != nil {
		return fmt.Errorf(campaignMetadataJSONErrorFormat, endpoint, err)
	}
	if err := validateCampaignRun(selector.Source, actual); err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf(campaignSelectionAttemptErrorFormat, expected.ID)
	}
	return nil
}
