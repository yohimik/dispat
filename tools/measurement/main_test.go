package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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
}
