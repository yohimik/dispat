package app

// Unit tests of the two manifest operations. They run against real files in a
// t.TempDir(), which is what keeps them units: no config, no git, no plan, and
// nothing faked except the scanner in the one test that needs a failure it
// cannot easily create on disk. The composition claims — the commands through
// the compiled binary, their exit codes over a process boundary — belong to
// the black-box suite in tests/integration.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/manifest"
	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"
)

// manifestRepo writes the given files (path relative to the folder, contents)
// into a fresh temp folder and returns it.
func manifestRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	}
	return root
}

// webPackageJSON is the fixture manifest of most tests here: an identity, one
// dependency per field, and a local path.
const webPackageJSON = `{
  "name": "@acme/web",
  "version": "1.2.0",
  "dependencies": {
    "@acme/core": "^1.2.0"
  },
  "devDependencies": {
    "typescript": "~5.4.0",
    "@acme/tsconfig": "file:../tsconfig"
  }
}
`

// events parses the JSON lines a run logged.
func events(t *testing.T, out string) []map[string]any {
	t.Helper()
	var evs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var ev map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &ev), "line: %s", line)
		evs = append(evs, ev)
	}
	return evs
}

func TestScanManifestsListing(t *testing.T) {
	// The pretty listing is the command's whole output: a title line per
	// manifest carrying identity and ecosystem, one line per declared
	// dependency with its field and range, the local path called out, and a
	// closing count.
	root := manifestRepo(t, map[string]string{
		"package.json":        webPackageJSON,
		"nested/package.json": `{"name":"@acme/nested","version":"0.1.0"}`,
	})
	var out bytes.Buffer
	require.NoError(t, ScanManifests(context.Background(), ScanOptions{
		Root: root, Out: &out, Log: zerolog.Nop(),
	}))

	listing := out.String()
	assert.Contains(t, listing, "package.json  npm  @acme/web@1.2.0")
	assert.Contains(t, listing, "dependencies")
	assert.Contains(t, listing, "@acme/core")
	assert.Contains(t, listing, "^1.2.0")
	assert.Contains(t, listing, "devDependencies")
	assert.Contains(t, listing, "-> ../tsconfig", "a declared local path is part of the answer")
	assert.Contains(t, listing, "nested/package.json  npm  @acme/nested@0.1.0",
		"the default walk descends into sub-folders")
	assert.Contains(t, listing, "2 manifest(s), 3 dependency declaration(s)")
}

func TestScanManifestsRootOnlyStaysPut(t *testing.T) {
	// --root-only is the ScanRoot half of the library: the folder's own
	// identity, nothing from below it.
	root := manifestRepo(t, map[string]string{
		"package.json":        webPackageJSON,
		"nested/package.json": `{"name":"@acme/nested","version":"0.1.0"}`,
	})
	var out bytes.Buffer
	require.NoError(t, ScanManifests(context.Background(), ScanOptions{
		Root: root, RootOnly: true, Out: &out, Log: zerolog.Nop(),
	}))
	assert.Contains(t, out.String(), "@acme/web@1.2.0")
	assert.NotContains(t, out.String(), "nested/package.json")
	assert.Contains(t, out.String(), "1 manifest(s)")
}

func TestScanManifestsScansASubFolder(t *testing.T) {
	// The positional folder resolves against the root, so the command works
	// from a monorepo top pointed at one package.
	root := manifestRepo(t, map[string]string{"packages/web/package.json": webPackageJSON})
	var out bytes.Buffer
	require.NoError(t, ScanManifests(context.Background(), ScanOptions{
		Root: root, Dir: "packages/web", Out: &out, Log: zerolog.Nop(),
	}))
	assert.Contains(t, out.String(), "package.json  npm  @acme/web@1.2.0",
		"paths are relative to the scanned folder, not the root")
	assert.Contains(t, out.String(), "1 manifest(s)")
}

