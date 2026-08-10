package cli

// Unit tests of the command-line controller itself: flag and argument
// parsing, usage and exit-code mapping, the init command (which needs no
// config, only a .git entry marking the repository root — faked here with a
// bare directory, so these stay unit tests), and the logger constructor.
// Everything the controller only
// composes — release flows, hooks, records, the run and preview commands
// against real repositories — is pinned by the black-box suite in
// tests/integration, driving the compiled binary.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/manifest"
	"github.com/yohimik/dispat/pkg/writer"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

func TestVersionFlag(t *testing.T) {
	// --version answers before anything else — no config file is read, so it
	// works outside a monorepo. The output is the terminal logo followed by
	// the version line; the default "dev" marks a local build, releases
	// override Version at build time from the release tag.
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--version"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Equal(t, logo+"\n\ndispat dev\n", stdout.String())
	assert.Empty(t, stderr.String())

	old := Version
	t.Cleanup(func() { Version = old })
	Version = "1.2.3"
	stdout.Reset()
	code = Run([]string{"--version"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Equal(t, logo+"\n\ndispat 1.2.3\n", stdout.String())
}

func TestHelpFlag(t *testing.T) {
	// --help exits 0 (asking for help is not an error) after the usage text,
	// and needs no config file or repository.
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--help"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr.String(), "usage: dispat")
	assert.Contains(t, stderr.String(), "flags:")
}

func TestUnknownFlagPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--nope"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "unknown flag", "the parse error must be reported, not swallowed")
	assert.Contains(t, stderr.String(), "usage: dispat")
}

func TestCommandArityIsAUsageError(t *testing.T) {
	// Wrong argument counts are rejected before any config or git is touched,
	// so none of these needs a repository.
	root := t.TempDir()
	for _, args := range [][]string{
		{"run"},                               // run requires the script name
		{"run", "a", "b"},                     // ...and nothing else: packages are flags
		{"preview", "a"},                      // preview takes no arguments either
		{"status", "extra"},                   // status takes no arguments
		{"bogus", "extra"},                    // more than one non-command word
		{"run", "x", "--on-error", "explode"}, // unknown --on-error value
		{"changelog", "a"},                    // nor do the step commands
		{"autoversion", "a"},
		{"commit", "a"},
		{"scanner", "a", "b"},                     // scanner takes at most one folder
		{"writer"},                                // writer needs at least one manifest
		{"writer", "go.mod"},                      // ...and something to write
		{"writer", "go.mod", "--set", "nope"},     // a malformed edit spec
		{"writer", "go.mod", "--replace", "nope"}, // ...and a malformed replacement
	} {
		var stdout, stderr bytes.Buffer
		code := Run(append(args, "--root", root), &stdout, &stderr)
		assert.Equal(t, 2, code, "args: %v", args)
	}
}

func TestFilterFlagsReachEveryPackageCommand(t *testing.T) {
	// The selection flags parse for all six commands that take them, in both
	// spellings, and compose with the window flags they used to be rejected
	// alongside. Exit 1 (config not found in this bare folder) rather than 2
	// is what proves the command line itself was accepted.
	root := t.TempDir()
	for _, args := range [][]string{
		{"run", "x", "--package", "core", "--space", "libs"},
		{"run", "x", "-p", "core,web", "-s", "libs"},
		{"run", "x", "-p", "core", "--since", "HEAD~1", "--consumers"},
		{"run", "x", "-p", "*"},
		{"preview", "-p", "core"},
		{"changelog", "--space", "libs"},
		{"autoversion", "-p", "core"},
		{"commit", "-s", "libs"},
		{"compute", "-p", "core"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(append(args, "--root", root), &stdout, &stderr)
		assert.Equal(t, 1, code, "args: %v — stderr: %s", args, stderr.String())
	}
}

func TestSinceHasNoShorthand(t *testing.T) {
	// -s belongs to --space, so the two selection flags own the two
	// shorthands. A `-s` value is a space term and reaches config loading
	// (exit 1); nothing silently reads it as a git revision.
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	assert.Equal(t, 1, Run([]string{"run", "x", "-s", "HEAD~1", "--root", root}, &stdout, &stderr))
	assert.NotContains(t, stderr.String(), "unknown flag")
}

func TestInvalidConfig(t *testing.T) {
	root := t.TempDir() // no config file at all
	var stdout, stderr bytes.Buffer
	code := Run([]string{"status", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "no dispat config file found",
		"the not-found error must be reported, naming what was tried")
}

func TestInitCommand(t *testing.T) {
	// dispat init writes a starter config that must itself load — a generated
	// config nobody can run is worse than none — in each supported format.
	for _, format := range []string{"json", "yaml", "toml"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
			args := []string{"init", "--root", root}
			if format != "json" {
				args = append(args, "--format", format)
			}
			var stdout, stderr bytes.Buffer
			code := Run(args, &stdout, &stderr)
			require.Equal(t, 0, code, "stderr: %s", stderr.String())
			assert.Contains(t, stdout.String(), "created dispat."+format)

			file := filepath.Join(root, "dispat."+format)
			require.FileExists(t, file)
			_, err := config.Load(file, nil)
			require.NoError(t, err, "the starter config must load as written")
		})
	}
}

func TestInitCommandRefusesToOverwrite(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), []byte("{}"), 0o644))
	var stdout, stderr bytes.Buffer
	code := Run([]string{"init", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 1, code, "an existing config must never be overwritten")
	data, err := os.ReadFile(filepath.Join(root, "dispat.json"))
	require.NoError(t, err)
	assert.Equal(t, "{}", string(data), "the existing file must be untouched")

	code = Run([]string{"init", "--root", root, "--format", "ini"}, &stdout, &stderr)
	assert.Equal(t, 1, code, "an unknown format is an error")
}

