package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	cloudClientMetadataPath = ".nx/llvm-cloud-client.json"
	cloudClientVersionKey   = "nx-cloud"
	cloudClientDigestKey    = "nx-cloud-sha256"
	cloudClientBridge       = `
const FS = require('node:fs');
const PATH = require('node:path');
const UPDATER_PATH = 'node_modules/nx/dist/src/nx-cloud/update-manager.js';
const METADATA_PATH = '.nx/llvm-cloud-client.json';
const UPDATER = require(PATH.join(process.cwd(), UPDATER_PATH));
async function prepareClient() {
  const CLIENT = await UPDATER.verifyOrUpdateNxCloudClient();
  if (!CLIENT || !CLIENT.version) throw new Error('Nx Cloud client has no version');
  const METADATA = {version: CLIENT.version, directory: PATH.join(UPDATER.getBundleInstallDefaultLocation(), CLIENT.version)};
  FS.mkdirSync(PATH.dirname(METADATA_PATH), {recursive: true});
  FS.writeFileSync(METADATA_PATH, JSON.stringify(METADATA));
}
prepareClient().catch(error => { console.error(error.message); process.exitCode = 1; });
`
)

type cloudClientMetadata struct {
	Version   string `json:"version"`
	Directory string `json:"directory"`
}

type cloudClientIdentity struct {
	Version string
	SHA256  string
}

func prepareCloudClients(ctx context.Context, data fixture, environment []string, output io.Writer, versions map[string]string) error {
	roots := []string{data.Root}
	for _, node := range data.Nodes {
		roots = append(roots, node.Root)
	}
	var expected cloudClientIdentity
	for _, root := range roots {
		if err := runCommand(ctx, command{Dir: root, Name: "node", Args: []string{"-e", cloudClientBridge}, Env: environment, Output: output}); err != nil {
			return fmt.Errorf("Nx Cloud client is unavailable before LLVM timing: %w", err)
		}
		identity, err := readCloudClientIdentity(root)
		if err != nil {
			return err
		}
		if expected == (cloudClientIdentity{}) {
			expected = identity
		}
		if identity != expected {
			return fmt.Errorf("Nx Cloud clients differ between LLVM coordinator and executors: %s", root)
		}
	}
	versions[cloudClientVersionKey], versions[cloudClientDigestKey] = expected.Version, expected.SHA256
	fmt.Fprintf(output, "Prepared Nx Cloud client %s, SHA-256 %s, on coordinator and both executors before timing\n", expected.Version, expected.SHA256)
	return nil
}

func readCloudClientIdentity(root string) (cloudClientIdentity, error) {
	content, err := os.ReadFile(filepath.Join(root, cloudClientMetadataPath))
	if err != nil {
		return cloudClientIdentity{}, err
	}
	var metadata cloudClientMetadata
	if err := json.Unmarshal(content, &metadata); err != nil {
		return cloudClientIdentity{}, err
	}
	if metadata.Version == "" || metadata.Directory == "" {
		return cloudClientIdentity{}, fmt.Errorf("Nx Cloud client in %s has incomplete identity metadata", root)
	}
	directory := metadata.Directory
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(root, directory)
	}
	digest, err := hashCloudClientDirectory(directory)
	if err != nil {
		return cloudClientIdentity{}, fmt.Errorf("Nx Cloud client in %s cannot be fingerprinted: %w", root, err)
	}
	return cloudClientIdentity{Version: metadata.Version, SHA256: digest}, nil
}

func hashCloudClientDirectory(directory string) (string, error) {
	hash := sha256.New()
	files := 0
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, pathErr error) error {
		if pathErr != nil {
			return pathErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("Nx Cloud client path %s is not a regular file", path)
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		fileHash := sha256.New()
		if _, err := io.Copy(fileHash, input); err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s\x00%x\n", filepath.ToSlash(relative), fileHash.Sum(nil))
		files++
		return nil
	})
	if err != nil {
		return "", err
	}
	if files == 0 {
		return "", fmt.Errorf("Nx Cloud client directory %s contains no files", directory)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
