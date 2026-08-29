package model

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersioningShared(t *testing.T) {
	assert.False(t, VersioningIndependent.Shared())
	assert.False(t, Versioning("").Shared(), "the zero value is independent")
	assert.True(t, VersioningFixed.Shared())
	assert.True(t, VersioningFixedSparse.Shared())
	assert.True(t, VersioningFixedMajorMinor.Shared())
	assert.True(t, VersioningFixedMajorMinorSparse.Shared())
	assert.True(t, VersioningFixedMajor.Shared())
	assert.True(t, VersioningFixedMajorSparse.Shared())
}

// TestVersioningDepthAndSparseness pins the two numbers every other layer
// reads a mode through: how much of the version the group holds in common,
// and whether an unchanged member rides along or stays behind.
func TestVersioningDepthAndSparseness(t *testing.T) {
	cases := []struct {
		mode   Versioning
		depth  int
		sparse bool
	}{
		{VersioningIndependent, 0, false},
		{Versioning(""), 0, false},
		{Versioning("nonsense"), 0, false},
		{VersioningFixed, 3, false},
		{VersioningFixedSparse, 3, true},
		{VersioningFixedMajorMinor, 2, false},
		{VersioningFixedMajorMinorSparse, 2, true},
		{VersioningFixedMajor, 1, false},
		{VersioningFixedMajorSparse, 1, true},
	}
	for _, c := range cases {
		t.Run(string(c.mode), func(t *testing.T) {
			assert.Equal(t, c.depth, c.mode.SharedDepth())
			assert.Equal(t, c.sparse, c.mode.Sparse())
			assert.Equal(t, c.depth > 0, c.mode.Shared(), "sharing is having a depth")
		})
	}
	assert.Equal(t, SharedVersioningDepth, VersioningFixed.SharedDepth(),
		"the full depth is what fixed shares")
}

// TestSparseModesPairWithAPlainMode fences the naming contract the
// configuration relies on: every sparse mode is a plain mode plus the Sparse
// suffix, and the two agree on everything but sparseness.
func TestSparseModesPairWithAPlainMode(t *testing.T) {
	pairs := map[Versioning]Versioning{
		VersioningFixed:           VersioningFixedSparse,
		VersioningFixedMajorMinor: VersioningFixedMajorMinorSparse,
		VersioningFixedMajor:      VersioningFixedMajorSparse,
	}
	for plain, sparse := range pairs {
		assert.Equal(t, string(plain)+"Sparse", string(sparse))
		assert.Equal(t, plain.SharedDepth(), sparse.SharedDepth(), "a pair shares one depth")
		assert.False(t, plain.Sparse())
		assert.True(t, sparse.Sparse())
	}
}

func TestDepKindString(t *testing.T) {
	assert.Equal(t, "dependencies", KindDependencies.String(), "the zero value spells itself out")
	assert.Equal(t, "devDependencies", KindDevDependencies.String())
	assert.Equal(t, "peerDependencies", KindPeerDependencies.String())
}

// TestChannelsAdmit pins the vocabulary every channel restriction is read
// with: nothing named admits everything, "stable" is the stable line, "*" is
// any prerelease channel and nothing else, and a name is a name.
func TestChannelsAdmit(t *testing.T) {
	cases := []struct {
		name     string
		channels []string
		channel  string
		want     bool
	}{
		{"nothing named admits a stable release", nil, "stable", true},
		{"nothing named admits a prerelease", nil, "beta", true},
		{"an empty list is nothing named", []string{}, "beta", true},
		{"stable admits the stable line", []string{"stable"}, "stable", true},
		{"stable holds a prerelease back", []string{"stable"}, "beta", false},
		{"the wildcard admits any prerelease", []string{"*"}, "beta", true},
		{"the wildcard admits another prerelease", []string{"*"}, "rc", true},
		{"the wildcard is not the stable line", []string{"*"}, "stable", false},
		{"a name admits its own channel", []string{"beta"}, "beta", true},
		{"a name admits no other", []string{"beta"}, "rc", false},
		{"a name is matched however it is spelled", []string{"BeTa"}, "beta", true},
		{"a name does not admit the stable line", []string{"beta"}, "stable", false},
		{"several names admit any of them", []string{"beta", "rc"}, "rc", true},
		{"stable and the wildcard together admit everything", []string{"stable", "*"}, "beta", true},
		{"stable and the wildcard admit the stable line too", []string{"stable", "*"}, "stable", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ChannelsAdmit(c.channels, c.channel))
		})
	}
}

