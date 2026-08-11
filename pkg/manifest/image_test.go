package manifest

import (
	"strings"
	"testing"
)

func TestParseImageRefSplitsEveryShape(t *testing.T) {
	for _, tc := range []struct {
		ref                    string
		repo, tag, digest      string
		hasTag, pinned, interp bool
	}{
		// The plain cases.
		{ref: "redis", repo: "redis"},
		{ref: "redis:7.2", repo: "redis", tag: "7.2", hasTag: true},
		{ref: "ghcr.io/acme/api:1.4.2", repo: "ghcr.io/acme/api", tag: "1.4.2", hasTag: true},
		{ref: "ghcr.io/acme/api", repo: "ghcr.io/acme/api"},

		// A registry port is a colon before the last slash, a tag is a colon
		// after it. Getting this backwards is the classic bug in this parser.
		{ref: "localhost:5000/api:1.0", repo: "localhost:5000/api", tag: "1.0", hasTag: true},
		{ref: "localhost:5000/api", repo: "localhost:5000/api"},
		{ref: "registry.example.com:5000/team/api:2.0.0", repo: "registry.example.com:5000/team/api", tag: "2.0.0", hasTag: true},

		// Digests, with and without a tag beside them.
		{ref: "redis@sha256:abc123", repo: "redis", digest: "sha256:abc123", pinned: true},
		{ref: "redis:7.2@sha256:abc123", repo: "redis", tag: "7.2", digest: "sha256:abc123", hasTag: true, pinned: true},
		{ref: "localhost:5000/api@sha256:abc", repo: "localhost:5000/api", digest: "sha256:abc", pinned: true},

		// Values resolved outside the file.
		{ref: "${BASE}:${TAG}", repo: "${BASE}", tag: "${TAG}", hasTag: true, interp: true},
		{ref: "${IMAGE}", repo: "${IMAGE}", interp: true},
		{ref: "ghcr.io/acme/api:$VERSION", repo: "ghcr.io/acme/api", tag: "$VERSION", hasTag: true, interp: true},

		// Malformed input comes back inert rather than rejected.
		{ref: ""},
		{ref: "redis:", repo: "redis"},
		{ref: "@sha256:abc", digest: "sha256:abc", pinned: true},
		{ref: "redis@", repo: "redis"},
	} {
		got := ParseImageRef(tc.ref)
		if got.Repository != tc.repo || got.Tag != tc.tag || got.Digest != tc.digest {
			t.Errorf("ParseImageRef(%q) = %q/%q/%q, want %q/%q/%q",
				tc.ref, got.Repository, got.Tag, got.Digest, tc.repo, tc.tag, tc.digest)
		}
		if got.HasTag() != tc.hasTag {
			t.Errorf("ParseImageRef(%q).HasTag() = %v, want %v", tc.ref, got.HasTag(), tc.hasTag)
		}
		if got.Pinned() != tc.pinned {
			t.Errorf("ParseImageRef(%q).Pinned() = %v, want %v", tc.ref, got.Pinned(), tc.pinned)
		}
		if got.Interpolated() != tc.interp {
			t.Errorf("ParseImageRef(%q).Interpolated() = %v, want %v", tc.ref, got.Interpolated(), tc.interp)
		}
	}
}

func TestParseImageRefOffsetsPointAtTheTag(t *testing.T) {
	// The offsets are what a writer splices through, so they must index the
	// reference it was given, digest and all.
	for _, ref := range []string{
		"redis:7.2", "ghcr.io/acme/api:1.4.2", "localhost:5000/api:1.0",
		"redis:7.2@sha256:abc123",
	} {
		got := ParseImageRef(ref)
		if !got.HasTag() {
			t.Fatalf("ParseImageRef(%q) found no tag", ref)
		}
		if ref[got.TagStart:got.TagEnd] != got.Tag {
			t.Errorf("ParseImageRef(%q) offsets %d:%d select %q, want %q",
				ref, got.TagStart, got.TagEnd, ref[got.TagStart:got.TagEnd], got.Tag)
		}
		// Splicing through the offsets must reproduce the reference with only
		// the tag changed.
		spliced := ref[:got.TagStart] + "9.9.9" + ref[got.TagEnd:]
		want := strings.Replace(ref, ":"+got.Tag, ":9.9.9", 1)
		if spliced != want {
			t.Errorf("splicing %q gave %q, want %q", ref, spliced, want)
		}
	}
	// No tag, no span: a writer has nothing to aim at and must not invent one.
	for _, ref := range []string{"redis", "redis:", "", "redis@sha256:abc"} {
		got := ParseImageRef(ref)
		if got.TagStart != -1 || got.TagEnd != -1 {
			t.Errorf("ParseImageRef(%q) reported a tag span %d:%d", ref, got.TagStart, got.TagEnd)
		}
	}
}

func TestValidTag(t *testing.T) {
	for _, tag := range []string{
		"1.2.3", "latest", "v1.2.3", "1.2.3-alpine", "_odd", "A1", "1.0.0-rc.1",
		strings.Repeat("a", 128),
	} {
		if !ValidTag(tag) {
			t.Errorf("ValidTag(%q) = false, want true", tag)
		}
	}
	for _, tag := range []string{
		"", ".1.2.3", "-1.2.3", "1.2.3 ", "a/b", "a:b", "^1.2.3", "1.2.3\n",
		strings.Repeat("a", 129),
	} {
		if ValidTag(tag) {
			t.Errorf("ValidTag(%q) = true, want false", tag)
		}
	}
}

func TestIsDockerfileNames(t *testing.T) {
	for _, name := range []string{
		"Dockerfile", "dockerfile", "DOCKERFILE", "Dockerfile.dev",
		"Dockerfile.production", "api.Dockerfile", "api.dockerfile",
		"Containerfile", "Containerfile.dev", "worker.Containerfile",
	} {
		if !IsDockerfile(name) {
			t.Errorf("IsDockerfile(%q) = false, want true", name)
		}
	}
	for _, name := range []string{
		"", ".dockerfile", ".dockerignore", "Dockerfile.md", "Dockerfile.txt",
		"Dockerfile.rst", "Dockerfile.notes.md", "Dockerfile.", "Dockerfiles",
		"dockerfile-notes.md", "compose.yaml", ".containerfile",
	} {
		if IsDockerfile(name) {
			t.Errorf("IsDockerfile(%q) = true, want false", name)
		}
	}
}
