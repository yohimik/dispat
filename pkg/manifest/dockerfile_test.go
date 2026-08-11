package manifest

import (
	"reflect"
	"strings"
	"testing"
)

// refTexts is the references a Dockerfile names, in the order they appear.
func refTexts(src string) []string {
	refs := DockerfileRefs(strings.Split(src, "\n"))
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Text)
	}
	return out
}

func TestDockerfileRefsFindsEveryInstruction(t *testing.T) {
	src := "# syntax=docker/dockerfile:1\n" +
		"ARG GO_VERSION=1.25\n" +
		"FROM --platform=$BUILDPLATFORM ghcr.io/acme/toolchain:2.1.0 AS builder\n" +
		"RUN --mount=type=cache,target=/root/.cache \\\n" +
		"    --mount=type=bind,from=ghcr.io/acme/protos:0.4.0,source=/p,target=/p \\\n" +
		"    go build -o /app ./cmd/app\n" +
		"\n" +
		"FROM ghcr.io/acme/base:1.2.3\n" +
		"COPY --from=builder /app /usr/local/bin/app\n" +
		"COPY --from=ghcr.io/acme/certs:3.0.0 /certs /etc/ssl/certs\n"
	want := []string{
		"ghcr.io/acme/toolchain:2.1.0",
		"ghcr.io/acme/protos:0.4.0",
		"ghcr.io/acme/base:1.2.3",
		"ghcr.io/acme/certs:3.0.0",
	}
	if got := refTexts(src); !reflect.DeepEqual(got, want) {
		t.Errorf("DockerfileRefs = %v, want %v", got, want)
	}
}

func TestDockerfileRefsSkipsWhatIsNotAnImage(t *testing.T) {
	src := "FROM scratch AS empty\n" +
		"FROM golang:1.25 AS builder\n" +
		"FROM builder AS test\n" +
		"FROM BUILDER AS lint\n" +
		"COPY --from=builder /a /b\n" +
		"COPY --from=test /c /d\n" +
		"COPY --from=2 /e /f\n" +
		"COPY --from=scratch /g /h\n" +
		"COPY --from= /i /j\n" +
		"COPY --chown=root:root /k /l\n" +
		"RUN mytool --from=a/b:1.0 build\n" +
		"RUN --mount=type=cache,target=/x make\n" +
		"RUN --mount=from=,target=/x make\n" +
		"ENV FROM=redis:7.2\n"
	// scratch is a keyword, an alias points inside this file whatever its case,
	// a numeric --from names a stage by position, an empty --from names
	// nothing, and ENV is not an instruction that pulls an image.
	if got := refTexts(src); !reflect.DeepEqual(got, []string{"golang:1.25"}) {
		t.Errorf("DockerfileRefs = %v, want just golang:1.25", got)
	}
}

func TestDockerfileRefsAliasScopeStartsAtItsDefinition(t *testing.T) {
	// "tools" is a real image on line one and a stage alias from line two on.
	// The builder resolves it exactly this way.
	src := "FROM tools:1.0 AS first\nFROM alpine:3 AS tools\nCOPY --from=tools /a /b\n"
	want := []string{"tools:1.0", "alpine:3"}
	if got := refTexts(src); !reflect.DeepEqual(got, want) {
		t.Errorf("DockerfileRefs = %v, want %v", got, want)
	}
}

func TestDockerfileRefsOddButLegalSpellings(t *testing.T) {
	src := "  from   ghcr.io/acme/base:1.0   as   base\r\n" +
		"copy \\\n" +
		"  # a comment inside the continuation\n" +
		"  --from=ghcr.io/acme/tools:2.0 /a /b\n" +
		"FROM ${REGISTRY}/api:${TAG}\n" +
		"FROM redis@sha256:abc123\n" +
		"FROM postgres\n" +
		"FROM \\\n"
	want := []string{
		"ghcr.io/acme/base:1.0",
		"ghcr.io/acme/tools:2.0",
		"${REGISTRY}/api:${TAG}",
		"redis@sha256:abc123",
		"postgres",
	}
	if got := refTexts(src); !reflect.DeepEqual(got, want) {
		t.Errorf("DockerfileRefs = %v, want %v", got, want)
	}
}

func TestDockerfileRefsOffsetsSelectTheReference(t *testing.T) {
	// The offsets are what the writer splices through, so each must select its
	// own reference out of its own line — including one buried in a mount
	// option and one preceded by a platform flag.
	src := "FROM --platform=linux/amd64 a/one:1.0 AS x\r\n" +
		"RUN --mount=type=bind,from=a/two:2.0,target=/t --mount=from=a/three:3.0 make\n"
	lines := strings.Split(src, "\n")
	refs := DockerfileRefs(lines)
	if len(refs) != 3 {
		t.Fatalf("DockerfileRefs found %d references, want 3: %+v", len(refs), refs)
	}
	for _, r := range refs {
		if got := lines[r.Line][r.Start:r.End]; got != r.Text {
			t.Errorf("line %d offsets %d:%d select %q, want %q", r.Line, r.Start, r.End, got, r.Text)
		}
	}
	if refs[1].Line != 1 || refs[2].Line != 1 {
		t.Errorf("the two mount references should both be on line 1: %+v", refs)
	}
	if refs[1].Start >= refs[2].Start {
		t.Errorf("references on one line must come out in order: %+v", refs)
	}
}

func TestDockerfileRefsEmptyInput(t *testing.T) {
	// The last two are lines that hold something without holding a token: a
	// bare continuation, and one indented.
	for _, src := range []string{
		"", "\n\n\n", "# nothing but prose\n", "   \n\t\n", "FROM\n", "FROM \\\n",
		"\\\n", "  \\\n  \\\n",
	} {
		if got := refTexts(src); len(got) != 0 {
			t.Errorf("DockerfileRefs(%q) = %v, want none", src, got)
		}
	}
}
