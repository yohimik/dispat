package gitx

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAliasIndexIsEveryMatcherTried checks the prefix dispatch against the
// definition it replaces, every matcher tried for every name. The formats
// cover an alias opening with its package name, with shared literal text, with
// a placeholder (an empty prefix, a candidate for every name), and with a
// spelled prerelease whose stable shape opens differently from its full one;
// the names are real renders and their near misses.
func TestAliasIndexIsEveryMatcherTried(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	matched := 0
	formats := []string{
		"v{major}", "v{major}.{minor}", "{name}-v{major}", "{name}@{major}", "{name}/v{major}.{minor}",
		"{major}.x", "release-{name}-{major}", "{name}-v{version}", "{channel}.{counter}-{name}-v{major}",
		"{name}-{major}-{channel}.{counter}", "latest",
	}
	packages := []string{"core", "core-ui", "co", "@acme/core", "ui", "v", "release"}
	for round := 0; round < 40; round++ {
		var matchers []AliasMatcher
		for n := 1 + rng.Intn(10); n > 0; n-- {
			matchers = append(matchers,
				AliasFormat(formats[rng.Intn(len(formats))]).Matcher(packages[rng.Intn(len(packages))]))
		}
		index := NewAliasIndex(matchers)
		require.Equal(t, len(matchers), index.Len())

		var names []string
		for _, pkg := range packages {
			for _, tail := range []string{"1", "12", "1.2", "1.x", "1.2.3", "1.2.3-beta.0", "x", ""} {
				for _, lead := range []string{"v", pkg + "-v", pkg + "@", pkg + "/v", "release-" + pkg + "-",
					"beta.1-" + pkg + "-v", pkg + "-", ""} {
					names = append(names, lead+tail, lead+tail+"-beta.1")
				}
			}
		}
		names = append(names, "latest", "", "V1", fmt.Sprint(rng.Int()))
		for _, name := range names {
			want := false
			for _, m := range matchers {
				want = want || m.IsMatch(name)
			}
			assert.Equalf(t, want, index.IsMatch(name), "round %d: %q", round, name)
			if want {
				matched++
			}
		}
	}
	assert.Greater(t, matched, 500, "the names have to exercise matches, not only misses")
	assert.False(t, AliasIndex{}.IsMatch("v1"), "the zero index matches nothing")
}
