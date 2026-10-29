package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudClientPreparationVerifiesEveryExecutorBeforeMeasurement(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is unavailable for the pinned Nx API bridge test")
	}
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-client", true: "different-client"}[mismatch], func(t *testing.T) {
			tools := t.TempDir()
			module := filepath.Join(tools, "nx", "dist", "src", "nx-cloud", "update-manager.js")
			if err := os.MkdirAll(filepath.Dir(module), directoryMode); err != nil {
				t.Fatal(err)
			}
			const updater = `
const fs = require('node:fs');
const path = require('node:path');
exports.getBundleInstallDefaultLocation = () => path.join(process.cwd(), '.nx/cache/cloud');
exports.verifyOrUpdateNxCloudClient = async () => {
  const version = 'test-client-1';
  const directory = path.join(exports.getBundleInstallDefaultLocation(), version);
  fs.mkdirSync(directory, {recursive: true});
  const content = process.env.LLVM_TEST_CLIENT_MISMATCH === 'true' && process.cwd().endsWith('llvm-node-2') ? 'different' : 'same';
  fs.writeFileSync(path.join(directory, 'index.js'), content);
  return {version, nxCloudClient: {commands: {}}};
};
`
			if err := os.WriteFile(module, []byte(updater), fileMode); err != nil {
				t.Fatal(err)
			}
			settings, err := parseOptions([]string{"--tool", nxTool, "--mode", distributedMode, "--tools", tools})
			if err != nil {
				t.Fatal(err)
			}
			data, err := prepareFixture(context.Background(), settings, t.TempDir(), io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			versions := map[string]string{}
			environment := []string{"LLVM_TEST_CLIENT_MISMATCH=" + map[bool]string{false: "false", true: "true"}[mismatch]}
			err = prepareCloudClients(context.Background(), data, environment, io.Discard, versions)
			if mismatch {
				if err == nil || !strings.Contains(err.Error(), "clients differ") || len(versions) != 0 {
					t.Fatalf("different client bundles were accepted: %v, %+v", err, versions)
				}
				return
			}
			if err != nil || versions[cloudClientVersionKey] != "test-client-1" || len(versions[cloudClientDigestKey]) != 64 {
				t.Fatalf("client identity was not prepared: %v, %+v", err, versions)
			}
			for _, node := range data.Nodes {
				if _, err := os.Stat(filepath.Join(node.Root, cloudClientMetadataPath)); err != nil {
					t.Fatalf("executor client was not prepared: %v", err)
				}
			}
		})
	}
}

func TestCloudClientDigestBindsNamesAndContentsAndRejectsLinks(t *testing.T) {
	root := t.TempDir()
	if _, err := hashCloudClientDirectory(root); err == nil {
		t.Fatal("empty client bundle was accepted")
	}
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.WriteFile(first, []byte("client"), fileMode); err != nil {
		t.Fatal(err)
	}
	digest, err := hashCloudClientDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(first, second); err != nil {
		t.Fatal(err)
	}
	renamed, err := hashCloudClientDirectory(root)
	if err != nil || digest == renamed {
		t.Fatalf("client digest ignored file names: %v", err)
	}
	if err := os.Symlink(second, first); err != nil {
		t.Fatal(err)
	}
	if _, err := hashCloudClientDirectory(root); err == nil {
		t.Fatal("linked client bundle was accepted")
	}
}
