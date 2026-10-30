package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

const (
	isMalformedCampaignPageValid            = false
	campaignSelectionTimeoutDuration        = 5 * time.Minute
	campaignMetadataRequestTimeoutDuration  = 30 * time.Second
	campaignMetadataMaximumBytes            = 2 * 1024 * 1024
	campaignMetadataPageSizeCount           = 100
	campaignMetadataMaximumPagesCount       = 10
	campaignMetadataFirstPageNumber         = 1
	campaignRunOrderBefore                  = -1
	campaignRunOrderEqual                   = 0
	campaignRunOrderAfter                   = 1
	campaignMetadataCommandName             = "gh"
	campaignMetadataSubcommandName          = "api"
	campaignRunsEndpointFormat              = "repos/%s/actions/workflows/build-experiments.yml/runs?head_sha=%s&event=workflow_dispatch&status=success&per_page=%d&page=%d"
	campaignArtifactsEndpointFormat         = "repos/%s/actions/runs/%d/artifacts?per_page=%d&page=%d"
	campaignAttemptEndpointFormat           = "repos/%s/actions/runs/%d/attempts/%d"
	campaignMetadataCommandErrorFormat      = "GitHub campaign metadata request %q failed: %w"
	campaignMetadataJSONErrorFormat         = "GitHub campaign metadata response %q is invalid JSON: %w"
	campaignMetadataPageErrorFormat         = "GitHub campaign metadata page %q has a missing, inconsistent, or excessive inventory"
	campaignMetadataDuplicateRunErrorFormat = "GitHub campaign metadata repeats run %d across pages"
	campaignMetadataTooLargeErrorMessage    = "GitHub campaign metadata exceeded the response byte limit"
)

type campaignMetadataBuffer struct {
	Content bytes.Buffer
}

func (output *campaignMetadataBuffer) Write(content []byte) (int, error) {
	if len(content) > campaignMetadataMaximumBytes-output.Content.Len() {
		return campaignEmptyCount, errors.New(campaignMetadataTooLargeErrorMessage)
	}
	return output.Content.Write(content)
}

func requestCampaignMetadata(ctx context.Context, endpoint string) ([]byte, error) {
	requestContext, cancel := context.WithTimeout(ctx, campaignMetadataRequestTimeoutDuration)
	defer cancel()
	output := campaignMetadataBuffer{}
	command := exec.CommandContext(requestContext, campaignMetadataCommandName, campaignMetadataSubcommandName, endpoint)
	command.Stdout, command.Stderr = &output, io.Discard
	command.WaitDelay = commandWaitDelay
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf(campaignMetadataCommandErrorFormat, endpoint, errors.Join(requestContext.Err(), err))
	}
	return output.Content.Bytes(), nil
}

func (selector campaignSelector) readRuns(ctx context.Context) ([]campaignWorkflowRun, error) {
	var runs []campaignWorkflowRun
	identities := make(map[int64]struct{})
	for page := campaignMetadataFirstPageNumber; page <= campaignMetadataMaximumPagesCount; page++ {
		endpoint := fmt.Sprintf(campaignRunsEndpointFormat, selector.Source.Repository, selector.Source.Revision, campaignMetadataPageSizeCount, page)
		content, err := selector.Request(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		var response struct {
			Total *int                  `json:"total_count"`
			Runs  []campaignWorkflowRun `json:"workflow_runs"`
		}
		if err := json.Unmarshal(content, &response); err != nil {
			return nil, fmt.Errorf(campaignMetadataJSONErrorFormat, endpoint, err)
		}
		if !isCampaignPageInventoryValid(campaignPageInventory{Total: response.Total, PageCount: len(response.Runs), PreviousCount: len(runs)}) || response.Runs == nil {
			return nil, fmt.Errorf(campaignMetadataPageErrorFormat, endpoint)
		}
		for _, run := range response.Runs {
			if err := validateCampaignRun(selector.Source, run); err != nil {
				return nil, err
			}
			if _, exists := identities[run.ID]; exists {
				return nil, fmt.Errorf(campaignMetadataDuplicateRunErrorFormat, run.ID)
			}
			identities[run.ID] = struct{}{}
		}
		runs = append(runs, response.Runs...)
		if len(runs) == *response.Total {
			return runs, nil
		}
	}
	return nil, fmt.Errorf(campaignMetadataPageErrorFormat, campaignWorkflowPath)
}

func (selector campaignSelector) readArtifacts(ctx context.Context, run campaignWorkflowRun) ([]campaignWorkflowArtifact, error) {
	var artifacts []campaignWorkflowArtifact
	for page := campaignMetadataFirstPageNumber; page <= campaignMetadataMaximumPagesCount; page++ {
		endpoint := fmt.Sprintf(campaignArtifactsEndpointFormat, selector.Source.Repository, run.ID, campaignMetadataPageSizeCount, page)
		content, err := selector.Request(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		var response struct {
			Total     *int                       `json:"total_count"`
			Artifacts []campaignWorkflowArtifact `json:"artifacts"`
		}
		if err := json.Unmarshal(content, &response); err != nil {
			return nil, fmt.Errorf(campaignMetadataJSONErrorFormat, endpoint, err)
		}
		if !isCampaignPageInventoryValid(campaignPageInventory{Total: response.Total, PageCount: len(response.Artifacts), PreviousCount: len(artifacts)}) || response.Artifacts == nil {
			return nil, fmt.Errorf(campaignMetadataPageErrorFormat, endpoint)
		}
		artifacts = append(artifacts, response.Artifacts...)
		if len(artifacts) == *response.Total {
			return artifacts, nil
		}
	}
	return nil, fmt.Errorf(campaignMetadataPageErrorFormat, campaignWorkflowPath)
}

type campaignPageInventory struct {
	Total         *int
	PageCount     int
	PreviousCount int
}

func isCampaignPageInventoryValid(inventory campaignPageInventory) bool {
	if inventory.Total == nil || *inventory.Total < campaignEmptyCount ||
		*inventory.Total > campaignMetadataPageSizeCount*campaignMetadataMaximumPagesCount || inventory.PageCount > campaignMetadataPageSizeCount {
		return isMalformedCampaignPageValid
	}
	count := inventory.PreviousCount + inventory.PageCount
	return count == *inventory.Total || count < *inventory.Total && inventory.PageCount == campaignMetadataPageSizeCount
}
