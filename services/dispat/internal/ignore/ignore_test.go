package ignore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The matcher is the whole feature's vocabulary, so every pattern form is
// exercised against the paths a reader would expect it to reach and the ones
// they would expect it to leave alone.

func compile(t *testing.T, patterns ...string) *Rules {
	t.Helper()
	r, err := Compile(patterns)
	require.NoError(t, err)
	return r
}

func TestMatchPatternForms(t *testing.T) {
	for _, tc := range []struct {
		name     string
		patterns []string
		hits     []string
		misses   []string
	}{
		{
			name:     "a bare name reaches any depth",
			patterns: []string{"README.md"},
			hits:     []string{"README.md", "docs/README.md", "a/b/README.md"},
			misses:   []string{"README.mdx", "readme.md", "docs/CHANGELOG.md"},
		},
		{
			name:     "a bare glob reaches any depth too",
			patterns: []string{"*.md"},
			hits:     []string{"README.md", "docs/guide.md", "a/b/c.md"},
			misses:   []string{"README.txt", "md"},
		},
		{
			name:     "a path pattern is anchored to the declaring folder",
			patterns: []string{"docs/guide.md"},
			hits:     []string{"docs/guide.md"},
			misses:   []string{"guide.md", "src/docs/guide.md"},
		},
		{
			name:     "a star crosses separators, which is what makes one enough",
			patterns: []string{"docs/*"},
			hits:     []string{"docs/guide.md", "docs/api/v1/index.md"},
			misses:   []string{"docs", "src/docs/guide.md"},
		},
		{
			name:     "a trailing slash covers the folder and everything under it",
			patterns: []string{"testdata/"},
			hits:     []string{"testdata", "testdata/a.json", "testdata/deep/b.json", "src/testdata/a.json"},
			misses:   []string{"testdata2/a.json", "a/testdata.json"},
		},
		{
			name:     "naming a path anchors the folder to the declaring level",
			patterns: []string{"src/testdata/"},
			hits:     []string{"src/testdata/a.json"},
			misses:   []string{"testdata/a.json", "a/src/testdata/b.json"},
		},
		{
			name:     "a leading slash reads as from here, not from the root",
			patterns: []string{"/docs/guide.md"},
			hits:     []string{"docs/guide.md"},
			misses:   []string{"a/docs/guide.md"},
		},
		{
			name:     "a leading ./ is the same thing said differently",
			patterns: []string{"./docs/guide.md"},
			hits:     []string{"docs/guide.md"},
			misses:   []string{"a/docs/guide.md"},
		},
		{
			name:     "the last pattern to match decides",
			patterns: []string{"docs/*", "!docs/api.md"},
			hits:     []string{"docs/guide.md", "docs/api/v1.md"},
			misses:   []string{"docs/api.md"},
		},
		{
			name:     "and it decides in both directions",
			patterns: []string{"!docs/api.md", "docs/*"},
			hits:     []string{"docs/api.md", "docs/guide.md"},
		},
		{
			name:     "an escaped bang is a literal one",
			patterns: []string{`\!urgent.md`},
			hits:     []string{"!urgent.md"},
			misses:   []string{"urgent.md"},
		},
		{
			name:     "comments and blank lines are not patterns",
			patterns: []string{"# docs are not code", "", "   ", "docs/"},
			hits:     []string{"docs/guide.md"},
			misses:   []string{"src/main.go", "# docs are not code"},
		},
		{
			name:     "surrounding whitespace is not part of the pattern",
			patterns: []string{"  docs/  "},
			hits:     []string{"docs/guide.md"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := compile(t, tc.patterns...)
			for _, hit := range tc.hits {
				assert.True(t, r.Match(hit), "%q should be ignored", hit)
			}
			for _, miss := range tc.misses {
				assert.False(t, r.Match(miss), "%q should not be ignored", miss)
			}
		})
	}
}