func TestScanManifestsJSONEvents(t *testing.T) {
	// The machine-readable rendering: one event per manifest carrying the
	// identity and every declaration, then a summary event.
	root := manifestRepo(t, map[string]string{"package.json": webPackageJSON})
	var out bytes.Buffer
	require.NoError(t, ScanManifests(context.Background(), ScanOptions{
		Root: root, JSON: true, Out: &out, Log: zerolog.New(&out),
	}))

	evs := events(t, out.String())
	require.Len(t, evs, 2, "one manifest event plus the summary")
	assert.Equal(t, "manifest", evs[0]["message"])
	assert.Equal(t, "package.json", evs[0]["path"])
	assert.Equal(t, "npm", evs[0]["ecosystem"])
	assert.Equal(t, "@acme/web", evs[0]["name"])
	assert.Equal(t, "1.2.0", evs[0]["version"])
	assert.Equal(t, true, evs[0]["root"])
	deps, ok := evs[0]["deps"].([]any)
	require.True(t, ok)
	require.Len(t, deps, 3)
	first := deps[0].(map[string]any)
	assert.Equal(t, "dependencies", first["kind"], "the zero kind is spelled out, never empty")
	assert.Equal(t, "@acme/core", first["name"])
	assert.Equal(t, "^1.2.0", first["range"])
	assert.NotContains(t, first, "localPath", "a declaration without one says nothing about it")
	assert.NotContains(t, evs[0], "buildNumber", "npm carries no build number, so the event omits it")

	// The declared local path is the strongest workspace-edge signal, so it
	// travels with the declaration that carries it.
	local := deps[1].(map[string]any)
	assert.Equal(t, "@acme/tsconfig", local["name"])
	assert.Equal(t, "devDependencies", local["kind"])
	assert.Equal(t, "../tsconfig", local["localPath"])

	assert.Equal(t, "scan complete", evs[1]["message"])
	assert.Equal(t, float64(1), evs[1]["manifests"])
	assert.Equal(t, float64(3), evs[1]["dependencies"])
	assert.Equal(t, float64(0), evs[1]["failed"])
}

func TestScanManifestsJSONCarriesTheOptionalFields(t *testing.T) {
	// A build number and a declared local path only appear when the manifest
	// has them, so the event stays quiet about what a format does not carry.
	root := manifestRepo(t, map[string]string{
		"AndroidManifest.xml": `<manifest package="com.acme.app" android:versionName="2.1.0" android:versionCode="42"/>`,
	})
	var out bytes.Buffer
	require.NoError(t, ScanManifests(context.Background(), ScanOptions{
		Root: root, JSON: true, Out: &out, Log: zerolog.New(&out),
	}))
	evs := events(t, out.String())
	require.Len(t, evs, 2)
	assert.Equal(t, "42", evs[0]["buildNumber"])
	assert.Equal(t, "2.1.0", evs[0]["version"])

	// And the pretty rendering says the same thing in one line.
	var pretty bytes.Buffer
	require.NoError(t, ScanManifests(context.Background(), ScanOptions{
		Root: root, Out: &pretty, Log: zerolog.Nop(),
	}))
	assert.Contains(t, pretty.String(), "com.acme.app@2.1.0  build 42")
}

func TestScanManifestsTitleWithoutAnIdentity(t *testing.T) {
	// go.mod declares a name but no version; requirements files declare
	// neither. The title line degrades to whatever the format actually has
	// instead of printing a stray "@".
	root := manifestRepo(t, map[string]string{
		"go.mod":           "module github.com/acme/core\n\ngo 1.26\n",
		"requirements.txt": "requests==2.31.0\n",
	})
	var out bytes.Buffer
	require.NoError(t, ScanManifests(context.Background(), ScanOptions{
		Root: root, Out: &out, Log: zerolog.Nop(),
	}))
	assert.Contains(t, out.String(), "go.mod  gomod  github.com/acme/core")
	assert.NotContains(t, out.String(), "github.com/acme/core@")
	assert.Contains(t, out.String(), "requirements.txt  python\n")
}

