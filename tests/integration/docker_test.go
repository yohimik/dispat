package integration

// Area 18: Docker through the compiled binary. Docker is the ecosystem dispat
// was built around and the last one it could not read, so what matters here is
// the whole loop closing over a process boundary: `dispat compute` deriving an
// image chain from a FROM line nobody wrote into the config, a release
// reconciling that FROM line and a compose file's tags to the versions it just
// computed, and the manifest commands reporting a Dockerfile and a compose
// file the way they report a package.json.
//
// The unit suites prove the parsing and the splicing. Only a real run can show
// that the name a Dockerfile declares — an image repository, never a folder
// name — reaches the workspace index the planner and the writer share.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestDockerComputeDerivesTheImageChain: two image packages, the second built
// FROM the first. Nothing in the config says they are related; compute reads
// it off the Dockerfile and writes the edge, and the next status orders the
// two by it.
func TestDockerComputeDerivesTheImageChain(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Packages = map[string]models.PackageConfig{
		// An image's identity is its repository, which is never the folder
		// name. Stating it is what lets the chain resolve.
		"base": {ManifestNames: []string{"registry.example.com/base"}},
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "base")
	r.SeedPackage("packages", "app")
	r.WriteFile("packages/base/Dockerfile", "FROM alpine:3.20\nRUN apk add --no-cache ca-certificates\n")
	r.WriteFile("packages/app/Dockerfile",
		"FROM registry.example.com/base:0.0.0 AS runtime\nCOPY --from=runtime /bin/app /bin/app\n")
	r.Commit("feat(base,app): bootstrap")

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "+ add     app -> base (dependencies)",
		"the FROM line is the edge")
	assert.Contains(t, res.Stdout, "packages/app/Dockerfile",
		"the suggestion says which file it came from")
	// alpine is not a workspace package, so it is not an edge — only a
	// declaration the graph has nowhere to put.
	assert.NotContains(t, res.Stdout, "alpine")

	res = r.Command("compute", "--write")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "applied 1 change(s)")
	assert.Equal(t, 0, r.Command("compute", "--check").Code, "the gate is green once written")

	res = r.Command("status")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "app", "the derived edge is now part of the graph")
}

// TestDockerReleaseReconcilesTagsAndCompose: a release of the base image
// rewrites the consumer's FROM line and its compose file to the version just
// computed — the base's tag for the dependency, the app's own for the image it
// builds. This is the workflow the cookbook used to route through a
// --build-arg because nothing could write the file.
func TestDockerReleaseReconcilesTagsAndCompose(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Spaces["libs"] = models.SpaceConfig{
		Path: "packages", Flow: buildPublish(),
		// The default range policy is a caret, and a tag is a plain label, so
		// what lands is a bare version — no registry can resolve a range.
		// "root" is that default spelled out, because an empty block is pruned
		// by the loader as absent.
		AutoVersion: &models.AutoVersionConfig{Manifests: "root"},
	}
	cfg.Packages = map[string]models.PackageConfig{
		"base": {ManifestNames: []string{"registry.example.com/base"}},
	}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "app", Provider: "base"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "base")
	r.SeedPackage("packages", "app")
	r.WriteFile("packages/base/Dockerfile", "FROM alpine:3.20\n")
	r.WriteFile("packages/app/Dockerfile",
		"FROM registry.example.com/base:0.0.0 AS runtime\n"+
			"COPY --from=runtime /bin/app /bin/app\n"+
			"COPY --from=registry.example.com/base:0.0.0 /etc/ssl /etc/ssl\n")
	r.WriteFile("packages/app/compose.yaml",
		"services:\n"+
			"  app:\n"+
			"    build:\n"+
			"      context: .\n"+
			"      tags:\n"+
			"        - registry.example.com/app:0.0.0\n"+
			"    image: registry.example.com/app:0.0.0\n"+
			"  base:\n"+
			"    image: registry.example.com/base:0.0.0\n"+
			"    ports:\n"+
			"      - \"8080:8080\"\n")
	// The caret carries the change down the edge, which is what makes app
	// release at all and its manifests worth reconciling.
	r.Commit("feat(base)^: harden the base image")

	r.ReleaseOK()

	dockerfile := readFile(t, r, "packages", "app", "Dockerfile")
	assert.Contains(t, dockerfile, "FROM registry.example.com/base:0.1.0 AS runtime",
		"the base's FROM tag follows its release")
	assert.Contains(t, dockerfile, "COPY --from=registry.example.com/base:0.1.0 /etc/ssl /etc/ssl",
		"a COPY --from naming a real image is reconciled too")
	assert.Contains(t, dockerfile, "COPY --from=runtime /bin/app /bin/app",
		"a COPY --from naming a stage is left alone")

	compose := readFile(t, r, "packages", "app", "compose.yaml")
	assert.Contains(t, compose, "image: registry.example.com/app:0.0.1",
		"the service the file builds carries app's own new version")
	assert.Contains(t, compose, "- registry.example.com/app:0.0.1",
		"and so does every tag that build publishes")
	assert.Contains(t, compose, "image: registry.example.com/base:0.1.0",
		"the pulled service carries the provider's")
	assert.Contains(t, compose, "- \"8080:8080\"",
		"a port mapping is not an image reference")
}

