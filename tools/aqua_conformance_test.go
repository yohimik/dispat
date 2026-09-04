package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	aquaconfig "github.com/aquaproj/aqua/v2/pkg/config/aqua"
	"github.com/yohimik/dispat/pkg/scanner"
	"gopkg.in/yaml.v2"
)

// This tools-only check holds our deliberately small production parser to
// Aqua's public configuration type without pulling Aqua's runtime reader (and
// its registry/expression behavior) into scanner or its TinyGo build graph.
func TestAquaPublicPackageCompatibility(t *testing.T) {
	src := []byte(`packages:
- name: cli/cli@v2.1.0
  version: ignored-by-inline
- name: tool/name
  registry: private
  version: 1.2.3
`)
	var upstream aquaconfig.Config
	if err := yaml.Unmarshal(src, &upstream); err != nil {
		t.Fatal(err)
	}
	if len(upstream.Packages) != 2 || upstream.Packages[0].Name != "cli/cli" || upstream.Packages[0].Version != "v2.1.0" {
		t.Fatalf("unexpected upstream interpretation: %#v", upstream.Packages)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aqua.yaml"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	mans, err := scanner.Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(mans) != 1 || len(mans[0].Deps) != 2 || mans[0].Deps[0].Range != upstream.Packages[0].Version {
		t.Fatalf("scanner disagrees with Aqua: %#v", mans)
	}
}
