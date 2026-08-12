package integration

// Area 16: the manifest commands through the compiled binary. `dispat
// scanner`, `dispat writer` and `dispat replacer` are the pkg/scanner and
// pkg/writer libraries exposed as commands, and everything a unit test cannot
// witness about them lives here: that they run with no config file and no
// release plan at all, that the folder and file paths a shell hands them
// resolve against --root, that their outcomes reach the exit code over a
// process boundary, and that the three command words did not cost the run
// shorthand a script.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// webPackageJSON is the fixture manifest of this file: an identity, a
// dependency in each of two fields, and a declared local path.
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

// findEvent returns the first event whose message matches, failing the test
// when none does.
func findEvent(t *testing.T, events []harness.Event, message string) harness.Event {
	t.Helper()
	for _, e := range events {
		if e.Str("message") == message {
			return e
		}
	}
	t.Fatalf("no %q event among %d events", message, len(events))
	return nil
}

// TestManifestsScannerNeedsNoConfig: the scanner command run against a folder
// with no dispat config anywhere and no git history — the claim that makes it
// usable on any checkout. The listing carries each manifest's identity,
// ecosystem and declarations, the walk descends by default and --root-only
// does not, and the positional folder resolves against --root.
func TestManifestsScannerNeedsNoConfig(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("packages/web/package.json", webPackageJSON)
	r.WriteFile("packages/web/vendored/package.json", `{"name":"@acme/vendored","version":"9.9.9"}`)
	r.WriteFile("packages/web/node_modules/left-pad/package.json", `{"name":"left-pad","version":"1.0.0"}`)
	// Deliberately no dispat.json and no commit: this command reads files.

	res := r.Command("scanner", "packages/web")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "package.json  npm  @acme/web@1.2.0")
	assert.Contains(t, res.Stdout, "@acme/core")
	assert.Contains(t, res.Stdout, "^1.2.0")
	assert.Contains(t, res.Stdout, "-> ../tsconfig", "a declared local path is part of the answer")
	assert.Contains(t, res.Stdout, "vendored/package.json", "the default walk descends")
	assert.NotContains(t, res.Stdout, "left-pad", "installed dependencies are never scanned")
	assert.Contains(t, res.Stdout, "2 manifest(s), 3 dependency declaration(s)")

	res = r.Command("scanner", "packages/web", "--root-only")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "@acme/web@1.2.0")
	assert.NotContains(t, res.Stdout, "vendored/package.json")
	assert.Contains(t, res.Stdout, "1 manifest(s)")

	// Invoked from inside the package folder, with no folder argument: --root
	// is where the user stands, and there is nothing to ascend to.
	res = r.CommandAt("packages/web", "scanner", "--root-only")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "@acme/web@1.2.0")

	assert.Equal(t, 1, r.Command("scanner", "nowhere").Code, "a folder that is not there is an error")
}

// TestManifestsScannerJSONEvents: --log-format json is the machine-readable
// contract of this command too — one event per manifest carrying the identity
// and every declaration, then a summary — which is what makes it pipeable in
// the same CI step as a release run.
func TestManifestsScannerJSONEvents(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("packages/web/package.json", webPackageJSON)

	res := r.Command("scanner", "packages/web", "--log-format", "json")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	ev := findEvent(t, res.Events, "manifest")
	assert.Equal(t, "package.json", ev.Str("path"))
	assert.Equal(t, "npm", ev.Str("ecosystem"))
	assert.Equal(t, "@acme/web", ev.Str("name"))
	assert.Equal(t, "1.2.0", ev.Str("version"))
	deps, ok := ev["deps"].([]any)
	require.True(t, ok, "the event carries the declarations: %v", ev)
	require.Len(t, deps, 3)
	first := deps[0].(map[string]any)
	assert.Equal(t, "dependencies", first["kind"], "the zero kind is spelled out")
	assert.Equal(t, "@acme/core", first["name"])

	summary := findEvent(t, res.Events, "scan complete")
	assert.Equal(t, float64(1), summary["manifests"])
	assert.Equal(t, float64(3), summary["dependencies"])
	assert.Equal(t, float64(0), summary["failed"])
}

