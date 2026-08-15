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

// TestManifestsScannerDebugEventsAndDroppedEntries: --log-level debug narrates
// the scan (where it starts, what each manifest held) and a declared entry the
// parser could not read surfaces in the manifest event's dropped array. At the
// default level the narration stays out of the stream.
func TestManifestsScannerDebugEventsAndDroppedEntries(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("compose.yaml", "services:\n  broken:\n  api:\n    build: .\n    image: ghcr.io/acme/api:1.0.0\n")

	res := r.Command("scanner", "--log-format", "json", "--log-level", "debug")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	scanning := findEvent(t, res.Events, "scanning")
	assert.Equal(t, "debug", scanning.Str("level"))
	dropped := findEvent(t, res.Events, "declaration dropped")
	assert.Equal(t, "compose.yaml", dropped.Str("manifest"))
	assert.Equal(t, "service broken: not a mapping", dropped.Str("entry"))
	ev := findEvent(t, res.Events, "manifest")
	entries, ok := ev["dropped"].([]any)
	require.True(t, ok, "the manifest event names what it dropped: %v", ev)
	assert.Equal(t, []any{"service broken: not a mapping"}, entries)

	res = r.Command("scanner", "--log-format", "json")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	for _, e := range res.Events {
		assert.NotEqual(t, "scanning", e.Str("message"), "debug narration stays out of the default level")
	}
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
	f.Scripts = map[string]models.Script{
		"scanner": {"echo the script ran"},
		"build":   {"echo building"},
		"publish": {"echo publishing"},
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
// git history. Replacements apply in the order they were given, every
// occurrence is replaced, and the paths resolve against --root.
func TestManifestsReplacerNeedsNoConfig(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("build.gradle", "implementation 'com.acme:core:1.2.0'\ntestImplementation 'com.acme:core:1.2.0'\n")
	r.WriteFile("docs/README.md", "Requires com.acme:core:1.2.0 and nothing else.\n")

	res := r.Command("replacer", "--replace", "com.acme:core:1.2.0=>com.acme:core:1.3.0",
		"build.gradle", "docs/README.md")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "2 file(s), 3 occurrence(s): 2 applied, 0 skipped, 0 missing")

	gradle, err := os.ReadFile(r.Path("build.gradle"))
	require.NoError(t, err)
	assert.Equal(t, "implementation 'com.acme:core:1.3.0'\ntestImplementation 'com.acme:core:1.3.0'\n", string(gradle))
	readme, err := os.ReadFile(r.Path("docs", "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "Requires com.acme:core:1.3.0 and nothing else.\n", string(readme))

	// Repeated --rep values apply in order, each over what the last left.
	res = r.Command("replacer", "--replace", "1.3.0=>1.4.0", "--replace", "1.4.0=>2.0.0", "build.gradle")
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
	assert.Equal(t, 2, r.Command("replacer", "--replace", "no-separator", "notes.txt").Code)
	// No file to work on is a usage error too.
	assert.Equal(t, 2, r.Command("replacer", "--replace", "a=>b").Code)

	// A pattern matching nothing is quiet by default and fatal under --strict.
	assert.Equal(t, 0, r.Command("replacer", "--replace", "absent=>x", "notes.txt").Code)
	strict := r.Command("replacer", "--strict", "--replace", "absent=>x", "notes.txt")
	assert.Equal(t, 1, strict.Code)
	assert.Contains(t, strict.Stdout+strict.Stderr, "matched nothing")

	// An unreadable file fails the command; the others are still attempted.
	r.WriteFile("keep.txt", "version 1.0.0\n")
	failed := r.Command("replacer", "--replace", "1.0.0=>1.1.0", "absent.txt", "keep.txt")
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
		"--replace", "1.0.0=>1.1.0", "--replace", "absent=>x", "--replace", "keep=>keep", "notes.txt")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	file := findEvent(t, res.Events, "file updated")
	assert.Equal(t, "notes.txt", file.Str("path"))
	summary := findEvent(t, res.Events, "replace complete")
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
	f.Scripts = map[string]models.Script{
		"replacer": {"echo the script ran"},
		"build":    {"echo building"},
		"publish":  {"echo publishing"},
	}
	f.Spaces = map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: buildPublish()},
	}
	r.WriteConfigModel(f)
	r.SeedPackage("packages", "core")
	r.WriteFile("packages/core/notes.txt", "version 1.0.0\n")
	r.Commit("feat(core): first release")

	bare := r.Command("replacer", "--replace", "1.0.0=>1.1.0", "packages/core/notes.txt")
	require.Equal(t, 0, bare.Code, "stderr:\n%s", bare.Stderr)
	assert.NotContains(t, bare.Stdout, "the script ran", "the bare word is the command")
	data, err := os.ReadFile(r.Path("packages", "core", "notes.txt"))
	require.NoError(t, err)
	assert.Equal(t, "version 1.1.0\n", string(data))

	script := r.RunScript("replacer")
	require.Equal(t, 0, script.Code, "stderr:\n%s", script.Stderr)
	assert.Contains(t, script.Stdout, "the script ran", "the two-word spelling still reaches the script")
}