// TestCompileNothingToMatch: a level that declares no usable pattern compiles
// to nil, which every caller treats as "says nothing" without a branch of its
// own.
func TestCompileNothingToMatch(t *testing.T) {
	for _, patterns := range [][]string{nil, {}, {""}, {"# just a comment"}, {"  "}} {
		r, err := Compile(patterns)
		require.NoError(t, err)
		assert.Nil(t, r, "patterns %v", patterns)
		assert.False(t, r.Match("anything"), "a nil ruleset is safe to ask")
	}
	matched, ignored := (*Rules)(nil).Decide("anything")
	assert.False(t, matched)
	assert.False(t, ignored)
}

// TestCompileRefusesWhatCannotBeCarriedOut: a pattern the author clearly
// meant something by, that means nothing as written, is a config error rather
// than a line that silently does nothing.
func TestCompileRefusesWhatCannotBeCarriedOut(t *testing.T) {
	_, err := Compile([]string{"docs/", "!"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "re-includes nothing")

	_, err = Compile([]string{"/"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "names nothing")
}

// TestMatchEmptyPath: the path of the declaring folder itself is not one of
// its files, so nothing matches it.
func TestMatchEmptyPath(t *testing.T) {
	assert.False(t, compile(t, "*").Match(""))
}

func TestRelative(t *testing.T) {
	for _, tc := range []struct {
		dir, file, want string
		inside          bool
	}{
		{"/r/pkg", "/r/pkg/src/a.go", "src/a.go", true},
		{"/r/pkg/", "/r/pkg/a.go", "a.go", true},
		{"/r/pkg", "/r/pkg", "", false},
		// The boundary is respected: a sibling whose name starts the same is
		// not inside.
		{"/r/pkg", "/r/pkg-extra/a.go", "", false},
		{"/r/pkg", "/r/other/a.go", "", false},
		// A root-level layer owns everything below it.
		{"/", "/r/a.go", "r/a.go", true},
		{".", "r/a.go", "r/a.go", true},
	} {
		rel, ok := Relative(tc.dir, tc.file)
		assert.Equal(t, tc.inside, ok, "%s in %s", tc.file, tc.dir)
		assert.Equal(t, tc.want, rel, "%s in %s", tc.file, tc.dir)
	}
}

// TestChainNearestLevelDecides: the levels concatenate, and the nearest one
// with something to say wins — including a package re-including what the
// repository excluded, which is the only direction that needs the chain at
// all.
func TestChainNearestLevelDecides(t *testing.T) {
	chain := Chain{
		{Dir: "/r", Rules: compile(t, "*.md")},
		{Dir: "/r/packages", Rules: compile(t, "fixtures/")},
		{Dir: "/r/packages/core", Rules: compile(t, "!README.md", "scratch/")},
	}

	assert.True(t, chain.Ignores("/r/packages/core/docs/guide.md"), "the root level reaches down")
	assert.False(t, chain.Ignores("/r/packages/core/README.md"), "the package lifts it")
	assert.True(t, chain.Ignores("/r/packages/core/scratch/x.go"), "and adds its own")
	assert.True(t, chain.Ignores("/r/packages/utils/fixtures/a.json"), "the space level reaches its own packages")
	assert.False(t, chain.Ignores("/r/packages/core/main.go"), "everything else counts")

	// A sibling package's README is still ignored: only the layer belonging to
	// the package can lift the exclusion, and this chain is core's.
	assert.True(t, chain.Ignores("/r/packages/utils/README.md"))
}

// TestChainOutsideItsLayers: a file under none of the chain's folders is
// nobody's business here, and asking about it is not an error.
func TestChainOutsideItsLayers(t *testing.T) {
	chain := Chain{{Dir: "/r/packages/core", Rules: compile(t, "*")}}
	assert.False(t, chain.Ignores("/elsewhere/a.go"))
	assert.False(t, Chain(nil).Ignores("/r/a.go"))
}
