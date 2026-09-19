// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for the private live-pin coordinator a composed release
// hands to its package scripts. It is transient run state rather than
// repository state, so a nested command has to prove the context it inherited
// belongs to this workspace before it may admit a source revision from it.
// Every shape below is what that context looks like when something in the
// environment has replaced, truncated or outlived it.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covPolyrepoLivePinProbe is the publish stage of the later package in a
// dependency pair. By the time it runs, the provider has recorded and the
// coordinator holds one real pin, so each case below can copy that directory
// and break exactly one thing about the copy.
const covPolyrepoLivePinProbe = `
if [ "$DISPAT_PACKAGE" != beta ]; then
  echo publishing
  exit 0
fi
LOG=@LOG@
SCRATCH=@SCRATCH@
DISPAT=@DISPAT@
LIVE=$DISPAT_INTERNAL_WORKSPACE_LIVE_PINS
mkdir -p "$SCRATCH"

copy_live() {
  rm -rf "$SCRATCH/$1"
  mkdir -p "$SCRATCH/$1"
  cp "$LIVE/context.json" "$SCRATCH/$1/context.json"
  for pin in "$LIVE"/*.pin; do cp "$pin" "$SCRATCH/$1/"; done
}

probe() {
  name=$1
  shift
  printf '=== %s\n' "$name" >> "$LOG"
  env "$@" "$DISPAT" status >> "$LOG" 2>&1
  printf '>>> %s exit %s\n' "$name" "$?" >> "$LOG"
}

: > "$SCRATCH/plain-file"
probe context-is-not-a-directory DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="$SCRATCH/plain-file"

mkdir -p "$SCRATCH/empty"
probe context-file-is-missing DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="$SCRATCH/empty"

probe context-directory-is-absent DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="$SCRATCH/never-created"

copy_live unreadable-context
printf '%s\n' 'not json at all' > "$SCRATCH/unreadable-context/context.json"
probe context-is-not-json DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="$SCRATCH/unreadable-context"

copy_live unreadable-pin
for pin in "$SCRATCH/unreadable-pin"/*.pin; do printf '%s\n' 'not json at all' > "$pin"; done
probe pin-is-not-json DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="$SCRATCH/unreadable-pin"

copy_live linked-context
rm "$SCRATCH/linked-context/context.json"
ln -s "$LIVE/context.json" "$SCRATCH/linked-context/context.json"
probe context-file-is-a-link DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="$SCRATCH/linked-context"

copy_live foreign-context
printf '%s\n' '{"root":"/nowhere","config":"/nowhere/dispat.json","owners":{},"repositories":[]}' \
  > "$SCRATCH/foreign-context/context.json"
probe context-belongs-elsewhere DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="$SCRATCH/foreign-context"

copy_live trailing-pin
for pin in "$SCRATCH/trailing-pin"/*.pin; do printf '%s\n' '{"owner":"lib-source"}' >> "$pin"; done
probe pin-has-trailing-data DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="$SCRATCH/trailing-pin"

copy_live foreign-pin
for pin in "$SCRATCH/foreign-pin"/*.pin; do
  printf '%s\n' '{"owner":"other-source","revision":"0123456789012345678901234567890123456789"}' > "$pin"
done
probe pin-names-another-owner DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="$SCRATCH/foreign-pin"

copy_live short-pin
for pin in "$SCRATCH/short-pin"/*.pin; do
  printf '%s\n' '{"owner":"lib-source","revision":"0123abc"}' > "$pin"
done
probe pin-is-not-a-commit DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="$SCRATCH/short-pin"

copy_live oversized-pin
for pin in "$SCRATCH/oversized-pin"/*.pin; do
  i=0
  : > "$pin"
  while [ $i -lt 140 ]; do
    printf '%s\n' '0123456789012345678901234567890123456789' >> "$pin"
    i=$((i+1))
  done
done
probe pin-is-oversized DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="$SCRATCH/oversized-pin"

copy_live linked-pin
for pin in "$SCRATCH/linked-pin"/*.pin; do rm "$pin"; ln -s "$LIVE/context.json" "$pin"; done
probe pin-is-a-link DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="$SCRATCH/linked-pin"

copy_live vanished-pin
rm -f "$SCRATCH/vanished-pin"/*.pin
probe pin-has-vanished DISPAT_INTERNAL_WORKSPACE_LIVE_PINS="$SCRATCH/vanished-pin"

probe owners-are-unreadable DISPAT_INTERNAL_WORKSPACE_OWNERS=not-json
probe repositories-are-unreadable DISPAT_INTERNAL_WORKSPACE_REPOSITORIES=not-json
probe repository-is-the-control-identity DISPAT_INTERNAL_WORKSPACE_REPOSITORIES='["control"]'
probe repository-is-listed-twice DISPAT_INTERNAL_WORKSPACE_REPOSITORIES='["lib-source","lib-source"]'
probe owner-names-an-absent-repository DISPAT_INTERNAL_WORKSPACE_OWNERS='{"PACKAGE_GHOST":"ghost-source"}'

probe context-is-intact DISPAT_PROBE=intact
echo publishing
`

