package publicapi_test

// The shared vocabulary pkg/scanner and pkg/writer both depend on, exercised
// through its own exported surface: file-name classification, the dependency
// kinds, image-reference splitting, Dockerfile reference location, and the
// compose identity rule. Every case here is a fact one of the two halves
// relies on, so a change in the vocabulary shows up as a failure here before
// it shows up as a manifest written into the wrong field.

import (
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/manifest"
)

func TestPublicAPIManifestFormatClassification(t *testing.T) {
	t.Run("names resolve to formats", func(t *testing.T) {
		cases := []struct {
			name string
			want manifest.Format
			ok   bool
		}{
			{"package.json", manifest.FormatNpm, true},
			{"Cargo.toml", manifest.FormatCargo, true},
			{"Acme.fsproj", manifest.FormatMSBuildProject, true},
			{"Acme.vbproj", manifest.FormatMSBuildProject, true},
			{"Acme.uproject", manifest.FormatUnrealProject, true},
			{"requirements.txt", manifest.FormatRequirements, true},
			{"requirements-dev.txt", manifest.FormatRequirements, true},
			{"dev-requirements.txt", manifest.FormatRequirements, true},
			{"OLD-REQUIREMENTS-NOTES.txt", "", false},
			{"Dockerfile", manifest.FormatDockerfile, true},
			{"Containerfile", manifest.FormatDockerfile, true},
			{"Dockerfile.dev", manifest.FormatDockerfile, true},
			{"Dockerfile.build.dev", manifest.FormatDockerfile, true},
			{"Dockerfile.md", "", false},
			{"Dockerfile.markdown", "", false},
			{"Dockerfile.rst", "", false},
			{"Dockerfile.adoc", "", false},
			{"Dockerfile.txt", "", false},
			{"Dockerfile.", "", false},
			{"api.Dockerfile", manifest.FormatDockerfile, true},
			{"api.containerfile", manifest.FormatDockerfile, true},
			{".dockerfile", "", false},
			{"README.md", "", false},
			// FormatOf deliberately never learns the path-qualified formats:
			// a bare manifest.json is a web app manifest.
			{"manifest.json", "", false},
			{"ProjectSettings.asset", "", false},
		}
		for _, tc := range cases {
			got, ok := manifest.FormatOf(tc.name)
			if ok != tc.ok || got != tc.want {
				t.Errorf("FormatOf(%q) = %q, %v; want %q, %v", tc.name, got, ok, tc.want, tc.ok)
			}
		}
	})

	t.Run("paths resolve the folder-qualified formats", func(t *testing.T) {
		cases := []struct {
			path string
			want manifest.Format
			ok   bool
		}{
			{"Packages/manifest.json", manifest.FormatUnityPackages, true},
			{"game/Packages/manifest.json", manifest.FormatUnityPackages, true},
			{`game\Packages\manifest.json`, manifest.FormatUnityPackages, true},
			{"MyPackages/manifest.json", "", false},
			{"manifest.json", "", false},
			{"ProjectSettings/ProjectSettings.asset", manifest.FormatUnityProjectSettings, true},
			{"Config/DefaultGame.ini", manifest.FormatUnrealGameConfig, true},
			{"Config/DefaultEngine.ini", manifest.FormatUnrealEngineConfig, true},
			{"app/Config/DefaultEngine.ini", manifest.FormatUnrealEngineConfig, true},
			{"nested/deep/package.json", manifest.FormatNpm, true},
			{`nested\deep\Cargo.toml`, manifest.FormatCargo, true},
		}
		for _, tc := range cases {
			got, ok := manifest.FormatOfPath(tc.path)
			if ok != tc.ok || got != tc.want {
				t.Errorf("FormatOfPath(%q) = %q, %v; want %q, %v", tc.path, got, ok, tc.want, tc.ok)
			}
		}
	})

	t.Run("only the folder-qualified formats report a path suffix", func(t *testing.T) {
		qualified := map[manifest.Format]string{
			manifest.FormatUnityPackages:        "Packages/manifest.json",
			manifest.FormatUnityProjectSettings: "ProjectSettings/ProjectSettings.asset",
			manifest.FormatUnrealGameConfig:     "Config/DefaultGame.ini",
			manifest.FormatUnrealEngineConfig:   "Config/DefaultEngine.ini",
		}
		for _, f := range manifest.Formats {
			suffix, ok := manifest.PathSuffix(f)
			want, qualifies := qualified[f]
			if ok != qualifies || suffix != want {
				t.Errorf("PathSuffix(%q) = %q, %v; want %q, %v", f, suffix, ok, want, qualifies)
			}
			if ok {
				got, resolved := manifest.FormatOfPath(suffix)
				if !resolved || got != f {
					t.Errorf("PathSuffix(%q) round trip = %q, %v", f, got, resolved)
				}
			}
		}
	})

	t.Run("every listed format is reachable from a file name or path", func(t *testing.T) {
		if len(manifest.Formats) == 0 {
			t.Fatal("no formats listed")
		}
		seen := map[manifest.Format]bool{}
		for _, f := range manifest.Formats {
			if seen[f] {
				t.Errorf("format %q listed twice", f)
			}
			seen[f] = true
		}
	})
}

