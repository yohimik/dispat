package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProductionCoverpkgIncludesEveryProductionModule(t *testing.T) {
	t.Setenv("DISPAT_COVERPKG", "")
	got := strings.Split(productionCoverpkg(), ",")
	want := []string{
		"github.com/yohimik/dispat/services/dispat/...",
		"github.com/yohimik/dispat/pkg/ccme/...",
		"github.com/yohimik/dispat/pkg/config/...",
		"github.com/yohimik/dispat/pkg/manifest/...",
		"github.com/yohimik/dispat/pkg/models/...",
		"github.com/yohimik/dispat/pkg/scanner/...",
		"github.com/yohimik/dispat/pkg/writer/...",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("productionCoverpkg = %q, want %q", got, want)
	}
}

func TestProductionCoverpkgAcceptsTheRunnerScope(t *testing.T) {
	t.Setenv("DISPAT_COVERPKG", "example.test/one/...,example.test/two/...")
	if got := productionCoverpkg(); got != "example.test/one/...,example.test/two/..." {
		t.Fatalf("productionCoverpkg = %q", got)
	}
}

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
	require.True(t, IsTinyGo())
	require.Equal(t, []string{"-opt=z", "-no-debug", "-p", "2"}, compilerBuildArgs())
	require.NoError(t, validateBuildSelection())
}

func TestIsTinyGoRequiresExplicitCompiler(t *testing.T) {
	clearBuildSelectionEnv(t)
	t.Setenv("DISPAT_TEST_BINARY", "/tmp/tinygo-built-but-opaque")
	require.False(t, IsTinyGo())
	require.Empty(t, compilerBuildArgs())
}

func TestBuildSelectionRejectsMixedToolchains(t *testing.T) {
	clearBuildSelectionEnv(t)
	tests := []struct {
		name, binary, versions, compiler, cover, race, want string
	}{
		{name: "versioned binaries without plain prebuilt", versions: "versions", want: "requires DISPAT_TEST_BINARY"},
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

// TestVersionedFixturesDeclareTheirCompiler: the TinyGo gate hands every
// shard the fixtures it built once, and still names TinyGo so the tests that
// expect TinyGo's runtime know they drive one. The directory decides where
// each versioned binary comes from; the compiler is never asked for.
func TestVersionedFixturesDeclareTheirCompiler(t *testing.T) {
	clearBuildSelectionEnv(t)
	dir := t.TempDir()
	t.Setenv("DISPAT_TEST_BINARY", filepath.Join(dir, "dispat"))
	t.Setenv("DISPAT_TEST_VERSIONED_BINARY_DIR", dir)
	t.Setenv("DISPAT_TEST_COMPILER", "/usr/local/tinygo/bin/tinygo")
	want := filepath.Join(dir, "dispat-1.2.0-rc.1")
	require.NoError(t, os.WriteFile(want, nil, 0o755))
	require.NoError(t, validateBuildSelection())
	require.True(t, IsTinyGo())
	prebuilt, compilerBin, err := versionedBuildSelection("1.2.0-rc.1")
	require.NoError(t, err)
	require.Equal(t, want, prebuilt)
	require.Empty(t, compilerBin, "a fixture from the directory needs no compiler")
}

// fatalRecorder stands in for the test BuildVersioned is handed, so its
// refusal can be read instead of ending this test.
type fatalRecorder struct {
	testing.TB
	message string
}

func (r *fatalRecorder) Helper() {}

func (r *fatalRecorder) Fatalf(format string, args ...any) {
	r.message = fmt.Sprintf(format, args...)
	panic(r)
}

// TestBuildVersionedRefusesAnUnlistedVersion: a version the TinyGo gate has
// no fixture for fails every run, before anything is built, and says where
// the version has to be added.
func TestBuildVersionedRefusesAnUnlistedVersion(t *testing.T) {
	recorder := &fatalRecorder{TB: t}
	func() {
		defer func() {
			if recovered := recover(); recovered != recorder {
				panic(recovered)
			}
		}()
		BuildVersioned(recorder, "9.9.9")
	}()
	require.Contains(t, recorder.message, `BuildVersioned("9.9.9")`)
	require.Contains(t, recorder.message, "SelfUpdateFixtureVersions")
	require.Contains(t, recorder.message, "tiny-fixtures stage of services/dispat/Dockerfile")
}

// TestTinyFixtureStageBuildsTheFixtureVersions: the TinyGo gate builds the
// self-update fixtures once, in a stage of its own, rather than in every
// shard. The stage has to build exactly the versions the tests ask for, with
// the flags, stamp and limits the harness would have used, or the gate
// accepts fixtures that are not the ones the suite describes.
func TestTinyFixtureStageBuildsTheFixtureVersions(t *testing.T) {
	root, err := monorepoRoot()
	require.NoError(t, err)
	dockerfile, err := os.ReadFile(filepath.Join(root, "services", "dispat", "Dockerfile"))
	require.NoError(t, err)
	stage := dockerfileStage(t, string(dockerfile), "FROM tiny-build AS tiny-fixtures")

	loop := regexp.MustCompile(`(?m)for version in ([^;]+); do`).FindStringSubmatch(stage)
	require.Len(t, loop, 2, "the tiny-fixtures stage must build its versions in one loop:\n%s", stage)
	require.Equal(t, SelfUpdateFixtureVersions, strings.Fields(loop[1]))

	build := "tinygo build " + strings.Join(tinyGoBuildArgs, " ")
	require.Contains(t, stage, build, "the fixtures are built with the harness's TinyGo flags")
	require.Contains(t, stage, `-ldflags "`+versionLDFlag+`${version}"`,
		"the fixtures carry the stamp BuildVersioned would have given them")
	require.Contains(t, stage, `-o "/fixtures/dispat-${version}"`,
		"the fixtures are named the way versionedBuildSelection looks them up")
	for _, env := range tinyGoBuildEnv {
		require.Contains(t, stage, env, "the fixture builds run under the harness's compiler limits")
	}

	test := dockerfileStage(t, string(dockerfile), "FROM tiny-build AS tiny-test")
	require.Contains(t, test, "COPY --from=tiny-fixtures /fixtures /fixtures")
	require.Contains(t, test, "DISPAT_TEST_VERSIONED_BINARY_DIR=/fixtures")
}

// dockerfileStage returns one stage of a Dockerfile: its FROM line and every
// line up to the next stage's.
func dockerfileStage(t *testing.T, dockerfile, from string) string {
	t.Helper()
	_, rest, found := strings.Cut(dockerfile, "\n"+from+"\n")
	require.True(t, found, "no %q stage", from)
	if next := strings.Index(rest, "\nFROM "); next >= 0 {
		rest = rest[:next]
	}
	return from + "\n" + rest
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