// TestRecordSpecsGateOnChannels pins the two-part gate both recorders read:
// the policy must be enabled, and the release's channel must be one it records
// on. The specs answer identically, which is what lets the changelog and the
// GitHub release be configured the same way.
func TestRecordSpecsGateOnChannels(t *testing.T) {
	cases := []struct {
		name     string
		enabled  bool
		channels []string
		channel  string
		want     bool
	}{
		{"every channel, a stable release", true, nil, "stable", true},
		{"every channel, a prerelease", true, nil, "beta", true},
		{"stables only, a stable release", true, []string{"stable"}, "stable", true},
		{"stables only, a prerelease", true, []string{"stable"}, "beta", false},
		{"one named channel", true, []string{"beta"}, "beta", true},
		{"one named channel, another arrives", true, []string{"beta"}, "rc", false},
		{"prereleases only, a stable release", true, []string{"*"}, "stable", false},
		{"disabled outright", false, nil, "stable", false},
		{"disabled beats the channels", false, []string{"stable"}, "stable", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl := ChangelogSpec{Enabled: c.enabled, Channels: c.channels}
			gh := GitHubSpec{Enabled: c.enabled, Channels: c.channels}
			assert.Equal(t, c.want, cl.Records(c.channel))
			assert.Equal(t, c.want, gh.Records(c.channel), "both specs answer alike")
		})
	}
}

// TestPackageScopeDir: src narrows which folder a package's changes must sit
// under, and an unset src leaves the package folder itself.
func TestPackageScopeDir(t *testing.T) {
	assert.Equal(t, filepath.Join("packages", "core"),
		(&Package{Dir: filepath.Join("packages", "core")}).ScopeDir(),
		"without src the whole package folder counts")
	assert.Equal(t, filepath.Join("packages", "core", "lib"),
		(&Package{Dir: filepath.Join("packages", "core"), Src: "lib"}).ScopeDir())
	assert.Equal(t, filepath.Join("packages", "core", "src", "main"),
		(&Package{Dir: filepath.Join("packages", "core"), Src: "src/main"}).ScopeDir(),
		"a slash-separated src is a path on every platform")
}

// TestPackageVersionGroupName: a package belongs to a group only when its
// versioning shares one, and the group is the one configuration named or the
// space's own when it named none.
func TestPackageVersionGroupName(t *testing.T) {
	cases := []struct {
		name string
		pkg  *Package
		want string
	}{
		{"nil package", nil, ""},
		{"no space", &Package{Name: "tool"}, ""},
		{"independent space", &Package{Space: &Space{Name: "libs"}}, ""},
		{"the space's own group", &Package{Space: &Space{Name: "libs", Versioning: VersioningFixed}}, "libs"},
		{"a named group", &Package{Space: &Space{Name: "libs", Versioning: VersioningFixedMajor,
			VersionGroup: "shared"}}, "shared"},
		{"a named group an independent space ignores",
			&Package{Space: &Space{Name: "libs", VersionGroup: "shared"}}, ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, c.pkg.VersionGroupName(), c.name)
	}
}

