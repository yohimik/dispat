package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

const evidenceReaderPermissions os.FileMode = 0o444

func TestDeriveFilePreservesExtraEvidenceAndNeverOverwrites(t *testing.T) {
	input := "../../packages/docs/plugins/test-report/testdata/measurement.json"
	output := filepath.Join(t.TempDir(), "measurement.json")
	if err := deriveFile(input, output); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&evidenceReaderPermissions != evidenceReaderPermissions {
		t.Fatalf("measurement evidence is not readable by the artifact collector: mode %04o", info.Mode().Perm())
	}
	if err := executionmeasurement.Validate(body); err != nil {
		t.Fatalf("written measurement evidence is invalid: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result["producerExtra"].(map[string]any)["kept"] != true {
		t.Fatal("extra producer evidence disappeared")
	}
	if err := deriveFile(input, output); err == nil {
		t.Fatal("existing measurement was overwritten")
	}
	retained, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, retained) {
		t.Fatal("refused overwrite changed the retained measurement evidence")
	}
}
