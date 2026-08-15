package scanner

import (
	"reflect"
	"slices"
	"testing"
)

func TestDockerfileReadsEveryImageReference(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Dockerfile", `# syntax=docker/dockerfile:1
# The builder pulls the workspace's own toolchain image.
FROM --platform=$BUILDPLATFORM ghcr.io/acme/toolchain:2.1.0 AS builder
RUN --mount=type=cache,target=/root/.cache \
    --mount=type=bind,from=ghcr.io/acme/protos:0.4.0,source=/protos,target=/protos \
    go build -o /app ./cmd/app

FROM ghcr.io/acme/base:1.2.3
COPY --from=builder /app /usr/local/bin/app
COPY --from=ghcr.io/acme/certs:3.0.0 /certs /etc/ssl/certs
COPY --from=0 /x /y
`)
	m := scanOne(t, dir)
	if m.Ecosystem != EcosystemDocker {
		t.Errorf("Ecosystem = %q, want %q", m.Ecosystem, EcosystemDocker)
	}
	// A Dockerfile names what it builds on the command line, never in the file.
	if m.Name != "" || m.Version != "" {
		t.Errorf("Dockerfile declared an identity: %q %q", m.Name, m.Version)
	}
	want := []DeclaredDep{
		{Name: "ghcr.io/acme/base", Range: "1.2.3"},
		{Name: "ghcr.io/acme/certs", Range: "3.0.0"},
		{Name: "ghcr.io/acme/protos", Range: "0.4.0"},
		{Name: "ghcr.io/acme/toolchain", Range: "2.1.0"},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("Deps = %+v, want %+v", m.Deps, want)
	}
}

func TestDockerfileSkipsWhatIsNotAnImage(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Dockerfile", `FROM scratch AS empty
FROM golang:1.25 AS builder
FROM builder AS test
FROM BUILDER AS lint
COPY --from=builder /a /b
COPY --from=test /c /d
COPY --from=2 /e /f
COPY --from=scratch /g /h
FROM golang:1.25 AS final
`)
	m := scanOne(t, dir)
	// scratch is a keyword, an alias points inside this file (whatever its
	// case), and a numeric --from names a stage by position. The one real
	// image is declared twice and counted once.
	want := []DeclaredDep{{Name: "golang", Range: "1.25"}}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("Deps = %+v, want %+v", m.Deps, want)
	}
}

func TestDockerfileAliasesOnlyShadowBelowThemselves(t *testing.T) {
	dir := t.TempDir()
	// "tools" is a real image on line one and a stage alias from line two on.
	// The builder resolves it the same way: the alias is in scope only after
	// it is defined.
	write(t, dir, "Dockerfile", `FROM tools:1.0 AS first
FROM alpine:3 AS tools
COPY --from=tools /a /b
`)
	m := scanOne(t, dir)
	want := []DeclaredDep{
		{Name: "alpine", Range: "3"},
		{Name: "tools", Range: "1.0"},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("Deps = %+v, want %+v", m.Deps, want)
	}
}

func TestDockerfileOddButLegalSpellings(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Dockerfile.dev", "  from   ghcr.io/acme/base:1.0   as   base\r\n"+
		"# a comment inside the continuation\n"+
		"copy \\\n"+
		"  # still the same instruction\n"+
		"  --from=ghcr.io/acme/tools:2.0 /a /b\n"+
		"FROM ${REGISTRY}/api:${TAG}\n"+
		"FROM redis@sha256:abc123\n"+
		"FROM postgres\n"+
		"FROM \\\n")
	m := scanOne(t, dir)
	want := []DeclaredDep{
		{Name: "${REGISTRY}/api", Range: "${TAG}"},
		{Name: "ghcr.io/acme/base", Range: "1.0"},
		{Name: "ghcr.io/acme/tools", Range: "2.0"},
		{Name: "postgres"},
		{Name: "redis"},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("Deps = %+v, want %+v", m.Deps, want)
	}
}

func TestDockerfileEmptyAndCommentOnly(t *testing.T) {
	for _, src := range []string{"", "\n\n\n", "# nothing but prose\n# and more\n"} {
		dir := t.TempDir()
		write(t, dir, "Dockerfile", src)
		m := scanOne(t, dir)
		if len(m.Deps) != 0 {
			t.Errorf("Deps = %+v for %q, want none", m.Deps, src)
		}
	}
}

func TestComposeIdentityIsTheServiceThatBuilds(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.yaml", `services:
  cache:
    image: redis:7.2
  db:
    image: postgres:16.1
  api:
    build:
      context: .
      tags:
        - ghcr.io/acme/api:1.4.2
        - docker.io/acme/api:1.4.2
    image: ghcr.io/acme/api:1.4.2
    ports:
      - "8080:80"
    environment:
      REDIS_URL: "redis:6379"
`)
	m := scanOne(t, dir)
	if m.Name != "ghcr.io/acme/api" || m.Version != "1.4.2" {
		t.Errorf("identity = %q@%q, want ghcr.io/acme/api@1.4.2", m.Name, m.Version)
	}
	// build.tags are names the built image is published under, not inputs, so
	// they are not dependencies. Neither is a port mapping that happens to
	// contain a colon, nor an environment value that looks like a reference.
	want := []DeclaredDep{
		{Name: "postgres", Range: "16.1"},
		{Name: "redis", Range: "7.2"},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("Deps = %+v, want %+v", m.Deps, want)
	}
}

func TestComposeIdentityFallsBackToTheCommonestImage(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "docker-compose.yml", `services:
  cache:
    image: redis:7.2
  worker:
    image: ghcr.io/acme/api:2.0.0
  web:
    image: ghcr.io/acme/api:2.0.0
`)
	m := scanOne(t, dir)
	// Nothing builds, so the image two services share wins over the one a
	// single service pulls.
	if m.Name != "ghcr.io/acme/api" || m.Version != "2.0.0" {
		t.Errorf("identity = %q@%q, want ghcr.io/acme/api@2.0.0", m.Name, m.Version)
	}
	want := []DeclaredDep{{Name: "redis", Range: "7.2"}}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("Deps = %+v, want %+v", m.Deps, want)
	}
}

func TestComposeWithoutAnIdentity(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.override.yaml", `services:
  cache:
    image: redis
  db:
    image: postgres
  proxy:
    image: ${PROXY_IMAGE}:${PROXY_TAG}
`)
	m := scanOne(t, dir)
	// No tagged, literal image anywhere: the file wires third-party services
	// together and declares nothing of its own.
	if m.Name != "" || m.Version != "" {
		t.Errorf("identity = %q@%q, want none", m.Name, m.Version)
	}
	want := []DeclaredDep{
		{Name: "${PROXY_IMAGE}", Range: "${PROXY_TAG}"},
		{Name: "postgres"},
		{Name: "redis"},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("Deps = %+v, want %+v", m.Deps, want)
	}
}

func TestComposeShapesThatAreNotServices(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.yaml", `name: acme
services:
  empty:
  listy:
    - not
    - a
    - service
  scalar: "nonsense"
  api:
    build: .
    image: ghcr.io/acme/api:1.0.0
volumes:
  data:
    image: not-a-service:9.9
`)
	m := scanOne(t, dir)
	if m.Name != "ghcr.io/acme/api" || m.Version != "1.0.0" {
		t.Errorf("identity = %q@%q, want ghcr.io/acme/api@1.0.0", m.Name, m.Version)
	}
	// A null service, a list and a scalar have no image to read, and an image
	// key outside the services block is not a service's.
	if len(m.Deps) != 0 {
		t.Errorf("Deps = %+v, want none", m.Deps)
	}
	// Each of the three is a declared service the parser could not read, and
	// the manifest says so, in name order.
	want := []string{
		"service empty: not a mapping",
		"service listy: not a mapping",
		"service scalar: not a mapping",
	}
	if !slices.Equal(m.Dropped, want) {
		t.Errorf("Dropped = %+v, want %+v", m.Dropped, want)
	}
}

func TestComposeMalformed(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "compose.yaml", "services:\n  api:\n   image: [unterminated\n")
	if _, err := New().Scan(t.Context(), dir); err == nil {
		t.Error("a compose file that is not YAML must report an error")
	}
}

func TestComposeEmpty(t *testing.T) {
	for _, src := range []string{"", "services:\n", "# just a comment\n", "[]\n"} {
		dir := t.TempDir()
		write(t, dir, "compose.yaml", src)
		mans, err := New().Scan(t.Context(), dir)
		if src == "[]\n" {
			// A sequence document cannot decode into the services mapping.
			if err == nil {
				t.Error("a non-mapping compose document must report an error")
			}
			continue
		}
		if err != nil {
			t.Fatalf("Scan(%q): %v", src, err)
		}
		if len(mans) != 1 || mans[0].Name != "" || len(mans[0].Deps) != 0 {
			t.Errorf("Scan(%q) = %+v, want one empty manifest", src, mans)
		}
	}
}

func TestDockerfileCommandFlagsAreNotImages(t *testing.T) {
	dir := t.TempDir()
	// A --from past the instruction's own arguments is a flag of the command
	// being run, not a reference to an image.
	write(t, dir, "Dockerfile", `FROM a/base:1.0
RUN mytool sync --from=a/other:9.9 --to=/out
COPY --from=a/tools:2.0 /x /y
COPY /z /w --from=a/late:3.0
`)
	m := scanOne(t, dir)
	want := []DeclaredDep{
		{Name: "a/base", Range: "1.0"},
		{Name: "a/tools", Range: "2.0"},
	}
	if !reflect.DeepEqual(m.Deps, want) {
		t.Errorf("Deps = %+v, want %+v", m.Deps, want)
	}
}
