package gitx

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
)

// TestReleaseReaderIndexIsEveryReaderTried checks the index against the
// definition it replaces: every package's reader tried in order, the first
// that parses the name winning. The formats spell the name in the lead, in the
// tail, between two placeholders and not at all, and include formats that do
// not compile; the packages repeat names and formats, and the names are real
// renders and their near misses.
func TestReleaseReaderIndexIsEveryReaderTried(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	formats := []TagFormat{
		"", "{name}@{version}", "v{version}", "{version}", "packages/{name}/v{version}", "v{name}@{version}",
		"{name}-v{version}-{channel}.{counter}", "{version}+{name}", "{version}-{name}-{channel}.{counter}",
		"{name}{version}", "{version}{channel}{counter}", "{name}/{version}{name}", "v{major}", "{name}",
	}
	names := []string{"core", "core-ui", "co", "@acme/core", "v", "pkg@1", "1"}
	versions := []ccme.Version{
		{Major: 1, Minor: 4, Patch: 2},
		{Major: 1, Minor: 4, Patch: 2, Prerelease: []string{"beta", "4"}},
		{Major: 0, Minor: 1},
	}
	var tags []string
	for _, format := range formats {
		for _, name := range names {
			for _, v := range versions {
				tag := format.Render(name, v)
				tags = append(tags, tag, tag+"x", "x"+tag)
				if len(tag) > 1 {
					tags = append(tags, tag[:len(tag)-1], tag[1:])
				}
			}
		}
	}
	matched := 0
	for round := 0; round < 60; round++ {
		packages := make([]PackageTagFormat, 1+rng.Intn(12))
		readers := make([]VersionReader, len(packages))
		for i := range packages {
			packages[i] = PackageTagFormat{
				Package: names[rng.Intn(len(names))],
				Format:  formats[rng.Intn(len(formats))],
			}
			readers[i] = packages[i].Format.Reader(packages[i].Package)
		}
		index := NewReleaseReaderIndex(packages)
		for _, tag := range tags {
			want := -1
			for i, r := range readers {
				if _, ok := r.ParseVersion(tag); ok {
					want = i
					break
				}
			}
			got, ok := index.FindFirstReader(tag)
			require.Equal(t, want >= 0, ok, "tag %q against %v", tag, packages)
			if ok {
				matched++
				require.Equal(t, want, got, "tag %q against %v", tag, packages)
			}
		}
	}
	assert.Greater(t, matched, 1000, "the comparison must exercise names that do read")
}

func TestReleaseReaderIndexZeroValueReadsNothing(t *testing.T) {
	_, ok := ReleaseReaderIndex{}.FindFirstReader("core@1.0.0")
	assert.False(t, ok)
}

func TestAliasRendererZeroValueRendersTheEmptyFormat(t *testing.T) {
	v := ccme.Version{Major: 2, Minor: 3, Patch: 4}
	assert.Equal(t, AliasFormat("").Render("core", v), AliasRenderer{}.Render("core", v))
}
