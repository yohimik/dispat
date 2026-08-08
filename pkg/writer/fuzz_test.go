package writer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// FuzzRewriteNpm hammers the byte-splice path: whatever the input file held,
// a rewrite either errors or leaves valid JSON behind — never a torn or
// corrupted manifest. That is the writer's whole contract.
func FuzzRewriteNpm(f *testing.F) {
	seeds := []string{
		`{"name":"a","version":"1.0.0","dependencies":{"b":"^1.0.0"}}`,
		"{\n\t\"dependencies\": { \"b\": \"workspace:*\" }\n}",
		`{"dependencies":{"b":{"version":"1.0.0"}}}`,
		`{"version":"1.0.0"}`,
		`[]`,
		`{`,
		``,
		`{"dependencies":{"we\"ird":"1"}}`,
	}
	for _, s := range seeds {
		f.Add(s, "b", "^2.0.0", "2.0.0")
	}
	f.Fuzz(func(t *testing.T, content, name, rng, version string) {
		dir := t.TempDir()
		path := filepath.Join(dir, "package.json")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Skip()
		}
		_, err := Rewrite(path, version, []Edit{{Name: name, Range: rng}})
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("manifest vanished: %v", readErr)
		}
		if err == nil && json.Valid([]byte(content)) && !json.Valid(data) {
			t.Fatalf("rewrite corrupted valid JSON:\n in: %q\nout: %q", content, data)
		}
		if err != nil && string(data) != content {
			t.Fatalf("a failed rewrite modified the file:\n in: %q\nout: %q", content, data)
		}
	})
}