// TestManifestsScannerStrictGatesBrokenManifests: the partial-result contract
// reaching the exit code. A manifest that fails to parse is reported while the
// healthy ones are still listed and the command succeeds; --strict is the CI
// spelling that refuses the same repository.
func TestManifestsScannerStrictGatesBrokenManifests(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("packages/web/package.json", webPackageJSON)
	r.WriteFile("packages/broken/package.json", "{ this is not json")

	res := r.Command("scanner", "--log-format", "json")
	require.Equal(t, 0, res.Code, "a broken manifest alone does not fail the command")
	assert.Contains(t, res.Stdout, "manifest failed to parse")
	assert.Contains(t, res.Stdout, "broken/package.json", "the report names the file")
	assert.Equal(t, "@acme/web", findEvent(t, res.Events, "manifest").Str("name"),
		"the manifests that parsed are still reported")
	assert.Equal(t, float64(1), findEvent(t, res.Events, "scan complete")["failed"])

	res = r.Command("scanner", "--strict")
	assert.Equal(t, 1, res.Code, "--strict turns the same repository into a failure")
	assert.Contains(t, res.Stdout, "@acme/web@1.2.0", "the partial result is still printed")
}

// TestManifestsWriterEditsInPlace: the writer command through the binary over
// two ecosystems at once — the batch a migration script actually runs. Only
// the version text being changed moves, every other byte of each file
// survives, and the run reports what it did.
func TestManifestsWriterEditsInPlace(t *testing.T) {
	r := harness.New(t)
	const goMod = "module github.com/acme/api\n\ngo 1.26\n\nrequire github.com/acme/core v1.2.0\n"
	r.WriteFile("packages/web/package.json", webPackageJSON)
	r.WriteFile("packages/api/go.mod", goMod)

	res := r.Command("writer", "packages/web/package.json", "packages/api/go.mod",
		"--set-version", "1.3.0",
		"--set", "@acme/core=^1.3.0",
		"--set", "devDependencies:typescript=~5.5.0",
		"--set", "github.com/acme/core=v1.3.0")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	web, err := os.ReadFile(r.Path("packages", "web", "package.json"))
	require.NoError(t, err)
	assert.Equal(t, strings.NewReplacer(
		`"version": "1.2.0"`, `"version": "1.3.0"`,
		`"@acme/core": "^1.2.0"`, `"@acme/core": "^1.3.0"`,
		`"typescript": "~5.4.0"`, `"typescript": "~5.5.0"`,
	).Replace(webPackageJSON), string(web),
		"nothing but the three version scalars may differ")

	api, err := os.ReadFile(r.Path("packages", "api", "go.mod"))
	require.NoError(t, err)
	assert.Equal(t, strings.Replace(goMod, "v1.2.0", "v1.3.0", 1), string(api),
		"go.mod has no own version to write, so only the require moved")

	assert.Contains(t, res.Stdout, "version written")
	assert.Contains(t, res.Stdout, "applied  dependencies  @acme/core  ^1.3.0")
	assert.Contains(t, res.Stdout, "applied  devDependencies  typescript  ~5.5.0")
	assert.Contains(t, res.Stdout, "2 manifest(s): 3 applied,")

	// Re-running the same edits converges: nothing left to change.
	res = r.Command("writer", "packages/web/package.json", "--set-version", "1.3.0",
		"--set", "@acme/core=^1.3.0", "--log-format", "json")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, "manifest unchanged", findEvent(t, res.Events, "manifest unchanged").Str("message"))
	assert.Equal(t, float64(0), findEvent(t, res.Events, "write complete")["applied"])
}

// TestManifestsWriterRedirects: --link manages the directive that points a
// dependency at a local folder, and an empty path removes it — the round trip
// a release does around publishing.
func TestManifestsWriterRedirects(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("packages/api/go.mod", "module github.com/acme/api\n\ngo 1.26\n\nrequire github.com/acme/core v1.2.0\n")
	gomod := r.Path("packages", "api", "go.mod")

	res := r.Command("writer", "packages/api/go.mod", "--link", "github.com/acme/core=../core")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	data, err := os.ReadFile(gomod)
	require.NoError(t, err)
	assert.Contains(t, string(data), "replace github.com/acme/core => ../core")
	assert.Contains(t, res.Stdout, "applied  link     github.com/acme/core  ../core")

	// The scanner reads back what the writer just wrote: the two halves agree
	// on the same file, which is the pair's whole contract.
	scan := r.Command("scanner", "packages/api", "--log-format", "json")
	require.Equal(t, 0, scan.Code, "stderr:\n%s", scan.Stderr)
	deps := findEvent(t, scan.Events, "manifest")["deps"].([]any)
	require.Len(t, deps, 1)
	assert.Equal(t, "../core", deps[0].(map[string]any)["localPath"])

	res = r.Command("writer", "packages/api/go.mod", "--link", "github.com/acme/core=")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	data, err = os.ReadFile(gomod)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "replace", "an empty path removes the directive")
	assert.Contains(t, res.Stdout, "(removed)")
}

