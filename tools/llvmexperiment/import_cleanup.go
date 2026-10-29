package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	importCloudTrialFileName              = "trial.json"
	importCloudCleanupFileName            = "cleanup.json"
	importCloudProvenanceFileName         = "provenance.json"
	importCleanupLegacySuffix             = "-cleanup-absence.json"
	importCleanupJournalSuffix            = "-cleanup-absence.jsonl"
	importCleanupCloudDirectoryName       = "cloud"
	importCleanupEnvironmentDirectoryName = "environment"
	importCleanupEmptyValue               = ""
	importCleanupModeSeparator            = "-"
	importCleanupNoEntriesCount           = 0
	importCleanupNewline                  = "\n"
	importCleanupIDFilterPrefix           = "id="
	importCleanupNameFilterPrefix         = "name="
	importCleanupZoneFilterSeparator      = " AND zone:"
	importCleanupSHA256Pattern            = `^[a-f0-9]{64}$`
	importCleanupNullJSONValue            = "null"
	importCleanupCensusIdentityError      = "cleanup census contains an invalid instance name or missing identity"
	importCleanupNullInventoryError       = "cleanup journal inventory is null rather than a declared hash map"
	importCleanupObservationErrorFormat   = "cleanup observation %s is invalid: %w"
	importCleanupReferenceErrorFormat     = "cleanup journal reference %s is missing, foreign, or has a different SHA256"
	importCleanupIdentityErrorFormat      = "cleanup observation %s does not match its selected instance or absence lookup"
	importCleanupJournalErrorFormat       = "cleanup journal %s is empty or has an incomplete observation"
	importCleanupFileErrorFormat          = "optional cloud evidence %s is not a regular file"
	importCleanupSourceChangedError       = "optional cloud cleanup evidence changed during import"
	importCleanupTrialIdentityError       = "cloud trial record differs from the selected run identity"
	importCleanupJSONTrailingError        = "observation has trailing JSON or invalid bytes"
)

type importCleanupIdentity struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Binary      string `json:"binarySha256"`
	Tool        string `json:"tool"`
	Mode        string `json:"mode"`
	Workload    string `json:"workload"`
	Profile     string `json:"profile"`
	Zone        string `json:"zone"`
	Project     string `json:"project"`
	Image       string `json:"image"`
	BootImage   string `json:"bootImage"`
	MachineType string `json:"machineType"`
	CPUPlatform string `json:"cpuPlatform"`
}

type importCleanupCensus struct {
	Options   importCleanupIdentity `json:"options"`
	Instances []hardwareInstance    `json:"instances"`
}

type importCleanupObservation struct {
	InstanceName  string `json:"instanceName"`
	InstanceID    string `json:"instanceId"`
	LookupFilter  string `json:"lookupFilter"`
	RawResponse   string `json:"rawResponse"`
	DeletionError string `json:"deletionError,omitempty"`
}

func snapshotImportedCleanupEvidence(root string) (map[string]string, error) {
	hashes := map[string]string{}
	for _, cell := range campaignCells() {
		if cell.Tool == changesetsTool {
			continue
		}
		relative := filepath.Join(importCleanupEnvironmentDirectoryName, cell.Tool+importCleanupModeSeparator+cell.Mode, importCleanupCloudDirectoryName)
		observed, err := readImportedCleanupEvidence(filepath.Join(root, relative))
		if err != nil {
			return nil, err
		}
		for name, digest := range observed {
			hashes[filepath.Join(relative, name)] = digest
		}
	}
	return hashes, nil
}