// TestCovPolyrepoNestedCommandRefusesALivePinContextItCannotTrust: the live
// coordinator is the one thing in a composed run that lets a nested command
// accept a source revision no control gitlink names yet, so every part of it
// is checked before it is believed: the directory itself, the context that
// binds it to this control root and configuration, and each atomic pin record.
// Anything that does not hold refuses the nested command by name rather than
// silently admitting an unpinned checkout, and a pin that is simply not there
// is no refusal at all.
func TestCovPolyrepoNestedCommandRefusesALivePinContextItCannotTrust(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "alpha")
	source.SeedPackage("packages", "beta")
	source.Commit("feat(alpha,beta): bootstrap the source")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	logPath := control.Path("nested-live-pins.log")
	scratch := t.TempDir()
	probe := strings.NewReplacer(
		"@LOG@", harness.ShQuote(logPath),
		"@SCRATCH@", harness.ShQuote(scratch),
		"@DISPAT@", control.DispatCommand(),
	).Replace(covPolyrepoLivePinProbe)

	cfg := covPolyrepoFile()
	cfg.Spaces = covPolyrepoSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg.Dependencies = models.Dependencies{{Consumer: "beta", Provider: "alpha"}}
	cfg.Scripts["publish"] = models.Script{probe}
	control.WriteConfigModel(cfg)
	control.Commit("chore: configure a source whose later package inspects the coordinator")

	control.ReleaseOK()
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "alpha@0.1.0")
	assert.Contains(t, polyrepoTags(control, "sources/lib"), "beta@0.1.0")

	raw, err := os.ReadFile(logPath)
	require.NoError(t, err, "the later package's publish stage did not run")
	log := string(raw)

	cases := []struct {
		probe string
		exit  string
		says  string
	}{
		{"context-is-not-a-directory", "1", "live pin context is not a directory"},
		{"context-file-is-missing", "1", "reading live pin context"},
		{"context-directory-is-absent", "1", "opening live pin context"},
		{"context-is-not-json", "1", "reading live pin context"},
		{"pin-is-not-json", "1", "reading live pin for repository"},
		{"context-file-is-a-link", "1", "live pin file is not a regular file"},
		{"context-belongs-elsewhere", "1", "live pin context does not match this workspace"},
		{"pin-has-trailing-data", "1", "live pin file has trailing data"},
		{"pin-names-another-owner", "1", "is malformed"},
		{"pin-is-not-a-commit", "1", "is malformed"},
		{"pin-is-oversized", "1", "live pin file exceeds its size limit"},
		{"pin-is-a-link", "1", "live pin file is not a regular file"},
		{"owners-are-unreadable", "1", "live pin context has no valid package owners"},
		{"repositories-are-unreadable", "1", "live pin context has no valid repository identities"},
		{"repository-is-the-control-identity", "1", "invalid source repository identity"},
		{"repository-is-listed-twice", "1", "duplicate source repository identity"},
		{"owner-names-an-absent-repository", "1", "names unknown repository"},
	}
	for _, tc := range cases {
		t.Run(tc.probe, func(t *testing.T) {
			section := covPolyrepoLogSection(t, log, tc.probe)
			assert.Contains(t, section, tc.says)
			assert.Contains(t, section, ">>> "+tc.probe+" exit "+tc.exit)
		})
	}

	t.Run("a pin that is simply absent is no refusal", func(t *testing.T) {
		section := covPolyrepoLogSection(t, log, "pin-has-vanished")
		assert.Contains(t, section, ">>> pin-has-vanished exit 0",
			"an owner with no record yet has published nothing to admit")
	})

	t.Run("the untouched coordinator is accepted", func(t *testing.T) {
		section := covPolyrepoLogSection(t, log, "context-is-intact")
		assert.Contains(t, section, ">>> context-is-intact exit 0")
		assert.Contains(t, section, `"package":"alpha"`,
			"the nested command still composes the whole fleet")
	})
}

// covPolyrepoLogSection returns one probe's slice of the shared log, so a
// message from a neighbouring case cannot satisfy an assertion.
func covPolyrepoLogSection(t *testing.T, log, name string) string {
	t.Helper()
	start := strings.Index(log, "=== "+name+"\n")
	require.GreaterOrEqual(t, start, 0, "probe %s did not run; log:\n%s", name, log)
	rest := log[start:]
	end := strings.Index(rest, ">>> "+name+" exit ")
	require.GreaterOrEqual(t, end, 0, "probe %s did not finish; log:\n%s", name, log)
	tail := rest[end:]
	if nl := strings.IndexByte(tail, '\n'); nl >= 0 {
		return rest[:end+nl+1]
	}
	return rest
}