// TestDockerManifestCommands: the config-free scanner and writer commands over
// the two Docker formats. The scanner reports a compose file's identity and a
// Dockerfile's bases; the writer's rewrite is proved byte-for-byte on disk, and
// the references it must decline come back as skipped rather than as failures.
func TestDockerManifestCommands(t *testing.T) {
	r := harness.New(t)
	dockerfile := "FROM ghcr.io/acme/base:1.0.0 AS build\n" +
		"FROM redis@sha256:0000000000000000000000000000000000000000000000000000000000000000\n" +
		"COPY --from=build /a /b\n"
	compose := "services:\n" +
		"  api:\n" +
		"    build: .\n" +
		"    image: ghcr.io/acme/api:1.2.0\n" +
		"  cache:\n" +
		"    image: redis:7.2\n"
	r.WriteFile("images/api/Dockerfile", dockerfile)
	r.WriteFile("images/api/compose.yaml", compose)
	// Deliberately no dispat.json and no commit: these commands read files.

	res := r.Command("scanner", "images/api")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "compose.yaml  docker  ghcr.io/acme/api@1.2.0",
		"the service that builds and tags names the file")
	assert.Contains(t, res.Stdout, "Dockerfile  docker",
		"a Dockerfile declares dependencies and no identity")
	assert.Contains(t, res.Stdout, "ghcr.io/acme/base")
	assert.Contains(t, res.Stdout, "redis")

	res = r.Command("writer", "images/api/compose.yaml",
		"--set-version", "1.3.0", "--set", "redis=7.4")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, "services:\n"+
		"  api:\n"+
		"    build: .\n"+
		"    image: ghcr.io/acme/api:1.3.0\n"+
		"  cache:\n"+
		"    image: redis:7.4\n",
		readFile(t, r, "images", "api", "compose.yaml"),
		"only the two tags changed")

	// A digest-pinned base is the normal state of a careful Dockerfile, so the
	// command succeeds and reports it rather than failing.
	res = r.Command("writer", "images/api/Dockerfile", "--set", "redis=7.4", "--log-format", "json")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, dockerfile, readFile(t, r, "images", "api", "Dockerfile"),
		"a declined rewrite leaves every byte alone")

	// An edit naming nothing in the file is still a missing edit under --strict.
	assert.Equal(t, 1, r.Command("writer", "images/api/Dockerfile",
		"--set", "nowhere=1.0.0", "--strict").Code)
}

// readFile is the contents of one of the harness repo's files.
func readFile(t *testing.T, r *harness.Repo, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(r.Path(parts...))
	require.NoError(t, err)
	return string(data)
}