func readImportedCleanupEvidence(root string) (map[string]string, error) {
	var census importCleanupCensus
	if err := readHardwareJSON(filepath.Join(root, importCloudProvenanceFileName), &census); err != nil {
		return nil, err
	}
	names := []string{importCloudTrialFileName, importCloudCleanupFileName}
	instances := map[string]hardwareInstance{}
	for _, instance := range census.Instances {
		if !filepath.IsLocal(instance.Name) || filepath.Base(instance.Name) != instance.Name || instance.Name == importDockerCurrentDirectoryName || instance.ID == importCleanupEmptyValue {
			return nil, errors.New(importCleanupCensusIdentityError)
		}
		for _, suffix := range []string{importCleanupLegacySuffix, importCleanupJournalSuffix} {
			name := instance.Name + suffix
			names = append(names, name)
			instances[name] = instance
		}
	}
	content := map[string][]byte{}
	hashes := map[string]string{}
	for _, name := range names {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf(importCleanupFileErrorFormat, name)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		content[name], hashes[name] = body, importDigest(body)
	}
	if body, present := content[importCloudTrialFileName]; present {
		var trial struct {
			Trial  importCleanupIdentity `json:"trial"`
			Status string                `json:"status"`
		}
		if err := json.Unmarshal(body, &trial); err != nil {
			return nil, err
		}
		if trial.Trial != census.Options || trial.Status != completedStatus && trial.Status != failedStatus && trial.Status != timeoutStatus {
			return nil, errors.New(importCleanupTrialIdentityError)
		}
	}
	// Cleanup is independent of computation. Preserve false or absent status
	// fields without converting them into a workload success or failure.
	var declared map[string]string
	if body, present := content[importCloudCleanupFileName]; present {
		var cleanup struct {
			Complete          *bool           `json:"complete"`
			ResourcesReleased *bool           `json:"resourcesReleased"`
			Evidence          json.RawMessage `json:"absenceEvidenceSha256"`
		}
		if err := json.Unmarshal(body, &cleanup); err != nil {
			return nil, err
		}
		if bytes.Equal(bytes.TrimSpace(cleanup.Evidence), []byte(importCleanupNullJSONValue)) {
			return nil, errors.New(importCleanupNullInventoryError)
		}
		if len(cleanup.Evidence) != importCleanupNoEntriesCount {
			if err := json.Unmarshal(cleanup.Evidence, &declared); err != nil {
				return nil, err
			}
		}
		for name, expected := range declared {
			if _, owned := instances[name]; !owned || !regexp.MustCompile(importCleanupSHA256Pattern).MatchString(expected) || hashes[name] != expected {
				return nil, fmt.Errorf(importCleanupReferenceErrorFormat, name)
			}
		}
	}
	for name := range content {
		if strings.HasSuffix(name, importCleanupJournalSuffix) && declared[name] == importCleanupEmptyValue {
			return nil, fmt.Errorf(importCleanupReferenceErrorFormat, name)
		}
	}
	for name, instance := range instances {
		if body, present := content[name]; present {
			if err := validateImportedCleanupObservation(name, body, importCleanupBinding{Instance: instance, Zone: census.Options.Zone}); err != nil {
				return nil, err
			}
		}
	}
	return hashes, nil
}

type importCleanupBinding struct {
	Instance hardwareInstance
	Zone     string
}

func validateImportedCleanupObservation(name string, content []byte, binding importCleanupBinding) error {
	observations := [][]byte{content}
	if strings.HasSuffix(name, importCleanupJournalSuffix) {
		if len(content) == importCleanupNoEntriesCount || !bytes.HasSuffix(content, []byte(importCleanupNewline)) {
			return fmt.Errorf(importCleanupJournalErrorFormat, name)
		}
		observations = bytes.Split(bytes.TrimSuffix(content, []byte(importCleanupNewline)), []byte(importCleanupNewline))
	}
	for _, body := range observations {
		var observed importCleanupObservation
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&observed); err != nil {
			return fmt.Errorf(importCleanupObservationErrorFormat, name, err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf(importCleanupObservationErrorFormat, name, errors.New(importCleanupJSONTrailingError))
		}
		identityFilter := importCleanupIDFilterPrefix + binding.Instance.ID
		nameFilter := importCleanupNameFilterPrefix + binding.Instance.Name + importCleanupZoneFilterSeparator + binding.Zone
		var absent []json.RawMessage
		if observed.InstanceName != binding.Instance.Name || observed.InstanceID != binding.Instance.ID ||
			observed.LookupFilter != identityFilter && (binding.Zone == importCleanupEmptyValue || observed.LookupFilter != nameFilter) ||
			json.Unmarshal([]byte(observed.RawResponse), &absent) != nil || absent == nil || len(absent) != importCleanupNoEntriesCount {
			return fmt.Errorf(importCleanupIdentityErrorFormat, name)
		}
	}
	return nil
}

func verifyImportedCleanupSnapshot(root string, expected map[string]string) error {
	observed, err := snapshotImportedCleanupEvidence(root)
	if err != nil {
		return err
	}
	if !maps.Equal(observed, expected) {
		return errors.New(importCleanupSourceChangedError)
	}
	return nil
}
