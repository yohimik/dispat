package main

import (
	"fmt"
	"math"
	"regexp"
)

const (
	buildCompletedStatus   = "completed"
	buildFailedStatus      = "failed"
	buildBlockedStatus     = "blocked"
	buildUnsupportedStatus = "unsupported"
	buildExecutorCount     = 2
	buildNotMeasuredText   = "not measured"
	buildFullProfile       = "full"
	buildSmokeProfile      = "smoke"
)

var buildRevisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var buildDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// DistributedBuild retains the common provenance and outcome of a two-executor build.
type DistributedBuild struct {
	Status          string            `json:"status"`
	Reason          string            `json:"reason,omitempty"`
	Mode            string            `json:"mode"`
	Profile         string            `json:"profile"`
	SourceRevision  string            `json:"sourceRevision"`
	Topology        string            `json:"topology"`
	ExecutorCount   int               `json:"executorCount"`
	JobsPerExecutor int               `json:"jobsPerExecutor"`
	TimeoutSeconds  *float64          `json:"timeoutSeconds,omitempty"`
	BuildTargets    []string          `json:"buildTargets"`
	Backends        []string          `json:"backends"`
	Cache           string            `json:"cache"`
	Seconds         *float64          `json:"seconds,omitempty"`
	Nodes           []string          `json:"nodes"`
	Artifacts       []BuildArtifact   `json:"artifacts"`
	Platform        string            `json:"platform,omitempty"`
	ToolVersions    map[string]string `json:"toolVersions,omitempty"`
	BinaryID        string            `json:"binaryId,omitempty"`
	BinarySHA256    string            `json:"binarySha256,omitempty"`
}

// BuildArtifact identifies a returned file verified by the coordinator.
type BuildArtifact struct {
	Package string `json:"package"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
}

func validateDistributedBuild(result *DistributedBuild, workload string) (map[string]map[string]BuildArtifact, error) {
	if !buildRevisionPattern.MatchString(result.SourceRevision) || result.Topology == "" || result.Cache == "" {
		return nil, fmt.Errorf("%s build source, topology or cache policy is missing or invalid", workload)
	}
	if result.BinarySHA256 != "" && !buildDigestPattern.MatchString(result.BinarySHA256) {
		return nil, fmt.Errorf("%s experiment Dispat binary SHA-256 is invalid", workload)
	}
	if result.TimeoutSeconds != nil && !isPositiveBuildDuration(*result.TimeoutSeconds) {
		return nil, fmt.Errorf("%s experiment timeout must be a finite positive number when recorded", workload)
	}
	if result.ExecutorCount != buildExecutorCount || result.JobsPerExecutor < 1 || len(result.BuildTargets) == 0 || len(result.Backends) != buildExecutorCount {
		return nil, fmt.Errorf("%s build workload or executor budget is invalid", workload)
	}
	if result.Profile != buildFullProfile && result.Profile != buildSmokeProfile {
		return nil, fmt.Errorf("%s profile %q is invalid", workload, result.Profile)
	}
	if result.Mode != "compilation" && result.Mode != "interpretation" && result.Mode != "hybrid" && result.Mode != "distributed" && result.Mode != "manual-gcp-split" {
		return nil, fmt.Errorf("%s distribution mode %q is invalid", workload, result.Mode)
	}
	switch result.Status {
	case buildCompletedStatus:
		if result.Seconds == nil || !isPositiveBuildDuration(*result.Seconds) {
			return nil, fmt.Errorf("completed %s build has no finite positive duration", workload)
		}
		if len(result.Nodes) != buildExecutorCount || result.Nodes[0] == "" || result.Nodes[0] == result.Nodes[1] || result.Nodes[1] == "" {
			return nil, fmt.Errorf("completed %s build lacks two distinct executors", workload)
		}
	case buildFailedStatus, buildBlockedStatus, buildUnsupportedStatus:
		if result.Reason == "" || result.Seconds != nil {
			return nil, fmt.Errorf("unmeasured %s build must explain its status and omit the duration", workload)
		}
	default:
		return nil, fmt.Errorf("%s build status %q is invalid", workload, result.Status)
	}
	packages := map[string]map[string]BuildArtifact{}
	for _, item := range result.Artifacts {
		if item.Package == "" || item.Path == "" || item.Bytes <= 0 || !buildDigestPattern.MatchString(item.SHA256) {
			return nil, fmt.Errorf("%s artifact %q has invalid identity, size or SHA-256", workload, item.Path)
		}
		if packages[item.Package] == nil {
			packages[item.Package] = map[string]BuildArtifact{}
		}
		if _, exists := packages[item.Package][item.Path]; exists {
			return nil, fmt.Errorf("%s artifact %s/%s is duplicated", workload, item.Package, item.Path)
		}
		packages[item.Package][item.Path] = item
	}
	return packages, nil
}

func isPositiveBuildDuration(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func buildOutcome(result *DistributedBuild) string {
	if result.Reason != "" {
		return result.Status + ": " + result.Reason
	}
	return result.Status
}

func buildSeconds(result *DistributedBuild) string {
	if result.Seconds == nil {
		return buildNotMeasuredText
	}
	return seconds(*result.Seconds)
}