// TestManifestsScannerVerifyGates: the two link gates, each a separate flag in
// one direction. A clean tree passes --verify-unlinked and fails
// --verify-linked; a tree carrying a go.mod replace (block form, which a
// line-based grep would miss) does the opposite. A dependency declared with a
// file: range is a declaration rather than an injected directive, so the gate
// leaves it alone. Asking for both gates at once is a usage error.
func TestManifestsScannerVerifyGates(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("packages/web/package.json", webPackageJSON)
	r.WriteFile("go.mod", "module example.com/m\n\ngo 1.25.0\n\nrequire example.com/dep v1.0.0\n")

	clean := r.Command("scanner", "--log-format", "json", "--verify-unlinked")
	assert.Equal(t, 0, clean.Code, "stderr:\n%s", clean.Stderr)

	linked := r.Command("scanner", "--log-format", "json", "--verify-linked")
	assert.Equal(t, 1, linked.Code, "a clean tree fails the inverse gate")
	absent := findEvent(t, linked.Events, "no local link present")
	assert.Equal(t, "E216", absent.Code())

	r.WriteFile("go.mod", "module example.com/m\n\ngo 1.25.0\n\nrequire example.com/dep v1.0.0\n\nreplace (\n\texample.com/dep => ../dep\n)\n")
	dirty := r.Command("scanner", "--log-format", "json", "--verify-unlinked")
	assert.Equal(t, 1, dirty.Code, "a surviving link fails the gate")
	present := findEvent(t, dirty.Events, "local link present")
	assert.Equal(t, "E215", present.Code())
	assert.Equal(t, "go.mod", present.Str("manifest"))
	assert.Equal(t, "example.com/dep", present.Str("dependency"))
	assert.Equal(t, "../dep", present.Str("path"))
	assert.Equal(t, 0, r.Command("scanner", "--log-format", "json", "--verify-linked").Code,
		"the same tree passes the inverse gate")

	assert.Equal(t, 2, r.Command("scanner", "--verify-unlinked", "--verify-linked").Code,
		"the two gates assert opposite states and cannot be asked together")
}

// TestManifestsWriterDropLinks: --drop-links sweeps every directive out of the
// named manifests without being told the names, across formats, and the
// verify gate confirms the result.
func TestManifestsWriterDropLinks(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("go.mod", "module example.com/m\n\ngo 1.25.0\n\nrequire example.com/dep v1.0.0\n\nreplace example.com/dep => ../dep\n")
	r.WriteFile("Cargo.toml", "[package]\nname = \"acme\"\n\n[dependencies]\ncore = \"1.0\"\n\n[patch.crates-io]\ncore = { path = \"../core\" }\n")
	r.WriteFile("pubspec.yaml", "name: acme\ndependencies:\n  core: ^1.0.0\n\ndependency_overrides:\n  core:\n    path: ../core\n")

	res := r.Command("writer", "--log-format", "json", "--drop-links",
		"go.mod", "Cargo.toml", "pubspec.yaml")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	summary := findEvent(t, res.Events, "write complete")
	assert.Equal(t, float64(3), summary["applied"], "one drop per manifest: %v", summary)

	verify := r.Command("scanner", "--verify-unlinked")
	assert.Equal(t, 0, verify.Code, "after the sweep the gate passes")

	again := r.Command("writer", "--drop-links", "go.mod", "Cargo.toml", "pubspec.yaml")
	assert.Equal(t, 0, again.Code, "a second sweep has nothing to do and says so quietly")

	assert.Equal(t, 2, r.Command("writer", "--drop-links", "--link", "core=../core", "go.mod").Code,
		"placing and sweeping redirects in one invocation is a usage error")
}

// TestManifestsWriterLinkDropVerifyCycle: the whole bracket through the
// commands alone. A link is placed, --verify-linked proves it landed, the
// sweep removes it, --verify-unlinked proves it is gone.
func TestManifestsWriterLinkDropVerifyCycle(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("go.mod", "module example.com/m\n\ngo 1.25.0\n\nrequire example.com/dep v1.0.0\n")

	require.Equal(t, 0, r.Command("writer", "--link", "example.com/dep=../dep", "go.mod").Code)
	assert.Equal(t, 0, r.Command("scanner", "--verify-linked").Code, "the link landed")
	require.Equal(t, 0, r.Command("writer", "--drop-links", "go.mod").Code)
	assert.Equal(t, 0, r.Command("scanner", "--verify-unlinked").Code, "the link is gone")
}