func TestScanManifestsReportsPartialParseFailures(t *testing.T) {
	// The library's partial-result contract, carried through the command: the
	// broken manifest is reported, the healthy one is still listed, and the
	// command succeeds. --strict is what turns the same run into a failure.
	root := manifestRepo(t, map[string]string{
		"package.json":       webPackageJSON,
		"other/package.json": "{ this is not json",
	})
	for _, strict := range []bool{false, true} {
		var out, logs bytes.Buffer
		err := ScanManifests(context.Background(), ScanOptions{
			Root: root, Strict: strict, Out: &out, Log: zerolog.New(&logs),
		})
		assert.Contains(t, out.String(), "@acme/web@1.2.0", "the parsed manifests are still reported")
		assert.Contains(t, out.String(), "1 manifest(s)")
		assert.Contains(t, logs.String(), "manifest failed to parse")
		assert.Contains(t, logs.String(), "other/package.json", "the failure names the file")
		if strict {
			require.Error(t, err)
			assert.Contains(t, err.Error(), "1 manifest(s) failed to parse")
			continue
		}
		require.NoError(t, err)
	}
}

func TestScanManifestsRejectsAFolderThatIsNot(t *testing.T) {
	root := manifestRepo(t, map[string]string{"package.json": webPackageJSON})
	var out, logs bytes.Buffer

	err := ScanManifests(context.Background(), ScanOptions{
		Root: root, Dir: "nowhere", Out: &out, Log: zerolog.New(&logs),
	})
	require.Error(t, err, "a folder that does not exist is the typo it looks like")
	assert.Contains(t, logs.String(), "cannot scan the folder")

	err = ScanManifests(context.Background(), ScanOptions{
		Root: root, Dir: "package.json", Out: &out, Log: zerolog.New(&logs),
	})
	require.Error(t, err, "a file is not a folder to scan")
	assert.Contains(t, err.Error(), "not a folder")
}

// failingScanner is a Scanner whose walk was interrupted: the swappable seam
// the App already uses for the same purpose.
type failingScanner struct{ err error }

func (f failingScanner) Scan(context.Context, string) ([]scanner.Manifest, error) {
	return nil, f.err
}
func (f failingScanner) ScanRoot(context.Context, string) ([]scanner.Manifest, error) {
	return nil, f.err
}

func TestScanManifestsPropagatesCancellation(t *testing.T) {
	// An interrupted scan is an interruption, not a parse failure: the
	// context error comes back as itself, so a Ctrl-C is never reported as a
	// broken manifest.
	root := manifestRepo(t, map[string]string{"package.json": webPackageJSON})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, logs bytes.Buffer
	err := ScanManifests(ctx, ScanOptions{
		Root: root, Scanner: failingScanner{err: context.Canceled},
		Out: &out, Log: zerolog.New(&logs),
	})
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotContains(t, logs.String(), "failed to parse")
}

func TestWriteManifestsRewritesAndPreservesEverythingElse(t *testing.T) {
	// The point of pkg/writer, through the command: only the version text
	// being changed moves, and every other byte of the file survives.
	root := manifestRepo(t, map[string]string{"packages/web/package.json": webPackageJSON})
	var out bytes.Buffer
	require.NoError(t, WriteManifests(context.Background(), WriteOptions{
		Root:  root,
		Paths: []string{"packages/web/package.json"},
		Edits: []writer.Edit{
			{Name: "@acme/core", Kind: manifest.KindDependencies, Range: "^1.3.0"},
			{Name: "typescript", Kind: manifest.KindDevDependencies, Range: "~5.5.0"},
		},
		Version: "1.3.0",
		Out:     &out, Log: zerolog.Nop(),
	}))

	data, err := os.ReadFile(filepath.Join(root, "packages/web/package.json"))
	require.NoError(t, err)
	want := strings.NewReplacer(
		`"version": "1.2.0"`, `"version": "1.3.0"`,
		`"@acme/core": "^1.2.0"`, `"@acme/core": "^1.3.0"`,
		`"typescript": "~5.4.0"`, `"typescript": "~5.5.0"`,
	).Replace(webPackageJSON)
	assert.Equal(t, want, string(data), "nothing but the three version scalars may differ")

	listing := out.String()
	assert.Contains(t, listing, "packages/web/package.json")
	assert.Contains(t, listing, "version written")
	assert.Contains(t, listing, "applied  dependencies  @acme/core  ^1.3.0")
	assert.Contains(t, listing, "applied  devDependencies  typescript  ~5.5.0")
	assert.Contains(t, listing, "1 manifest(s): 2 applied, 0 skipped, 0 missing")
}

