package app

import (
	"fmt"
	"os"
	"path/filepath"
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
      "run": {
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
    run:
      build: build
      publish: publish
`
	initTOML = `# dispat configuration — https://github.com/yohimik/dispat

[scripts]
build = "echo build $DISPAT_PACKAGE@$DISPAT_NEW_VERSION"
publish = "echo publish $DISPAT_PACKAGE@$DISPAT_NEW_VERSION"

[spaces.packages]
path = "packages"

[spaces.packages.run]
build = "build"
publish = "publish"
`
)

// InitConfig writes a starter configuration file into dir. format selects
// "json" (the default when empty), "yaml" or "toml"; the file is named
// dispat.<format>. An existing file is never overwritten — that is an error,
// so a typo cannot silently discard a real configuration. It returns the
// name of the file written and needs neither a loaded config nor a git
// repository.
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
	name := "dispat." + format
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists, not overwriting", name)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return name, nil
}
