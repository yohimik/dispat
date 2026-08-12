package selfupdate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// repoRoot is where install.sh and the image folders live, four levels up from
// this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	require.NoError(t, err)
	return root
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	require.NoError(t, err, "%s must exist: the installer and the images are part of the release", rel)
	return string(data)
}

// TestInstallScriptNamesTheSameAssets: install.sh and AssetName are the two
// halves of one contract, the same way scripts/build-dispat.sh is (see
// TestAssetNameMatchesTheBuildScript). A disagreement here is every install and
// every image build failing at once on a 404, so the script's template is
// evaluated against the Go function rather than eyeballed.
func TestInstallScriptNamesTheSameAssets(t *testing.T) {
	script := readRepoFile(t, "install.sh")

	template := regexp.MustCompile(`(?m)^ASSET="([^"]+)"`).FindStringSubmatch(script)
	require.Len(t, template, 2, "install.sh must build the asset name in one ASSET= assignment")

	for _, tc := range []struct{ goos, goarch string }{
		{"linux", "amd64"}, {"linux", "arm64"},
		{"darwin", "amd64"}, {"darwin", "arm64"},
		{"windows", "amd64"}, {"windows", "arm64"},
	} {
		got := strings.NewReplacer("${OS}", tc.goos, "${ARCH}", tc.goarch).Replace(template[1])
		if tc.goos == "windows" {
			// The script appends the extension in a second statement, under the
			// same condition this branch stands for.
			assert.Contains(t, script, `ASSET="${ASSET}.exe"`)
			got += ".exe"
		}
		assert.Equal(t, AssetName(tc.goos, tc.goarch), got, "%s/%s", tc.goos, tc.goarch)
	}
}

// TestInstallScriptFiltersTheSameTagPrefix: this repository publishes a release
// per module, so a resolver that stops filtering by tag starts installing
// pkg/ccme. The prefix is a constant on both sides and they must be one value.
func TestInstallScriptFiltersTheSameTagPrefix(t *testing.T) {
	script := readRepoFile(t, "install.sh")
	assert.Contains(t, script, `TAG_PREFIX="`+DefaultTagPrefix+`"`)

	ps1 := readRepoFile(t, "install.ps1")
	assert.Contains(t, ps1, `$TagPrefix = '`+DefaultTagPrefix+`'`)
	assert.Contains(t, ps1, `"dispat-windows-$Arch.exe"`,
		"install.ps1 must name the same asset AssetName does for windows")
}

// TestImagesInstallThroughTheScript: the images exist to ship the released
// binary, and they get it by running install.sh rather than by carrying their
// own download. A Dockerfile that stopped passing the target platform would
// silently build an amd64 image for arm64 under emulation.
func TestImagesInstallThroughTheScript(t *testing.T) {
	for _, pkg := range []string{"dispat-ubuntu", "dispat-debian", "dispat-alpine", "dispat-dind"} {
		t.Run(pkg, func(t *testing.T) {
			dockerfile := readRepoFile(t, filepath.Join("docker", pkg, "Dockerfile"))
			assert.Contains(t, dockerfile, "COPY install.sh /tmp/install.sh",
				"the build context is the repository root, so install.sh is copied straight in")
			assert.Contains(t, dockerfile, `sh /tmp/install.sh --version "${DISPAT_VERSION}" --os linux --arch "${TARGETARCH}"`,
				"TARGETARCH is what makes a cross-built image install its own architecture")

			compose := readRepoFile(t, filepath.Join("docker", pkg, "docker-compose.yml"))
			assert.Contains(t, compose, "DISPAT_VERSION: ${DISPAT_WORKSPACE_DISPAT_VERSION:?}",
				"the CLI version an image installs is the workspace's, not the image's own")
			assert.Contains(t, compose, "image: docker.io/yohimik/"+pkg+":",
				"the compose file is this package's manifest: its image line carries the name and version")
			assert.NotContains(t, compose, "tags:",
				"moving tags belong in the per-channel files, where autoVersion cannot reach them")

			// One file per channel this repository releases on: the stage runs
			// `-f $DISPAT_CHANNEL.yml`, and a channel with no file fails the
			// build rather than quietly inheriting the wrong tags.
			stable := readRepoFile(t, filepath.Join("docker", pkg, "stable.yml"))
			assert.Contains(t, stable, "docker.io/yohimik/"+pkg+":latest")
			assert.Contains(t, stable, "docker.io/yohimik/"+pkg+":${DISPAT_MAJOR:?}")
			rc := readRepoFile(t, filepath.Join("docker", pkg, "rc.yml"))
			assert.Contains(t, rc, "docker.io/yohimik/"+pkg+":rc")
			assert.NotContains(t, rc, ":latest",
				"a prerelease must never move latest")
		})
	}
}

// composeVarRef finds compose's interpolation of a dispat variable:
// "${DISPAT_MAJOR:?}", "${DISPAT_WORKSPACE_DISPAT_VERSION:?}".
var composeVarRef = regexp.MustCompile(`\$\{(DISPAT_[A-Z0-9_]+)`)

// TestImageComposeFilesOnlyNameVariablesDispatEmits: the compose files are
// interpolated by docker, not by dispat, so a name dispat never sets is not a
// build that reads an empty string — with the ":?" form it is a build that
// stops before it starts, at the very end of a release, after the artefacts
// are already out.
//
// That is not hypothetical: every stable image build failed on
// "required variable DISPAT_MAJOR is missing a value" for as long as the
// channel files named a variable the planner did not render. Eyeballing the
// pair is what let that ship, so the two sides are compared here instead.
//
// Dockerfiles are deliberately out of scope: their "${DISPAT_VERSION}" is a
// build ARG that compose passes in under "args:", not a stage variable.
func TestImageComposeFilesOnlyNameVariablesDispatEmits(t *testing.T) {
	// A release of a package named "dispat", which is what the images read
	// their CLI version from, so DISPAT_WORKSPACE_DISPAT_* resolves for real
	// rather than being special-cased by prefix.
	rel := &plan.Release{
		Pkg:     &model.Package{Name: "dispat", Space: &model.Space{Name: "services"}},
		Next:    ccme.Version{Major: 1, Minor: 4, Patch: 2},
		Current: ccme.Version{Major: 1, Minor: 4, Patch: 1},
		Channel: ccme.ChannelStable,
	}
	pl := &plan.Plan{Order: []string{"dispat"}, Releases: map[string]*plan.Release{"dispat": rel}}

	emitted := map[string]bool{"DISPAT_STAGE": true} // added per task by packageEnv
	for _, pairs := range [][]string{rel.Vars(), rel.OutputVars(), release.WorkspaceEnv(pl, zerolog.Nop())} {
		for _, p := range pairs {
			name, _, ok := strings.Cut(p, "=")
			require.True(t, ok, "not a NAME=value pair: %q", p)
			emitted[name] = true
		}
	}

	for _, pkg := range []string{"dispat-ubuntu", "dispat-debian", "dispat-alpine", "dispat-dind"} {
		for _, file := range []string{"docker-compose.yml", "stable.yml", "rc.yml"} {
			rel := filepath.Join("docker", pkg, file)
			for _, m := range composeVarRef.FindAllStringSubmatch(readRepoFile(t, rel), -1) {
				assert.True(t, emitted[m[1]],
					"%s interpolates %s, which no dispat variable provides", rel, m[1])
			}
		}
	}
}