func TestPublicAPIManifestKindVocabulary(t *testing.T) {
	valid := []manifest.Kind{
		manifest.KindDependencies, manifest.KindDevDependencies,
		manifest.KindPeerDependencies, manifest.KindOptionalDependencies,
	}
	for _, k := range valid {
		if !k.IsValid() || !k.Valid() {
			t.Errorf("kind %q is not valid", k)
		}
	}
	for _, k := range []manifest.Kind{"bundleDependencies", "scripts", "Dependencies"} {
		if k.IsValid() || k.Valid() {
			t.Errorf("kind %q accepted", k)
		}
	}
	if got := manifest.KindDependencies.String(); got != "dependencies" {
		t.Errorf("zero kind spells %q", got)
	}
	if got := manifest.KindPeerDependencies.String(); got != "peerDependencies" {
		t.Errorf("peer kind spells %q", got)
	}

	parsed := map[string]manifest.Kind{
		"":                     manifest.KindDependencies,
		"dependencies":         manifest.KindDependencies,
		"devDependencies":      manifest.KindDevDependencies,
		"peerDependencies":     manifest.KindPeerDependencies,
		"optionalDependencies": manifest.KindOptionalDependencies,
	}
	for word, want := range parsed {
		got, ok := manifest.ParseKind(word)
		if !ok || got != want {
			t.Errorf("ParseKind(%q) = %q, %v; want %q", word, got, ok, want)
		}
	}
	for _, word := range []string{"bundleDependencies", "DEPENDENCIES", "deps"} {
		if got, ok := manifest.ParseKind(word); ok {
			t.Errorf("ParseKind(%q) = %q, accepted", word, got)
		}
	}
}

