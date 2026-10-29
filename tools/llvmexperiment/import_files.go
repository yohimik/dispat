package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const importedEvidenceDirectory = "build-campaign-evidence"

func inspectImportTree(ctx context.Context, root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return fmt.Errorf("campaign evidence is not a regular file or directory: %s", path)
		}
		return nil
	})
}

func copyImportTree(ctx context.Context, source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.Mkdir(target, directoryMode)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("campaign evidence is not a regular file: %s", path)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
		if err != nil {
			return errors.Join(err, input.Close())
		}
		_, copyErr := io.Copy(output, input)
		return errors.Join(copyErr, output.Close(), input.Close())
	})
}

// Stage and validate both campaigns before replacing only their owned cells.
// Existing standard release experiments remain byte-for-byte untouched.
func installCampaignImport(ctx context.Context, results string, imported *campaignImport) (failure error) {
	if err := os.MkdirAll(results, directoryMode); err != nil {
		return err
	}
	info, err := os.Lstat(results)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("campaign results must be a real directory")
	}
	stage, err := os.MkdirTemp(results, ".build-campaign-import-")
	if err != nil {
		return err
	}
	retainStage := false
	defer func() {
		if !retainStage {
			failure = errors.Join(failure, os.RemoveAll(stage))
		}
	}()
	newRoot, oldRoot := filepath.Join(stage, "new"), filepath.Join(stage, "old")
	if err := os.Mkdir(newRoot, directoryMode); err != nil {
		return err
	}
	if err := os.Mkdir(oldRoot, directoryMode); err != nil {
		return err
	}
	names := make([]string, 0, len(imported.cells))
	for name, root := range imported.cells {
		if err := copyImportTree(ctx, root, filepath.Join(newRoot, name)); err != nil {
			return err
		}
		names = append(names, name)
	}
	// Copying is followed by another validation so imported bytes, rather than
	// an earlier read of mutable input files, determine the accepted report.
	for _, campaign := range imported.Campaigns {
		for name, expected := range campaign.Cells {
			body, err := os.ReadFile(filepath.Join(newRoot, name, "verdict.json"))
			if err != nil {
				return err
			}
			if importDigest(body) != expected {
				return fmt.Errorf("campaign verdict changed during import: %s", name)
			}
			var record verdict
			if err := json.Unmarshal(body, &record); err != nil {
				return err
			}
			if err := verifyRetainedEvidence(filepath.Join(newRoot, name), record.ExecutionMeasurement); err != nil {
				return err
			}
		}
	}
	if err := retainImportedEnvironment(newRoot, imported); err != nil {
		return err
	}
	if err := writeExclusiveJSON(filepath.Join(newRoot, importedCampaignManifest), imported); err != nil {
		return err
	}
	names = append(names, importedCampaignManifest, importedEvidenceDirectory)
	sort.Strings(names)
	old, err := previousBuildCampaignFiles(results)
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(results, name)); err == nil && !old[name] {
			return fmt.Errorf("campaign destination already belongs to another result: %s", name)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	moved, installed := []string{}, []string{}
	defer func() {
		if failure == nil {
			return
		}
		for _, name := range installed {
			failure = errors.Join(failure, os.RemoveAll(filepath.Join(results, name)))
		}
		for _, name := range moved {
			if err := os.Rename(filepath.Join(oldRoot, name), filepath.Join(results, name)); err != nil {
				retainStage = true
				failure = errors.Join(failure, fmt.Errorf("prior campaign data remains in %s: %w", oldRoot, err))
			}
		}
	}()
	for name := range old {
		if err := os.Rename(filepath.Join(results, name), filepath.Join(oldRoot, name)); err != nil {
			return err
		}
		moved = append(moved, name)
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(newRoot, name), filepath.Join(results, name)); err != nil {
			return err
		}
		installed = append(installed, name)
	}
	return nil
}

func previousBuildCampaignFiles(root string) (map[string]bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	owned := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if name == importedEvidenceDirectory {
			if !entry.IsDir() {
				return nil, errors.New("prior imported environment evidence is not a real directory")
			}
			var prior campaignImport
			if err := readHardwareJSON(filepath.Join(root, importedCampaignManifest), &prior); err != nil {
				return nil, err
			}
			if prior.Source == "" || prior.Binary == "" || len(prior.Campaigns) != 2 {
				return nil, errors.New("prior environment evidence has no complete import manifest")
			}
			owned[name] = true
			continue
		}
		if name == importedCampaignManifest {
			if !entry.Type().IsRegular() {
				return nil, errors.New("prior import manifest is not a regular file")
			}
			owned[name] = true
			continue
		}
		managed := strings.HasPrefix(name, llvmWorkload+"-") || strings.HasPrefix(name, kernelWorkload+"-")
		if !entry.IsDir() {
			if managed {
				return nil, fmt.Errorf("prior build cell is not a real directory: %s", name)
			}
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, name, "verdict.json"))
		if errors.Is(err, os.ErrNotExist) && !managed {
			continue
		}
		if err != nil {
			return nil, err
		}
		var record verdict
		if err := json.Unmarshal(content, &record); err != nil {
			return nil, err
		}
		if record.Experiment != llvmWorkload && record.Experiment != kernelWorkload {
			if managed {
				return nil, fmt.Errorf("prior build cell names a different experiment: %s", name)
			}
			continue
		}
		if !managed {
			return nil, fmt.Errorf("unmanaged build comparison would mix with selected campaigns: %s", name)
		}
		owned[name] = true
	}
	return owned, nil
}