func TestWriteManifestsReportsMissingAndStrict(t *testing.T) {
	// An edit the manifest does not declare is reported and tolerated, since
	// a batch across manifests is allowed to overshoot; --strict is the CI
	// spelling that refuses it.
	files := map[string]string{"package.json": webPackageJSON}
	for _, strict := range []bool{false, true} {
		root := manifestRepo(t, files)
		var out bytes.Buffer
		err := WriteManifests(context.Background(), WriteOptions{
			Root:   root,
			Paths:  []string{"package.json"},
			Edits:  []writer.Edit{{Name: "nowhere", Kind: manifest.KindDependencies, Range: "1.0.0"}},
			Strict: strict, Out: &out, Log: zerolog.New(&out),
		})
		assert.Contains(t, out.String(), "missing  dependencies  nowhere  1.0.0")
		assert.Contains(t, out.String(), "0 applied, 0 skipped, 1 missing")
		if strict {
			require.Error(t, err)
			assert.Contains(t, err.Error(), "does not declare")
			continue
		}
		require.NoError(t, err)
	}
}

func TestWriteManifestsReportsSkippedWithoutFailing(t *testing.T) {
	// A version deferring to something outside the file is left alone rather
	// than overwritten with a literal. That is the normal state of a healthy
	// manifest, so it is reported and never fails the command, --strict or
	// not.
	root := manifestRepo(t, map[string]string{
		"pom.xml": `<project>
  <groupId>com.acme</groupId>
  <artifactId>web</artifactId>
  <version>1.2.0</version>
  <dependencies>
    <dependency>
      <groupId>com.acme</groupId>
      <artifactId>core</artifactId>
      <version>${core.version}</version>
    </dependency>
  </dependencies>
</project>
`})
	var out bytes.Buffer
	require.NoError(t, WriteManifests(context.Background(), WriteOptions{
		Root:   root,
		Paths:  []string{"pom.xml"},
		Edits:  []writer.Edit{{Name: "com.acme:core", Kind: manifest.KindDependencies, Range: "1.3.0"}},
		Strict: true, Out: &out, Log: zerolog.Nop(),
	}))
	assert.Contains(t, out.String(), "skipped  dependencies  com.acme:core  1.3.0")
	assert.Contains(t, out.String(), "0 applied, 1 skipped, 0 missing")

	data, err := os.ReadFile(filepath.Join(root, "pom.xml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "${core.version}", "the indirection the property exists for survives")
}

func TestWriteManifestsAddsAndRemovesARedirect(t *testing.T) {
	// The Replace half: point a dependency at a local folder, then let the
	// declaration resolve normally again, which is what a release has to do
	// before publishing.
	root := manifestRepo(t, map[string]string{
		"go.mod": "module github.com/acme/web\n\ngo 1.26\n\nrequire github.com/acme/core v1.2.0\n",
	})
	gomod := filepath.Join(root, "go.mod")

	var out bytes.Buffer
	require.NoError(t, WriteManifests(context.Background(), WriteOptions{
		Root: root, Paths: []string{"go.mod"},
		Replacements: []writer.Replacement{{Name: "github.com/acme/core", Path: "../core"}},
		Out:          &out, Log: zerolog.Nop(),
	}))
	data, err := os.ReadFile(gomod)
	require.NoError(t, err)
	assert.Contains(t, string(data), "replace github.com/acme/core => ../core")
	assert.Contains(t, out.String(), "applied  replace  github.com/acme/core  ../core")

	out.Reset()
	require.NoError(t, WriteManifests(context.Background(), WriteOptions{
		Root: root, Paths: []string{"go.mod"},
		Replacements: []writer.Replacement{{Name: "github.com/acme/core"}},
		Out:          &out, Log: zerolog.Nop(),
	}))
	data, err = os.ReadFile(gomod)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "replace", "an empty path removes the directive")
	assert.Contains(t, out.String(), "applied  replace  github.com/acme/core  (removed)")
}

func TestWriteManifestsAttemptsEveryManifestAndJoinsTheFailures(t *testing.T) {
	// One unusable path must not cost the others their edits: each file's
	// write is atomic and independent, so the run reports the whole picture
	// and fails at the end.
	root := manifestRepo(t, map[string]string{
		"package.json": webPackageJSON,
		"notes.txt":    "not a manifest\n",
	})
	var out, logs bytes.Buffer
	err := WriteManifests(context.Background(), WriteOptions{
		Root:    root,
		Paths:   []string{"notes.txt", "package.json"},
		Version: "1.3.0",
		Out:     &out, Log: zerolog.New(&logs),
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, writer.ErrUnsupportedManifest)
	assert.Contains(t, logs.String(), "manifest edit failed")

	data, readErr := os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, readErr)
	assert.Contains(t, string(data), `"version": "1.3.0"`, "the usable manifest was still written")
}

func TestWriteManifestsJSONEvents(t *testing.T) {
	// The machine-readable rendering: one event per manifest splitting the
	// outcomes the way pkg/writer does, and a message that says plainly
	// whether the file changed.
	root := manifestRepo(t, map[string]string{"package.json": webPackageJSON})
	var out bytes.Buffer
	require.NoError(t, WriteManifests(context.Background(), WriteOptions{
		Root:  root,
		Paths: []string{"package.json"},
		Edits: []writer.Edit{
			{Name: "@acme/core", Kind: manifest.KindDependencies, Range: "^1.3.0"},
			{Name: "nowhere", Kind: manifest.KindDevDependencies, Range: "1.0.0"},
		},
		Version: "1.3.0",
		JSON:    true, Out: &out, Log: zerolog.New(&out),
	}))

	evs := events(t, out.String())
	require.Len(t, evs, 2)
	assert.Equal(t, "manifest updated", evs[0]["message"])
	assert.Equal(t, "package.json", evs[0]["path"])
	assert.Equal(t, true, evs[0]["versionWritten"])
	edits := evs[0]["edits"].(map[string]any)
	applied := edits["applied"].([]any)
	require.Len(t, applied, 1)
	assert.Equal(t, "@acme/core", applied[0].(map[string]any)["name"])
	assert.Equal(t, "dependencies", applied[0].(map[string]any)["kind"])
	missing := edits["missing"].([]any)
	require.Len(t, missing, 1)
	assert.Equal(t, "devDependencies", missing[0].(map[string]any)["kind"])
	assert.NotContains(t, edits, "skipped", "an empty outcome is left out of the event")

	assert.Equal(t, "write complete", evs[1]["message"])
	assert.Equal(t, float64(1), evs[1]["applied"])
	assert.Equal(t, float64(1), evs[1]["missing"])
}

func TestWriteManifestsSaysWhenNothingChanged(t *testing.T) {
	// Rewriting an already-reconciled manifest changes nothing, and the event
	// says so rather than claiming a write.
	root := manifestRepo(t, map[string]string{"package.json": webPackageJSON})
	var out bytes.Buffer
	require.NoError(t, WriteManifests(context.Background(), WriteOptions{
		Root:  root,
		Paths: []string{"package.json"},
		Edits: []writer.Edit{{Name: "@acme/core", Kind: manifest.KindDependencies, Range: "^1.2.0"}},
		JSON:  true, Out: &out, Log: zerolog.New(&out),
	}))
	evs := events(t, out.String())
	require.Len(t, evs, 2)
	assert.Equal(t, "manifest unchanged", evs[0]["message"])
	assert.Equal(t, false, evs[0]["versionWritten"])
	assert.Equal(t, float64(0), evs[1]["applied"])
}

func TestWriteManifestsPropagatesCancellation(t *testing.T) {
	// A cancelled run stops issuing work rather than writing the rest of the
	// batch, and reports the interruption as itself.
	root := manifestRepo(t, map[string]string{"package.json": webPackageJSON})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := WriteManifests(ctx, WriteOptions{
		Root: root, Paths: []string{"package.json"}, Version: "1.3.0",
		Out: &out, Log: zerolog.Nop(),
	})
	assert.ErrorIs(t, err, context.Canceled)
	data, readErr := os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, readErr)
	assert.Equal(t, webPackageJSON, string(data), "nothing may be written after the cancellation")
}

func TestUnwrapJoined(t *testing.T) {
	// Each failed manifest is reported on its own line, whichever shape the
	// scanner's error contract hands back.
	assert.Nil(t, unwrapJoined(nil))

	single := errors.New("one")
	assert.Equal(t, []error{single}, unwrapJoined(single))

	second := errors.New("two")
	assert.Len(t, unwrapJoined(errors.Join(single, second)), 2)
}