// TestManifestsScannerRangeGates: the range gates are independent of the link
// gates and of each other. --forbid-range fails for every matching declared
// range; --require-range fails when nothing matches; the same pattern in both
// is a usage error; a tree carrying links but no forbidden ranges passes the
// range gate while failing the link gate.
func TestManifestsScannerRangeGates(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("packages/web/package.json", `{
  "name": "@acme/web",
  "version": "1.0.0",
  "dependencies": { "@acme/core": "workspace:*", "left-pad": "^1.0.0" }
}
`)

	forbid := r.Command("scanner", "--log-format", "json", "--forbid-range", "workspace:*")
	assert.Equal(t, 1, forbid.Code, "a workspace range on the way out fails the gate")
	hit := findEvent(t, forbid.Events, "forbidden range")
	assert.Equal(t, "E217", hit.Code())
	assert.Equal(t, "@acme/core", hit.Str("dependency"))
	assert.Equal(t, "workspace:*", hit.Str("pattern"))

	assert.Equal(t, 0, r.Command("scanner", "--require-range", "workspace:*").Code,
		"the same tree is exactly what the dev-state gate wants")

	require.Equal(t, 0, r.Command("writer", "--set", "@acme/core=^1.1.0", "packages/web/package.json").Code)
	assert.Equal(t, 0, r.Command("scanner", "--forbid-range", "workspace:*").Code,
		"after the rewrite the forbid gate passes")
	missing := r.Command("scanner", "--log-format", "json", "--require-range", "workspace:*")
	assert.Equal(t, 1, missing.Code)
	gone := findEvent(t, missing.Events, "required range missing")
	assert.Equal(t, "E218", gone.Code())

	// Links and ranges are unrelated checks: a linked go.mod fails the link
	// gate while both range gates keep their own answers.
	r.WriteFile("go.mod", "module example.com/m\n\ngo 1.25.0\n\nrequire example.com/dep v1.0.0\n\nreplace example.com/dep => ../dep\n")
	assert.Equal(t, 0, r.Command("scanner", "--forbid-range", "workspace:*").Code)
	assert.Equal(t, 1, r.Command("scanner", "--verify-unlinked").Code)

	assert.Equal(t, 2, r.Command("scanner", "--forbid-range", "workspace:*", "--require-range", "workspace:*").Code,
		"one pattern cannot be forbidden and required at once")
}

// TestManifestsWriterSetBuild: --set-build writes the counter each mobile
// format keeps, and only the counter; versions stay untouched. The pubspec
// spelling is the + suffix on the version scalar.
func TestManifestsWriterSetBuild(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("Info.plist", "<plist><dict>\n<key>CFBundleShortVersionString</key><string>1.2.0</string>\n<key>CFBundleVersion</key><string>41</string>\n</dict></plist>")
	r.WriteFile("AndroidManifest.xml", `<manifest package="com.acme.app" android:versionName="1.2.0" android:versionCode="41"/>`)
	r.WriteFile("build.gradle", "android {\n  defaultConfig {\n    versionCode 41\n    versionName \"1.2.0\"\n  }\n}\n")
	r.WriteFile("pubspec.yaml", "name: acme\nversion: 1.2.0\n")

	res := r.Command("writer", "--log-format", "json", "--set-build", "42",
		"Info.plist", "AndroidManifest.xml", "build.gradle", "pubspec.yaml")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	updated := 0
	for _, e := range res.Events {
		if e.Str("message") == "manifest updated" {
			updated++
			assert.Equal(t, true, e["buildWritten"], "the event says what moved: %v", e)
			assert.Equal(t, false, e["versionWritten"], "the version is not --set-build's to touch: %v", e)
		}
	}
	assert.Equal(t, 4, updated)

	for file, want := range map[string]string{
		"Info.plist":          "<key>CFBundleVersion</key><string>42</string>",
		"AndroidManifest.xml": `android:versionCode="42"`,
		"build.gradle":        "versionCode 42",
		"pubspec.yaml":        "version: 1.2.0+42",
	} {
		data, err := os.ReadFile(r.Path(file))
		require.NoError(t, err)
		assert.Contains(t, string(data), want, file)
		assert.Contains(t, string(data), "1.2.0", "%s keeps its version", file)
	}

	scan := r.Command("scanner", "--log-format", "json", "--root-only")
	require.Equal(t, 0, scan.Code)
	for _, e := range scan.Events {
		if e.Str("message") == "manifest" && e.Str("path") == "pubspec.yaml" {
			assert.Equal(t, "42", e.Str("buildNumber"), "the scanner reads the counter back")
		}
	}

	failed := r.Command("writer", "--set-build", "banana", "AndroidManifest.xml")
	assert.Equal(t, 1, failed.Code, "a word where an integer is required is refused")
}
