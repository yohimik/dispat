package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	importPrepareDockerInfo             = "prepare-docker-info.json"
	importCoordinatorDockerInfo         = "coordinator-docker-info.json"
	importWorkerDockerSuffix            = "-docker-info.json"
	importDockerCloudDirectoryName      = "cloud"
	importDockerCurrentDirectoryName    = "."
	importDockerClassicDriverName       = "overlay2"
	importDockerMissingVersionValue     = ""
	importDockerContainerdStatusKey     = "driver-type"
	importDockerContainerdStatusValue   = "io.containerd.snapshotter.v1"
	importDockerStatusMinimumFieldCount = 2
	importDockerStatusKeyIndex          = 0
	importDockerStatusValueIndex        = 1
	importDockerInstanceIdentityError   = "Docker store observation has an invalid instance identity"
	importDockerObservationErrorFormat  = "Docker store observation %s: %w"
	importDockerTrailingDataErrorFormat = "Docker store observation %s has trailing JSON or invalid bytes"
	importDockerClassicStoreErrorFormat = "Docker store observation %s must identify classic overlay2 and its actual daemon version"
	importDockerContainerdErrorFormat   = "Docker store observation %s uses the containerd image store"
)

// These are the fields explicitly selected by the preparation workflow and
// cloud driver. Reject broader daemon dumps rather than publishing credentials
// or proxy settings from an accidentally unsanitized observation.
type importedDockerStore struct {
	ServerVersion string     `json:"ServerVersion"`
	Driver        string     `json:"Driver"`
	DriverStatus  [][]string `json:"DriverStatus"`
	Architecture  string     `json:"Architecture"`
	OSType        string     `json:"OSType"`
	CgroupDriver  string     `json:"CgroupDriver"`
	CgroupVersion string     `json:"CgroupVersion"`
}

func validateImportedDockerStores(environment string, instances []hardwareInstance) error {
	names := []string{importPrepareDockerInfo, importCoordinatorDockerInfo}
	for _, instance := range instances {
		if !filepath.IsLocal(instance.Name) || instance.Name == importDockerCurrentDirectoryName || filepath.Base(instance.Name) != instance.Name {
			return fmt.Errorf(importDockerInstanceIdentityError)
		}
		names = append(names, instance.Name+importWorkerDockerSuffix)
	}
	for _, name := range names {
		path := filepath.Join(environment, importDockerCloudDirectoryName, name)
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf(importDockerObservationErrorFormat, name, err)
		}
		var observed importedDockerStore
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&observed); err != nil {
			return fmt.Errorf(importDockerObservationErrorFormat, name, err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf(importDockerTrailingDataErrorFormat, name)
		}
		if observed.Driver != importDockerClassicDriverName || strings.TrimSpace(observed.ServerVersion) == importDockerMissingVersionValue {
			return fmt.Errorf(importDockerClassicStoreErrorFormat, name)
		}
		for _, status := range observed.DriverStatus {
			if len(status) >= importDockerStatusMinimumFieldCount && status[importDockerStatusKeyIndex] == importDockerContainerdStatusKey && status[importDockerStatusValueIndex] == importDockerContainerdStatusValue {
				return fmt.Errorf(importDockerContainerdErrorFormat, name)
			}
		}
	}
	return nil
}
