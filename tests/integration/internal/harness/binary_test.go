package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func clearBuildSelectionEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"DISPAT_TEST_BINARY",
		"DISPAT_TEST_VERSIONED_BINARY_DIR",
		"DISPAT_TEST_COMPILER",
		"DISPAT_COVERDIR",
		"DISPAT_TEST_RACE",
	} {
		t.Setenv(name, "")
	}
}

func TestCompilerSelection(t *testing.T) {
	clearBuildSelectionEnv(t)
	t.Setenv("DISPAT_TEST_COMPILER", "tinygo")
	require.Equal(t, "tinygo", compiler())
	kind, err := compilerKind()
	require.NoError(t, err)
	require.Equal(t, "tinygo", kind)
	require.True(t, UsesTinyGo())
	require.Equal(t, []string{"-opt=z", "-no-debug", "-p", "2"}, compilerBuildArgs())
	require.NoError(t, validateBuildSelection())
}

func TestUsesTinyGoRequiresExplicitCompiler(t *testing.T) {
	clearBuildSelectionEnv(t)
	t.Setenv("DISPAT_TEST_BINARY", "/tmp/tinygo-built-but-opaque")
	require.False(t, UsesTinyGo())
	require.Empty(t, compilerBuildArgs())
}

func TestBuildSelectionRejectsMixedToolchains(t *testing.T) {
	clearBuildSelectionEnv(t)
	tests := []struct {
		name, binary, versions, compiler, cover, race, want string
	}{
		{name: "versioned binaries without plain prebuilt", versions: "versions", want: "requires DISPAT_TEST_BINARY"},
		{name: "versioned binaries and compiler", binary: "dispat", versions: "versions", compiler: "tinygo", want: "mutually exclusive"},
		{name: "unknown compiler", compiler: "clang", want: "must name go or tinygo"},
		{name: "TinyGo coverage", compiler: "tinygo", cover: "coverage", want: "DISPAT_COVERDIR"},
		{name: "TinyGo race", compiler: "tinygo", race: "1", want: "DISPAT_TEST_RACE"},
		{name: "prebuilt coverage", binary: "dispat", versions: "versions", cover: "coverage", want: "DISPAT_COVERDIR"},
		{name: "prebuilt race", binary: "dispat", versions: "versions", race: "1", want: "DISPAT_TEST_RACE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DISPAT_TEST_BINARY", tt.binary)
			t.Setenv("DISPAT_TEST_VERSIONED_BINARY_DIR", tt.versions)
			t.Setenv("DISPAT_TEST_COMPILER", tt.compiler)
			t.Setenv("DISPAT_COVERDIR", tt.cover)
			t.Setenv("DISPAT_TEST_RACE", tt.race)
			require.ErrorContains(t, validateBuildSelection(), tt.want)
		})
	}
}

func TestVersionedPrebuiltName(t *testing.T) {
	clearBuildSelectionEnv(t)
	dir := t.TempDir()
	t.Setenv("DISPAT_TEST_BINARY", filepath.Join(dir, "dispat"))
	t.Setenv("DISPAT_TEST_VERSIONED_BINARY_DIR", dir)
	want := filepath.Join(dir, "dispat-1.2.3")
	require.NoError(t, os.WriteFile(want, nil, 0o755))
	require.NoError(t, validateBuildSelection())
	prebuilt, compilerBin, err := versionedBuildSelection("1.2.3")
	require.NoError(t, err)
	require.Equal(t, want, prebuilt)
	require.Empty(t, compilerBin)
}

func TestPrebuiltWithSelectedCompilerBuildsVersionedFixtures(t *testing.T) {
	clearBuildSelectionEnv(t)
	t.Setenv("DISPAT_TEST_BINARY", "/tmp/exported-dispat")
	t.Setenv("DISPAT_TEST_COMPILER", "go")
	require.NoError(t, validateBuildSelection())
	prebuilt, compilerBin, err := versionedBuildSelection("1.2.3")
	require.NoError(t, err)
	require.Empty(t, prebuilt)
	require.NotEmpty(t, compilerBin)
}

func TestVersionedBuildRejectsPrebuiltOnlySelection(t *testing.T) {
	clearBuildSelectionEnv(t)
	t.Setenv("DISPAT_TEST_BINARY", "/tmp/exported-dispat")
	_, _, err := versionedBuildSelection("1.2.3")
	require.ErrorContains(t, err, "BuildVersioned requires DISPAT_TEST_COMPILER")
}