// TestManifestsWriterOutcomesReachTheExitCode: the three outcomes pkg/writer
// separates, mapped onto process exit codes — the distinction a CI step
// depends on. Skipped never fails, Missing only fails under --strict, and a
// path no writer covers always does while the usable manifests in the same
// batch are still written.
func TestManifestsWriterOutcomesReachTheExitCode(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("packages/web/package.json", webPackageJSON)
	r.WriteFile("packages/web/notes.txt", "not a manifest\n")

	res := r.Command("writer", "packages/web/package.json", "--set", "nowhere=1.0.0")
	assert.Equal(t, 0, res.Code, "an edit the manifest does not declare is tolerated by default")
	assert.Contains(t, res.Stdout, "missing  dependencies  nowhere  1.0.0")

	res = r.Command("writer", "packages/web/package.json", "--set", "nowhere=1.0.0", "--strict")
	assert.Equal(t, 1, res.Code, "--strict refuses it")

	res = r.Command("writer", "packages/web/notes.txt", "packages/web/package.json", "--set-version", "2.0.0")
	assert.Equal(t, 1, res.Code, "a path no writer covers always fails the command")
	assert.Contains(t, res.Stderr+res.Stdout, "no writer for this manifest")
	data, err := os.ReadFile(r.Path("packages", "web", "package.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"version": "2.0.0"`,
		"the usable manifest of the batch was still written")

	assert.Equal(t, 2, r.Command("writer", "packages/web/package.json", "--set", "nope").Code,
		"a malformed edit spec is a usage error")
	assert.Equal(t, 2, r.Command("writer", "packages/web/package.json").Code,
		"a writer invocation with nothing to write is a usage error")
	assert.Equal(t, 2, r.Command("writer").Code, "and so is one with no manifest")
}

// TestManifestsCommandWordsKeepTheirScripts: the two new command words are
// reserved like every other one, so the bare `dispat scanner` is the command
// even in a repository whose config defines a script by that name, while the
// two-word `dispat run scanner` still reaches the script. This is the one
// interaction between the manifest commands and a real release config.
func TestManifestsCommandWordsKeepTheirScripts(t *testing.T) {
	r := harness.New(t)
	f := harness.BaseFile()
	f.Scripts = map[string]string{
		"scanner": "echo the script ran",
		"build":   "echo building",
		"publish": "echo publishing",
	}
	f.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish()},
	}
	r.WriteConfigModel(f)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/package.json", `{"name":"core","version":"0.0.0"}`)
	r.Commit("feat(core): first release")

	bare := r.Command("scanner", "packages/core", "--root-only")
	require.Equal(t, 0, bare.Code, "stderr:\n%s", bare.Stderr)
	assert.Contains(t, bare.Stdout, "package.json  npm  core@0.0.0",
		"the bare word is the command, not the script")
	assert.NotContains(t, bare.Stdout, "the script ran")

	script := r.RunScript("scanner")
	require.Equal(t, 0, script.Code, "stderr:\n%s", script.Stderr)
	assert.Contains(t, script.Stdout, "the script ran", "the two-word spelling still reaches the script")
}

