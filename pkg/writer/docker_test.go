package writer

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// names lists the edits in a result bucket, for readable failures.
func names(edits []Edit) []string {
	out := make([]string, 0, len(edits))
	for _, e := range edits {
		out = append(out, e.Name)
	}
	slices.Sort(out)
	return out
}

func TestDockerfileRewritePreservesEveryOtherByte(t *testing.T) {
	// Deliberately awkward: a parser directive, a platform flag, an alias
	// whose name is also a real image elsewhere, a continued RUN with a
	// comment inside it, two references on one line, and no trailing newline.
	src := "# syntax=docker/dockerfile:1\n" +
		"ARG GO_VERSION=1.25\n" +
		"FROM --platform=$BUILDPLATFORM ghcr.io/acme/toolchain:2.1.0 AS builder\n" +
		"RUN --mount=type=bind,from=ghcr.io/acme/protos:0.4.0,target=/p \\\n" +
		"    # regenerate before building\n" +
		"    --mount=type=bind,from=ghcr.io/acme/certs:3.0.0,target=/c \\\n" +
		"    go build -o /app ./cmd/app\n" +
		"\n" +
		"FROM ghcr.io/acme/base:1.2.3\n" +
		"COPY --from=builder /app /usr/local/bin/app\n" +
		"LABEL org.opencontainers.image.base.name=\"ghcr.io/acme/base:1.2.3\"\n" +
		"CMD [\"app\"]"
	path := seed(t, "Dockerfile", src)

	res, err := Rewrite(path, "", []Edit{
		{Name: "ghcr.io/acme/base", Range: "2.0.0"},
		{Name: "ghcr.io/acme/toolchain", Range: "2.2.0"},
		{Name: "ghcr.io/acme/protos", Range: "0.5.0"},
		{Name: "ghcr.io/acme/certs", Range: "3.1.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "# syntax=docker/dockerfile:1\n" +
		"ARG GO_VERSION=1.25\n" +
		"FROM --platform=$BUILDPLATFORM ghcr.io/acme/toolchain:2.2.0 AS builder\n" +
		"RUN --mount=type=bind,from=ghcr.io/acme/protos:0.5.0,target=/p \\\n" +
		"    # regenerate before building\n" +
		"    --mount=type=bind,from=ghcr.io/acme/certs:3.1.0,target=/c \\\n" +
		"    go build -o /app ./cmd/app\n" +
		"\n" +
		"FROM ghcr.io/acme/base:2.0.0\n" +
		"COPY --from=builder /app /usr/local/bin/app\n" +
		// A LABEL is prose. The reference inside it is not an instruction that
		// names an image, so it is not touched.
		"LABEL org.opencontainers.image.base.name=\"ghcr.io/acme/base:1.2.3\"\n" +
		"CMD [\"app\"]"
	if got := read(t, path); got != want {
		t.Errorf("rewrite produced:\n%q\nwant:\n%q", got, want)
	}
	if got := names(res.Applied); len(got) != 4 {
		t.Errorf("Applied = %v, want all four", got)
	}
	if res.VersionWritten {
		t.Error("a Dockerfile has no version of its own to write")
	}
}

func TestDockerfileTwoReferencesOnOneLine(t *testing.T) {
	// Both splices land on one line, so the first must not move the second.
	// The short new tag and the long one exercise both shift directions.
	src := "RUN --mount=type=bind,from=a/one:1.0.0,target=/1 --mount=type=bind,from=a/two:2.0.0,target=/2 make\n"
	path := seed(t, "Dockerfile", src)
	if _, err := Rewrite(path, "", []Edit{
		{Name: "a/one", Range: "1.0.0-rc.1"},
		{Name: "a/two", Range: "3.0"},
	}); err != nil {
		t.Fatal(err)
	}
	want := "RUN --mount=type=bind,from=a/one:1.0.0-rc.1,target=/1 --mount=type=bind,from=a/two:3.0,target=/2 make\n"
	if got := read(t, path); got != want {
		t.Errorf("rewrite produced %q, want %q", got, want)
	}
}

func TestDockerfileDeclinesWhatItMustNotWrite(t *testing.T) {
	src := "FROM redis\n" +
		"FROM postgres@sha256:abc123\n" +
		"FROM mysql:8.0@sha256:def456\n" +
		"FROM ${REGISTRY}/api:${TAG}\n" +
		"FROM ghcr.io/acme/base:$VERSION\n"
	path := seed(t, "Dockerfile", src)

	res, err := Rewrite(path, "", []Edit{
		{Name: "redis", Range: "7.2"},               // no tag to replace
		{Name: "postgres", Range: "16.1"},           // digest, no tag
		{Name: "mysql", Range: "8.4"},               // digest beside a tag
		{Name: "${REGISTRY}/api", Range: "1.0.0"},   // resolved outside the file
		{Name: "ghcr.io/acme/base", Range: "1.0.0"}, // ditto, tag half only
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != src {
		t.Errorf("a declined rewrite changed the file:\n%q", got)
	}
	want := []string{"${REGISTRY}/api", "ghcr.io/acme/base", "mysql", "postgres", "redis"}
	if got := names(res.Skipped); !slices.Equal(got, want) {
		t.Errorf("Skipped = %v, want %v", got, want)
	}
	if len(res.Applied) != 0 || len(res.Missing) != 0 {
		t.Errorf("Applied = %v, Missing = %v, want neither", res.Applied, res.Missing)
	}
}

func TestDockerfileMissingAndNoOp(t *testing.T) {
	src := "FROM ghcr.io/acme/base:1.2.3\n"
	path := seed(t, "Dockerfile", src)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	res, err := Rewrite(path, "", []Edit{
		{Name: "ghcr.io/acme/base", Range: "1.2.3"}, // already right
		{Name: "ghcr.io/acme/other", Range: "1.0.0"},
		// An alias is not an image, so an edit naming one finds nothing.
		{Name: "builder", Range: "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 || len(res.Skipped) != 0 {
		t.Errorf("Applied = %v, Skipped = %v, want neither", res.Applied, res.Skipped)
	}
	if got := names(res.Missing); !slices.Equal(got, []string{"builder", "ghcr.io/acme/other"}) {
		t.Errorf("Missing = %v", got)
	}
	if got := read(t, path); got != src {
		t.Errorf("a no-op rewrite changed the file: %q", got)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("a no-op rewrite rewrote the file")
	}
}

func TestDockerfileRefusesAnIllegalTag(t *testing.T) {
	src := "FROM ghcr.io/acme/base:1.2.3\n"
	path := seed(t, "Dockerfile", src)
	for _, bad := range []string{"^1.2.3", "1.2.3 ", "", "a/b"} {
		if _, err := Rewrite(path, "", []Edit{{Name: "ghcr.io/acme/base", Range: bad}}); err == nil {
			t.Errorf("writing %q as a tag must be refused", bad)
		}
	}
	if got := read(t, path); got != src {
		t.Errorf("a refused rewrite changed the file: %q", got)
	}
}

func TestDockerfileCRLFAndVariantNames(t *testing.T) {
	for _, name := range []string{"Dockerfile", "Dockerfile.dev", "api.Dockerfile", "Containerfile"} {
		path := seed(t, name, "FROM ghcr.io/acme/base:1.0.0 AS build\r\nCOPY --from=build /a /b\r\n")
		res, err := Rewrite(path, "", []Edit{{Name: "ghcr.io/acme/base", Range: "1.1.0"}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := "FROM ghcr.io/acme/base:1.1.0 AS build\r\nCOPY --from=build /a /b\r\n"
		if got := read(t, path); got != want {
			t.Errorf("%s: rewrite produced %q, want %q", name, got, want)
		}
		if len(res.Applied) != 1 {
			t.Errorf("%s: Applied = %v", name, res.Applied)
		}
	}
}

func TestComposeRewritesImagesAndBuildTags(t *testing.T) {
	src := `# the stack
name: acme

services:
  cache:
    image: redis:7.2          # pinned by hand elsewhere
    ports:
      - "6379:6379"
  db:
    image: 'postgres:16.1'
    environment:
      DATABASE_URL: "postgres:5432"
  api:
    build:
      context: .
      tags:
        - ghcr.io/acme/api:1.4.2
        - "docker.io/acme/api:1.4.2"
    image: ghcr.io/acme/api:1.4.2
    depends_on:
      - db
  api-replica:
    image: ghcr.io/acme/api:1.4.2
`
	path := seed(t, "compose.yaml", src)
	res, err := Rewrite(path, "1.5.0", []Edit{
		{Name: "redis", Range: "7.4"},
		{Name: "postgres", Range: "16.2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `# the stack
name: acme

services:
  cache:
    image: redis:7.4          # pinned by hand elsewhere
    ports:
      - "6379:6379"
  db:
    image: 'postgres:16.2'
    environment:
      DATABASE_URL: "postgres:5432"
  api:
    build:
      context: .
      tags:
        - ghcr.io/acme/api:1.5.0
        - "docker.io/acme/api:1.5.0"
    image: ghcr.io/acme/api:1.5.0
    depends_on:
      - db
  api-replica:
    image: ghcr.io/acme/api:1.5.0
`
	if got := read(t, path); got != want {
		t.Errorf("rewrite produced:\n%s\nwant:\n%s", got, want)
	}
	if !res.VersionWritten {
		t.Error("VersionWritten = false, want true")
	}
	if got := names(res.Applied); !slices.Equal(got, []string{"postgres", "redis"}) {
		t.Errorf("Applied = %v", got)
	}
}

func TestComposeInlineTagList(t *testing.T) {
	src := "services:\n" +
		"  api:\n" +
		"    build:\n" +
		"      context: .\n" +
		"      tags: [\"ghcr.io/acme/api:1.0.0\", ghcr.io/acme/api:latest , 'other/api:1.0.0']\n" +
		"    image: ghcr.io/acme/api:1.0.0\n"
	path := seed(t, "compose.yaml", src)
	if _, err := Rewrite(path, "1.1.0", nil); err != nil {
		t.Fatal(err)
	}
	want := "services:\n" +
		"  api:\n" +
		"    build:\n" +
		"      context: .\n" +
		"      tags: [\"ghcr.io/acme/api:1.1.0\", ghcr.io/acme/api:1.1.0 , 'other/api:1.1.0']\n" +
		"    image: ghcr.io/acme/api:1.1.0\n"
	if got := read(t, path); got != want {
		t.Errorf("rewrite produced:\n%q\nwant:\n%q", got, want)
	}
}

func TestComposeLeavesEverythingItDoesNotOwn(t *testing.T) {
	// Nothing here is a service image: a port mapping, an environment value, a
	// command, an image key under volumes, and a tags list outside a build.
	src := `x-common: &common
  image: ghcr.io/acme/api:9.9.9
volumes:
  data:
    image: ghcr.io/acme/api:9.9.9
services:
  api:
    build: .
    image: ghcr.io/acme/api:1.0.0
    command: run --base redis:7.2
    ports:
      - "8080:8080"
    labels:
      tags:
        - ghcr.io/acme/api:9.9.9
`
	path := seed(t, "compose.yaml", src)
	if _, err := Rewrite(path, "2.0.0", []Edit{{Name: "redis", Range: "7.4"}}); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if strings.Count(got, "9.9.9") != 3 {
		t.Errorf("a scalar outside services.<name>.image was rewritten:\n%s", got)
	}
	if !strings.Contains(got, "image: ghcr.io/acme/api:2.0.0") {
		t.Errorf("the service's own image was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "command: run --base redis:7.2") {
		t.Errorf("a command was rewritten:\n%s", got)
	}
}

func TestComposeTieBreakDecidesWhoOwnsTheVersion(t *testing.T) {
	src := "services:\n  cache:\n    image: redis:7.2\n  db:\n    image: postgres:16.1\n"
	path := seed(t, "compose.yaml", src)
	// Nothing builds and each image appears once, so the tie goes to the
	// lowest service name: "cache" owns the version and "db" stays a
	// dependency. The writer must land on the same service the reader reported
	// the version from, which is what sharing manifest.ComposeIdentity buys.
	res, err := Rewrite(path, "5.0.0", []Edit{{Name: "postgres", Range: "16.2"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.VersionWritten {
		t.Error("VersionWritten = false, want true")
	}
	want := "services:\n  cache:\n    image: redis:5.0.0\n  db:\n    image: postgres:16.2\n"
	if got := read(t, path); got != want {
		t.Errorf("rewrite produced %q, want %q", got, want)
	}
	if got := names(res.Applied); !slices.Equal(got, []string{"postgres"}) {
		t.Errorf("Applied = %v", got)
	}
}

func TestComposeWithNoIdentityWritesNoVersion(t *testing.T) {
	// No tagged image anywhere, so the file declares nothing of its own and
	// the version argument has no target. Every image is still a dependency.
	src := "services:\n  cache:\n    image: redis\n  db:\n    image: postgres\n"
	path := seed(t, "compose.yaml", src)
	res, err := Rewrite(path, "5.0.0", []Edit{{Name: "redis", Range: "7.4"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten {
		t.Error("VersionWritten = true, but the file names no image of its own")
	}
	if got := read(t, path); got != src {
		t.Errorf("rewrite produced %q, want it unchanged", got)
	}
	if got := names(res.Skipped); !slices.Equal(got, []string{"redis"}) {
		t.Errorf("Skipped = %v, want the untagged image declined", got)
	}
}

func TestComposeDeclinesWhatItMustNotWrite(t *testing.T) {
	src := "services:\n" +
		"  api:\n" +
		"    build: .\n" +
		"    image: ${REGISTRY}/api:${TAG}\n" +
		"  cache:\n" +
		"    image: redis\n" +
		"  db:\n" +
		"    image: postgres@sha256:abc\n"
	path := seed(t, "compose.yaml", src)
	res, err := Rewrite(path, "1.0.0", []Edit{
		{Name: "redis", Range: "7.4"},
		{Name: "postgres", Range: "16.2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != src {
		t.Errorf("a declined rewrite changed the file:\n%s", got)
	}
	if res.VersionWritten {
		t.Error("an interpolated image must not receive the version")
	}
	if got := names(res.Skipped); !slices.Equal(got, []string{"postgres", "redis"}) {
		t.Errorf("Skipped = %v", got)
	}
}

func TestComposeRefusesAnIllegalTag(t *testing.T) {
	src := "services:\n  api:\n    build: .\n    image: ghcr.io/acme/api:1.0.0\n"
	path := seed(t, "compose.yaml", src)
	if _, err := Rewrite(path, "^2.0.0", nil); err == nil {
		t.Error("writing an illegal tag as the version must be refused")
	}
	if got := read(t, path); got != src {
		t.Errorf("a refused rewrite changed the file: %q", got)
	}
}

func TestComposeEveryRecognisedName(t *testing.T) {
	for _, name := range []string{
		"compose.yaml", "compose.yml", "compose.override.yaml", "compose.override.yml",
		"docker-compose.yaml", "docker-compose.yml",
		"docker-compose.override.yaml", "docker-compose.override.yml",
	} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte("services:\n  api:\n    build: .\n    image: a/api:1.0.0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := Rewrite(path, "1.1.0", nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !res.VersionWritten {
			t.Errorf("%s: VersionWritten = false", name)
		}
	}
}

func TestDockerUnsupportedNeighbours(t *testing.T) {
	// The names that look like these formats without being them.
	for _, name := range []string{".dockerignore", "Dockerfile.md", "docker-compose.prod.yml"} {
		path := seed(t, name, "FROM redis:7.2\n")
		if _, err := Rewrite(path, "", nil); !errors.Is(err, ErrUnsupportedManifest) {
			t.Errorf("%s: err = %v, want ErrUnsupportedManifest", name, err)
		}
	}
}