func TestPublicAPIManifestNameNormalisation(t *testing.T) {
	words := manifest.NameWords("dev-requirements.build_two.txt")
	if strings.Join(words, "|") != "dev|requirements|build|two|txt" {
		t.Errorf("NameWords = %q", words)
	}
	if got := manifest.NameWords(""); len(got) != 0 {
		t.Errorf("NameWords(\"\") = %q", got)
	}
	for _, tc := range []struct{ in, want string }{
		{"Acme_Core", "acme-core"},
		{"acme.core", "acme-core"},
		{"  ACME--CORE  ", "acme-core"},
		{"acme", "acme"},
		{"--leading", "leading"},
		{"", ""},
	} {
		if got := manifest.NormalizePyName(tc.in); got != tc.want {
			t.Errorf("NormalizePyName(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
	if manifest.IsRequirementsFile("requirements.yaml") {
		t.Error("a non-.txt name is a requirements file")
	}
	if !manifest.IsDockerfile("DOCKERFILE") {
		t.Error("the spelling a build ignores is rejected")
	}
}

func TestPublicAPIManifestImageReferences(t *testing.T) {
	cases := []struct {
		ref                  string
		repository, tag, dig string
		tagged, pinned       bool
		interpolated         bool
	}{
		{"redis:7.2", "redis", "7.2", "", true, false, false},
		{"ghcr.io/acme/api:1.0.0", "ghcr.io/acme/api", "1.0.0", "", true, false, false},
		{"localhost:5000/api", "localhost:5000/api", "", "", false, false, false},
		{"localhost:5000/api:2.0.0", "localhost:5000/api", "2.0.0", "", true, false, false},
		{"redis", "redis", "", "", false, false, false},
		{"redis:", "redis", "", "", false, false, false},
		{"acme/api@sha256:abc", "acme/api", "", "sha256:abc", false, true, false},
		{"acme/api:1.0.0@sha256:abc", "acme/api", "1.0.0", "sha256:abc", true, true, false},
		{"${BASE}:${TAG}", "${BASE}", "${TAG}", "", true, false, true},
		{"$IMAGE", "$IMAGE", "", "", false, false, true},
		{"", "", "", "", false, false, false},
	}
	for _, tc := range cases {
		got := manifest.ParseImageRef(tc.ref)
		if got.Repository != tc.repository || got.Tag != tc.tag || got.Digest != tc.dig {
			t.Errorf("ParseImageRef(%q) = %+v", tc.ref, got)
		}
		if got.IsTagged() != tc.tagged || got.IsTagged() != tc.tagged {
			t.Errorf("ParseImageRef(%q).IsTagged() = %v; want %v", tc.ref, got.IsTagged(), tc.tagged)
		}
		if got.IsPinned() != tc.pinned || got.Pinned() != tc.pinned {
			t.Errorf("ParseImageRef(%q).IsPinned() = %v; want %v", tc.ref, got.IsPinned(), tc.pinned)
		}
		if got.IsInterpolated() != tc.interpolated || got.Interpolated() != tc.interpolated {
			t.Errorf("ParseImageRef(%q).IsInterpolated() = %v", tc.ref, got.IsInterpolated())
		}
		if tc.tagged && tc.ref[got.TagStart:got.TagEnd] != tc.tag {
			t.Errorf("ParseImageRef(%q) tag span %d..%d does not hold the tag", tc.ref, got.TagStart, got.TagEnd)
		}
	}

	for _, tag := range []string{"1.0.0", "v1", "release_1", "a.b-c", strings.Repeat("a", 128)} {
		if !manifest.IsValidTag(tag) || !manifest.ValidTag(tag) {
			t.Errorf("IsValidTag(%q) = false", tag)
		}
	}
	for _, tag := range []string{"", ".1.0.0", "-1.0.0", "1.0.0+build", "1 0", "acme/api", strings.Repeat("a", 129)} {
		if manifest.IsValidTag(tag) || manifest.ValidTag(tag) {
			t.Errorf("IsValidTag(%q) = true", tag)
		}
	}
}

func TestPublicAPIManifestDockerfileReferences(t *testing.T) {
	t.Run("every instruction that names an image", func(t *testing.T) {
		lines := strings.Split(strings.TrimPrefix(`
# syntax=docker/dockerfile:1.7
ARG BASE

FROM --platform=linux/amd64 ghcr.io/acme/base:1.0.0 AS build
COPY --from=ghcr.io/acme/tools:2.0.0 /usr/bin/tool /usr/bin/tool
RUN --mount=type=bind,from=ghcr.io/acme/assets:3.0.0,source=/a,target=/a \
    --mount=type=cache,target=/root/.cache \
    make build
RUN mytool sync --from=not/an:image

FROM scratch AS empty
FROM build AS reuse
COPY --from=build /out /out
COPY --from=0 /out /out
COPY --from= /out /out
FROM \
    ghcr.io/acme/runtime:4.0.0
`, "\n"), "\n")
		refs := manifest.DockerfileRefs(lines)
		var got []string
		for _, r := range refs {
			if lines[r.Line][r.Start:r.End] != r.Text {
				t.Errorf("reference %+v does not point at its own bytes in %q", r, lines[r.Line])
			}
			got = append(got, r.Text)
		}
		want := []string{
			"ghcr.io/acme/base:1.0.0",
			"ghcr.io/acme/tools:2.0.0",
			"ghcr.io/acme/assets:3.0.0",
			"ghcr.io/acme/runtime:4.0.0",
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("DockerfileRefs = %q; want %q", got, want)
		}
	})

	t.Run("an alias only shadows an image below its own stage", func(t *testing.T) {
		lines := []string{
			"FROM tools:1.0 AS first",
			"FROM alpine:3 AS tools",
			"COPY --from=tools /a /a",
		}
		refs := manifest.DockerfileRefs(lines)
		if len(refs) != 2 || refs[0].Text != "tools:1.0" || refs[1].Text != "alpine:3" {
			t.Fatalf("DockerfileRefs = %+v", refs)
		}
	})

	t.Run("continuations and carriage returns tokenise alike", func(t *testing.T) {
		unix := manifest.DockerfileRefs([]string{"FROM \\", "  redis:7.2"})
		dos := manifest.DockerfileRefs([]string{"FROM \\\r", "  redis:7.2\r"})
		if len(unix) != 1 || len(dos) != 1 || unix[0].Text != dos[0].Text || dos[0].Text != "redis:7.2" {
			t.Fatalf("unix = %+v, dos = %+v", unix, dos)
		}
	})

	t.Run("a file naming no image reports none", func(t *testing.T) {
		if refs := manifest.DockerfileRefs([]string{"", "   ", "# only prose", "RUN true"}); len(refs) != 0 {
			t.Fatalf("DockerfileRefs = %+v", refs)
		}
	})

	t.Run("a bare continuation and a flag naming no stage carry no reference", func(t *testing.T) {
		lines := []string{
			`\`,
			"COPY --chown=1000:1000 --chmod=755 /a /a",
			`FROM \`,
			`\`,
			"  redis:7.2",
		}
		refs := manifest.DockerfileRefs(lines)
		if len(refs) != 1 || refs[0].Text != "redis:7.2" || refs[0].Line != 4 {
			t.Fatalf("DockerfileRefs = %+v", refs)
		}
	})
}

func TestPublicAPIManifestComposeIdentity(t *testing.T) {
	cases := []struct {
		name             string
		services         []manifest.ComposeService
		repository, tag  string
		identityDeclared bool
	}{
		{
			name: "the service that builds and tags wins",
			services: []manifest.ComposeService{
				{Name: "cache", Image: "redis:7.2"},
				{Name: "api", Image: "ghcr.io/acme/api:1.0.0", Builds: true},
			},
			repository: "ghcr.io/acme/api", tag: "1.0.0", identityDeclared: true,
		},
		{
			name: "a builder with no tag cannot be the identity",
			services: []manifest.ComposeService{
				{Name: "api", Image: "ghcr.io/acme/api", Builds: true},
				{Name: "cache", Image: "redis:7.2"},
			},
			repository: "redis", tag: "7.2", identityDeclared: true,
		},
		{
			name: "with nothing building, the most-named tagged repository wins",
			services: []manifest.ComposeService{
				{Name: "cache", Image: "redis:7.2"},
				{Name: "worker", Image: "ghcr.io/acme/api:1.0.0"},
				{Name: "api", Image: "ghcr.io/acme/api:1.0.0"},
			},
			repository: "ghcr.io/acme/api", tag: "1.0.0", identityDeclared: true,
		},
		{
			name: "a tie goes to the lowest service name",
			services: []manifest.ComposeService{
				{Name: "zeta", Image: "ghcr.io/acme/zeta:1.0.0"},
				{Name: "alpha", Image: "ghcr.io/acme/alpha:1.0.0"},
			},
			repository: "ghcr.io/acme/alpha", tag: "1.0.0", identityDeclared: true,
		},
		{
			name: "an interpolated reference is never an identity",
			services: []manifest.ComposeService{
				{Name: "api", Image: "${BASE}:${TAG}", Builds: true},
			},
		},
		{
			name: "untagged third-party wiring declares no identity",
			services: []manifest.ComposeService{
				{Name: "cache", Image: "redis"},
				{Name: "db", Image: ""},
			},
		},
		{name: "an empty file declares no identity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repository, tag := manifest.ComposeIdentity(tc.services)
			if repository != tc.repository || tag != tc.tag {
				t.Fatalf("ComposeIdentity = %q, %q; want %q, %q", repository, tag, tc.repository, tc.tag)
			}
			if (repository != "") != tc.identityDeclared {
				t.Fatalf("identity declared = %v; want %v", repository != "", tc.identityDeclared)
			}
		})
	}

	t.Run("the caller's slice is not reordered", func(t *testing.T) {
		services := []manifest.ComposeService{
			{Name: "zeta", Image: "ghcr.io/acme/zeta:1.0.0"},
			{Name: "alpha", Image: "ghcr.io/acme/alpha:1.0.0"},
		}
		manifest.ComposeIdentity(services)
		if services[0].Name != "zeta" {
			t.Fatalf("ComposeIdentity reordered its input: %+v", services)
		}
	})
}