// TestManifestsReplacerNeedsNoConfig: the replacer command over files that
// are not manifests at all, in a folder with no dispat config anywhere and no
// git history. Substitutions apply in the order they were given, every
// occurrence is replaced, and the paths resolve against --root.
func TestManifestsReplacerNeedsNoConfig(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("build.gradle", "implementation 'com.acme:core:1.2.0'\ntestImplementation 'com.acme:core:1.2.0'\n")
	r.WriteFile("docs/README.md", "Requires com.acme:core:1.2.0 and nothing else.\n")

	res := r.Command("replacer", "--sub", "com.acme:core:1.2.0=>com.acme:core:1.3.0",
		"build.gradle", "docs/README.md")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "2 file(s), 3 occurrence(s): 2 applied, 0 skipped, 0 missing")

	gradle, err := os.ReadFile(r.Path("build.gradle"))
	require.NoError(t, err)
	assert.Equal(t, "implementation 'com.acme:core:1.3.0'\ntestImplementation 'com.acme:core:1.3.0'\n", string(gradle))
	readme, err := os.ReadFile(r.Path("docs", "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "Requires com.acme:core:1.3.0 and nothing else.\n", string(readme))

	// Repeated --sub values apply in order, each over what the last left.
	res = r.Command("replacer", "--sub", "1.3.0=>1.4.0", "--sub", "1.4.0=>2.0.0", "build.gradle")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	gradle, err = os.ReadFile(r.Path("build.gradle"))
	require.NoError(t, err)
	assert.Contains(t, string(gradle), "com.acme:core:2.0.0")
}

// TestManifestsReplacerOutcomesReachTheExitCode: the three ways the command
// ends, over a process boundary.
func TestManifestsReplacerOutcomesReachTheExitCode(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("notes.txt", "version 1.0.0\n")

	// Nothing to write is a usage error.
	assert.Equal(t, 2, r.Command("replacer", "notes.txt").Code)
	// So is a spec with no separator.
	assert.Equal(t, 2, r.Command("replacer", "--sub", "no-separator", "notes.txt").Code)
	// No file to work on is a usage error too.
	assert.Equal(t, 2, r.Command("replacer", "--sub", "a=>b").Code)

	// A pattern matching nothing is quiet by default and fatal under --strict.
	assert.Equal(t, 0, r.Command("replacer", "--sub", "absent=>x", "notes.txt").Code)
	strict := r.Command("replacer", "--strict", "--sub", "absent=>x", "notes.txt")
	assert.Equal(t, 1, strict.Code)
	assert.Contains(t, strict.Stdout+strict.Stderr, "matched nothing")

	// An unreadable file fails the command; the others are still attempted.
	r.WriteFile("keep.txt", "version 1.0.0\n")
	failed := r.Command("replacer", "--sub", "1.0.0=>1.1.0", "absent.txt", "keep.txt")
	assert.Equal(t, 1, failed.Code)
	kept, err := os.ReadFile(r.Path("keep.txt"))
	require.NoError(t, err)
	assert.Equal(t, "version 1.1.0\n", string(kept), "one bad path must not cost the others their edits")
}

// TestManifestsReplacerJSONEvents: the machine-readable rendering, one event
// per file plus a summary, on the same stream CI already ingests.
func TestManifestsReplacerJSONEvents(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("notes.txt", "keep 1.0.0 keep\n")

	res := r.Command("replacer", "--log-format", "json",
		"--sub", "1.0.0=>1.1.0", "--sub", "absent=>x", "--sub", "keep=>keep", "notes.txt")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	file := findEvent(t, res.Events, "file updated")
	assert.Equal(t, "notes.txt", file.Str("path"))
	summary := findEvent(t, res.Events, "substitution complete")
	for _, key := range []string{"occurrences", "applied", "missing", "skipped"} {
		assert.Equal(t, float64(1), summary[key], "%s in %v", key, summary)
	}
}

// TestManifestsReplacerWordKeepsItsScript: the command word is reserved like
// every other one, and the two-word spelling still reaches a run script of
// the same name.
func TestManifestsReplacerWordKeepsItsScript(t *testing.T) {
	r := harness.New(t)
	f := harness.BaseFile()
	f.Scripts = map[string]string{
		"replacer": "echo the script ran",
		"build":    "echo building",
		"publish":  "echo publishing",
	}
	f.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish()},
	}
	r.WriteConfigModel(f)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/notes.txt", "version 1.0.0\n")
	r.Commit("feat(core): first release")

	bare := r.Command("replacer", "--sub", "1.0.0=>1.1.0", "packages/core/notes.txt")
	require.Equal(t, 0, bare.Code, "stderr:\n%s", bare.Stderr)
	assert.NotContains(t, bare.Stdout, "the script ran", "the bare word is the command")
	data, err := os.ReadFile(r.Path("packages", "core", "notes.txt"))
	require.NoError(t, err)
	assert.Equal(t, "version 1.1.0\n", string(data))

	script := r.RunScript("replacer")
	require.Equal(t, 0, script.Code, "stderr:\n%s", script.Stderr)
	assert.Contains(t, script.Stdout, "the script ran", "the two-word spelling still reaches the script")
}
