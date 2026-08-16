package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yohimik/dispat/services/dispat/internal/fsx"
)

// The starter configuration `dispat init` writes: the same minimal config in
// each format — two named scripts and one space at packages/ — so the very
// next `dispat status` has something loadable to work from. The echo commands
// are placeholders to replace with real build and publish commands.
const (
	initJSON = `{
  "scripts": {
    "build": "echo build $DISPAT_PACKAGE@$DISPAT_NEW_VERSION",
    "publish": "echo publish $DISPAT_PACKAGE@$DISPAT_NEW_VERSION"
  },
  "spaces": {
    "packages": {
      "path": "packages",
      "flow": {
        "build": "build",
        "publish": "publish"
      }
    }
  }
}
`
	initYAML = `# dispat configuration — https://github.com/yohimik/dispat
scripts:
  build: echo build $DISPAT_PACKAGE@$DISPAT_NEW_VERSION
  publish: echo publish $DISPAT_PACKAGE@$DISPAT_NEW_VERSION

spaces:
  packages:
    path: packages
    flow:
      build: build
      publish: publish
`
	initTOML = `# dispat configuration — https://github.com/yohimik/dispat

[scripts]
build = "echo build $DISPAT_PACKAGE@$DISPAT_NEW_VERSION"
publish = "echo publish $DISPAT_PACKAGE@$DISPAT_NEW_VERSION"

[spaces.packages]
path = "packages"

[spaces.packages.flow]
build = "build"
publish = "publish"
`
)

// InitConfig writes a starter configuration file into dir. format selects
// "json" (the default when empty), "yaml" or "toml"; the file is named
// dispat.<format>. It returns the name of the file written and needs no
// loaded config, but it does insist on dir being a git repository *root*
// (holding a .git entry — a directory, or a file for worktrees): the config
// establishes the effective monorepo root for every later command, so writing
// it anywhere else would plant a config that plans against the wrong tree.
// An existing config file is never overwritten either — that is an error, so
// a typo cannot silently discard a real configuration.
func InitConfig(dir, format string) (string, error) {
	var content string
	switch format {
	case "", "json":
		format, content = "json", initJSON
	case "yaml":
		content = initYAML
	case "toml":
		content = initTOML
	default:
		return "", fmt.Errorf("unknown config format %q (want json, yaml or toml)", format)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return "", fmt.Errorf("%s is not a git repository root (no .git); run `git init` first, or run `dispat init` at the repository root", dir)
	}
	name := "dispat." + format
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists, not overwriting", name)
	}
	if err := fsx.WriteFileAtomic(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return name, nil
}