// TestGitHubSpecKeyDistinguishesPolicies: the key is what decides whether two
// packages share a releaser, so every field a releaser is built from has to
// change it — the entry format included, since it shapes every body sent.
func TestGitHubSpecKeyDistinguishesPolicies(t *testing.T) {
	base := GitHubSpec{
		Enabled: true, Owner: "acme", Repo: "mono",
		APIURL: "https://api.github.com", TokenEnv: "GITHUB_TOKEN",
		Format: RecordFormat{
			FeaturesTitle: "Features",
			Header:        []EntryLine{{Line: []string{"a"}, Package: []string{"core"}}},
		},
	}
	assert.Equal(t, base.Key(), base.Key(), "the same policy keys the same twice")

	cases := map[string]func(s *GitHubSpec){
		"enabled":     func(s *GitHubSpec) { s.Enabled = false },
		"channels":    func(s *GitHubSpec) { s.Channels = []string{"stable"} },
		"allPackages": func(s *GitHubSpec) { s.AllPackages = true },
		"draft":       func(s *GitHubSpec) { s.Draft = true },
		"owner":       func(s *GitHubSpec) { s.Owner = "other" },
		"repo":        func(s *GitHubSpec) { s.Repo = "other" },
		"apiUrl":      func(s *GitHubSpec) { s.APIURL = "https://ghe" },
		"tokenEnv":    func(s *GitHubSpec) { s.TokenEnv = "OTHER" },
		"dateFormat":  func(s *GitHubSpec) { s.Format.DateFormat = "2006" },
		"titles":      func(s *GitHubSpec) { s.Format.FeaturesTitle = "Added" },
		"releaseName": func(s *GitHubSpec) { s.Format.ReleaseName = "v1" },
		"header text": func(s *GitHubSpec) { s.Format.Header = []EntryLine{{Line: []string{"b"}}} },
		"header filter": func(s *GitHubSpec) {
			s.Format.Header = []EntryLine{{Line: []string{"a"}, Package: []string{"other"}}}
		},
		"header length": func(s *GitHubSpec) { s.Format.Header = nil },
		"footer":        func(s *GitHubSpec) { s.Format.Footer = []EntryLine{{Line: []string{"a"}}} },
		"a line's channels": func(s *GitHubSpec) {
			s.Format.Header = []EntryLine{{Line: []string{"a"}, Package: []string{"core"},
				Channels: []string{"beta"}}}
		},
		// Each of the six authors fields on its own. A releaser is shared
		// between the packages whose keys match and carries the format it
		// renders every body with, so a field missing from the key would give
		// one package the other's attribution, silently and only when the two
		// happened to differ.
		"authors placement": func(s *GitHubSpec) { s.Format.AuthorsPlacement = "section" },
		"authors format":    func(s *GitHubSpec) { s.Format.AuthorsFormat = "username" },
		"authors commits":   func(s *GitHubSpec) { s.Format.AuthorsCommits = "all" },
		"authors include":   func(s *GitHubSpec) { s.Format.AuthorsInclude = []string{"a*"} },
		"authors exclude":   func(s *GitHubSpec) { s.Format.AuthorsExclude = []string{"*bot*"} },
		"authors title":     func(s *GitHubSpec) { s.Format.AuthorsTitle = "Contributors" },
	}
	for name, change := range cases {
		other := base
		other.Format.Header = append([]EntryLine(nil), base.Format.Header...)
		change(&other)
		assert.NotEqual(t, base.Key(), other.Key(), "a different %s must key differently", name)
	}
}

// TestGitHubSpecKeySeparatesFields: values are quoted and separated, so text
// cannot be shuffled between neighbouring fields to collide.
func TestGitHubSpecKeySeparatesFields(t *testing.T) {
	a := GitHubSpec{Owner: "ac", Repo: "me"}
	b := GitHubSpec{Owner: "a", Repo: "cme"}
	assert.NotEqual(t, a.Key(), b.Key())

	// The same for the authors pair either side of a boundary: two lists that
	// concatenate alike must not key alike.
	c := GitHubSpec{Format: RecordFormat{AuthorsInclude: []string{"a", "b"}}}
	d := GitHubSpec{Format: RecordFormat{AuthorsInclude: []string{"a"}, AuthorsExclude: []string{"b"}}}
	assert.NotEqual(t, c.Key(), d.Key())
}

// TestGitHubSpecKeySharesOnEqualAuthorPolicies: the other half of the claim.
// Two packages that say the same thing about attribution must still share one
// releaser, or every repository pays for a resolution and a verification per
// package for nothing.
func TestGitHubSpecKeySharesOnEqualAuthorPolicies(t *testing.T) {
	spec := func() GitHubSpec {
		return GitHubSpec{
			Enabled: true, Owner: "acme", Repo: "mono",
			Format: RecordFormat{
				AuthorsPlacement: "both",
				AuthorsFormat:    "username",
				AuthorsCommits:   "all",
				AuthorsInclude:   []string{"*"},
				AuthorsExclude:   []string{"*bot*"},
				AuthorsTitle:     "Contributors",
			},
		}
	}
	a, b := spec(), spec()
	assert.Equal(t, a.Key(), b.Key())
}