func TestInitCommandRequiresAGitRepositoryRoot(t *testing.T) {
	// The config establishes the effective monorepo root for every later
	// command, so init refuses to plant one outside a git repository root.
	root := t.TempDir() // no .git
	var stdout, stderr bytes.Buffer
	code := Run([]string{"init", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.NoFileExists(t, filepath.Join(root, "dispat.json"), "nothing may be written")
}

func TestNewLoggerFallsBackOnUnknownLevel(t *testing.T) {
	// Config validation makes an unknown level unreachable through Run; the
	// constructor still degrades to info rather than panicking or silencing.
	var buf bytes.Buffer
	log := newLogger("not-a-level", "json", &buf)
	log.Info().Msg("visible")
	log.Debug().Msg("hidden")
	assert.Contains(t, buf.String(), "visible")
	assert.NotContains(t, buf.String(), "hidden", "the fallback level is info")
}

func TestStepCommandWordsAreNotRunScripts(t *testing.T) {
	// The step command words are reserved: `dispat commit` parses as the
	// commit command (which reaches config loading and fails there in this
	// bare folder), never as `dispat run commit` (whose two-word spelling
	// stays available). The bare-word case exiting 1, not 2, is what proves
	// the word was taken as a command.
	root := t.TempDir()
	for _, word := range []string{"changelog", "autoversion", "commit"} {
		var stdout, stderr bytes.Buffer
		code := Run([]string{word, "--root", root}, &stdout, &stderr)
		assert.Equal(t, 1, code, "%s must parse as a command, not a run script", word)
	}
}

func TestManifestCommandWordsAreNotRunScripts(t *testing.T) {
	// The manifest command words are reserved like every other one, but they
	// prove it differently: needing no config file, they run to completion in
	// a bare folder. `dispat scanner` scans it and exits 0 (a run script by
	// that name would have failed at config loading), and `dispat writer`
	// fails arity at 2 rather than reaching a config at all.
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	assert.Equal(t, 0, Run([]string{"scanner", "--root", root}, &stdout, &stderr))
	assert.Contains(t, stdout.String(), "0 manifest(s)")
	assert.Equal(t, 2, Run([]string{"writer", "--root", root}, &stdout, &stderr))
}

func TestParseEditSpec(t *testing.T) {
	// The command-line spelling of an edit. Only the four kind words are read
	// as a kind prefix, so a Maven coordinate keeps its colon, and the range
	// starts after the first "=" so a PEP 508 range keeps its own.
	for _, tc := range []struct {
		spec string
		want writer.Edit
	}{
		{"acme=1.2.3", writer.Edit{Name: "acme", Kind: manifest.KindDependencies, Range: "1.2.3"}},
		{"dependencies:acme=1.2.3", writer.Edit{Name: "acme", Kind: manifest.KindDependencies, Range: "1.2.3"}},
		{"devDependencies:acme=^1.2.3", writer.Edit{Name: "acme", Kind: manifest.KindDevDependencies, Range: "^1.2.3"}},
		{"peerDependencies:acme=1", writer.Edit{Name: "acme", Kind: manifest.KindPeerDependencies, Range: "1"}},
		{"optionalDependencies:acme=1", writer.Edit{Name: "acme", Kind: manifest.KindOptionalDependencies, Range: "1"}},
		// A Maven coordinate: the prefix is not a kind word, so the whole
		// thing stays the name.
		{"com.acme:core=1.2.3", writer.Edit{Name: "com.acme:core", Kind: manifest.KindDependencies, Range: "1.2.3"}},
		// A scoped npm name, and a range carrying its own "=".
		{"@acme/core=workspace:*", writer.Edit{Name: "@acme/core", Kind: manifest.KindDependencies, Range: "workspace:*"}},
		{"requests=>=1.0,<2.0", writer.Edit{Name: "requests", Kind: manifest.KindDependencies, Range: ">=1.0,<2.0"}},
		{"devDependencies:com.acme:core=1", writer.Edit{Name: "com.acme:core", Kind: manifest.KindDevDependencies, Range: "1"}},
	} {
		got, err := parseEditSpec(tc.spec)
		require.NoError(t, err, "spec: %s", tc.spec)
		assert.Equal(t, tc.want, got, "spec: %s", tc.spec)
	}

	for _, spec := range []string{
		"acme",             // no range at all
		"acme=",            // an empty range writes nothing
		"=1.2.3",           // no name
		"devDependencies:", // a kind and nothing else
		"",
	} {
		_, err := parseEditSpec(spec)
		assert.Error(t, err, "spec %q must be rejected", spec)
	}
}

func TestParseReplaceSpec(t *testing.T) {
	// An empty path is the documented removal, so it is the one "empty" half
	// a replacement spec accepts.
	got, err := parseReplaceSpec("github.com/acme/core=../core")
	require.NoError(t, err)
	assert.Equal(t, writer.Replacement{Name: "github.com/acme/core", Path: "../core"}, got)

	got, err = parseReplaceSpec("github.com/acme/core=")
	require.NoError(t, err)
	assert.Equal(t, writer.Replacement{Name: "github.com/acme/core"}, got)

	for _, spec := range []string{"github.com/acme/core", "=../core", ""} {
		_, err := parseReplaceSpec(spec)
		assert.Error(t, err, "spec %q must be rejected", spec)
	}
}

func TestTestIsAnOrdinaryScriptWord(t *testing.T) {
	// "test" is not a command word, so `dispat test` is `dispat run test` like
	// any other bare word: it reaches config loading (exit 1 here) rather than
	// failing arity as a command would have.
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	assert.Equal(t, 1, Run([]string{"test", "--root", root}, &stdout, &stderr))
}
