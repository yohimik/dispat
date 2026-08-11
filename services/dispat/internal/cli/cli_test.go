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
	"runtime"
	"testing"

	"github.com/spf13/pflag"
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
	platform := "(" + runtime.GOOS + "_" + runtime.GOARCH + ")"
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--version"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Equal(t, logo+"\n\ndispat dev "+platform+"\n", stdout.String())
	assert.Empty(t, stderr.String())

	old := Version
	t.Cleanup(func() { Version = old })
	Version = "1.2.3"
	stdout.Reset()
	code = Run([]string{"--version"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Equal(t, logo+"\n\ndispat 1.2.3 "+platform+"\n", stdout.String(),
		"the platform says which of the release's binaries is running")
}

func TestHelpFlag(t *testing.T) {
	// --help exits 0 (asking for help is not an error) after the usage text,
	// and needs no config file or repository. Without a command word it is
	// the program help: every command, and the global flags only.
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--help"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	out := stderr.String()
	assert.Contains(t, out, "usage: dispat [command] [flags]")
	assert.Contains(t, out, "global flags:")
	for _, c := range commands {
		assert.Contains(t, out, c.short, "every command is listed")
	}
	assert.NotContains(t, out, "--set-version", "a command's own flags are one --help away")
	assert.Contains(t, out, `run "dispat <command> --help"`)
}

func TestHelpIsScopedToTheCommand(t *testing.T) {
	// The complaint this fixes: `dispat run --help` used to print the whole
	// program's help, because pflag intercepts an undeclared --help during
	// Parse, before the command word is read.
	for name, tc := range map[string]struct {
		args        []string
		usage       string
		has, hasNot []string
	}{
		"run": {
			args: []string{"run", "--help"}, usage: "usage: dispat run <script> [flags]",
			has:    []string{"--on-error", "--since", "--consumers", "--package"},
			hasNot: []string{"--set-version", "--tag", "--sub", "--interactive"},
		},
		"commit": {
			args: []string{"commit", "--help"}, usage: "usage: dispat commit [flags]",
			has:    []string{"--tag", "--push", "--message-format", "--space"},
			hasNot: []string{"--on-error", "--set-version"},
		},
		"github": {
			args: []string{"github", "--help"}, usage: "usage: dispat github [flags]",
			has:    []string{"--owner", "--repo", "--api-url", "--token-env", "--target"},
			hasNot: []string{"--tag", "--file"},
		},
		"writer, which would otherwise fail arity first": {
			args: []string{"writer", "--help"}, usage: "usage: dispat writer <manifest>... [flags]",
			has:    []string{"--set-version", "--set", "--replace", "--strict"},
			hasNot: []string{"--package", "--tag"},
		},
		"the run shorthand": {
			args: []string{"lint", "--help"}, usage: "usage: dispat run <script> [flags]",
			has:    []string{"--on-error"},
			hasNot: []string{"--set-version"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			assert.Equal(t, 0, Run(tc.args, &stdout, &stderr))
			out := stderr.String()
			assert.Contains(t, out, tc.usage)
			assert.Contains(t, out, "global flags:")
			for _, want := range tc.has {
				assert.Contains(t, out, want)
			}
			for _, unwanted := range tc.hasNot {
				assert.NotContains(t, out, unwanted, "another command's flag leaked in")
			}
		})
	}
}

func TestCommandUsageFallsBackToTheProgramHelp(t *testing.T) {
	// A word the table does not know has no help of its own; printing
	// nothing would be worse than printing the command list.
	var buf bytes.Buffer
	fs := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
	declareFlags(fs)
	printCommandUsage(&buf, fs, "nonsense")
	assert.Contains(t, buf.String(), "usage: dispat [command] [flags]")
}

func TestEveryFlagIsClaimedByACommand(t *testing.T) {
	// The drift guard: a flag added to the set but to no command's entry
	// would be invisible in every help rendering, and the command table is
	// the only place that mapping lives, so nothing else can catch it.
	claimed := make(map[string]bool, len(globalFlags))
	for _, n := range globalFlags {
		claimed[n] = true
	}
	for _, c := range commands {
		for _, n := range c.flags {
			claimed[n] = true
		}
	}
	fs := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
	declareFlags(fs)
	fs.VisitAll(func(f *pflag.Flag) {
		assert.True(t, claimed[f.Name], "flag --%s belongs to no command and to no global set", f.Name)
	})
	// ...and the other way: a table naming a flag the set does not declare
	// would render nothing, silently.
	for _, c := range commands {
		for _, n := range append(append([]string{}, c.flags...), globalFlags...) {
			assert.NotNil(t, fs.Lookup(n), "command %q names --%s, which is declared nowhere", c.name, n)
		}
	}
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
		{"github", "a"},
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
	// The selection flags parse for all seven commands that take them, in both
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
		{"github", "-p", "core"},
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
	for _, word := range []string{"changelog", "autoversion", "commit", "github"} {
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
	assert.Equal(t, 2, Run([]string{"replacer", "--root", root}, &stdout, &stderr))
}

func TestReplacerCommand(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	require.NoError(t, os.WriteFile(path, []byte("acme-core:1.0.0 and acme-core:1.0.0\n"), 0o644))

	// No config file and no git repository: the replacer only ever looks at
	// the files it is pointed at.
	var stdout, stderr bytes.Buffer
	code := Run([]string{"replacer", "--root", root, "--sub", "acme-core:1.0.0=>acme-core:1.1.0", "README.md"},
		&stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "2 occurrence(s) replaced")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "acme-core:1.1.0 and acme-core:1.1.0\n", string(data))

	// A pattern matching nothing is quiet by default and fatal under --strict.
	stdout.Reset()
	stderr.Reset()
	assert.Equal(t, 0, Run([]string{"replacer", "--root", root, "--sub", "absent=>x", "README.md"},
		&stdout, &stderr))
	assert.Equal(t, 1, Run([]string{"replacer", "--root", root, "--strict", "--sub", "absent=>x", "README.md"},
		&stdout, &stderr))

	// Nothing to write is a usage error, as it is for the writer.
	assert.Equal(t, 2, Run([]string{"replacer", "--root", root, "README.md"}, &stdout, &stderr))
	// So is a malformed substitution.
	assert.Equal(t, 2, Run([]string{"replacer", "--root", root, "--sub", "no-separator", "README.md"},
		&stdout, &stderr))
	// A file that is not there fails the command.
	assert.Equal(t, 1, Run([]string{"replacer", "--root", root, "--sub", "a=>b", "absent.md"},
		&stdout, &stderr))
}

func TestParseSubSpec(t *testing.T) {
	// The split is at the first "=>", so an "=" on either side survives and a
	// "=>" in the replacement text does too.
	for _, tc := range []struct {
		spec string
		want writer.Substitution
	}{
		{"1.0.0=>1.1.0", writer.Substitution{Find: "1.0.0", Write: "1.1.0"}},
		{"VERSION=1.0.0=>VERSION=1.1.0", writer.Substitution{Find: "VERSION=1.0.0", Write: "VERSION=1.1.0"}},
		{"a=>b=>c", writer.Substitution{Find: "a", Write: "b=>c"}},
		// An empty replacement deletes what it finds.
		{"drop-me=>", writer.Substitution{Find: "drop-me"}},
		{" spaced =>  padded ", writer.Substitution{Find: " spaced ", Write: "  padded "}},
	} {
		got, err := parseSubSpec(tc.spec)
		require.NoError(t, err, "spec: %s", tc.spec)
		assert.Equal(t, tc.want, got, "spec: %s", tc.spec)
	}

	for _, spec := range []string{
		"no separator",
		"=>only-a-replacement", // an empty find matches everywhere
		"",
	} {
		_, err := parseSubSpec(spec)
		assert.Error(t, err, "spec %q must be rejected", spec)
	}
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
