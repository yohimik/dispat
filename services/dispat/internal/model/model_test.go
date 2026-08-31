package model

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		// The record's own link, section and reference policy, for the same
		// reason: each shapes every body the shared releaser sends.
		"dependency link":     func(s *GitHubSpec) { s.Format.DependencyLink = LinkAuto },
		"no-changes text":     func(s *GitHubSpec) { s.Format.NoChangesText = "see the changelog" },
		"refs placement":      func(s *GitHubSpec) { s.Format.CommitRefsPlacement = "suffix" },
		"refs format":         func(s *GitHubSpec) { s.Format.CommitRefsFormat = "$DISPAT_COMMIT" },
		"refs link":           func(s *GitHubSpec) { s.Format.CommitRefsLink = LinkAuto },
		"link owner":          func(s *GitHubSpec) { s.Format.LinkOwner = "other" },
		"link repo":           func(s *GitHubSpec) { s.Format.LinkRepo = "other" },
		"link api url":        func(s *GitHubSpec) { s.Format.LinkAPIURL = "https://ghe/api/v3" },
		"sections length":     func(s *GitHubSpec) { s.Format.Sections = []RecordSection{{Builtin: SectionFixes}} },
		"a section's builtin": func(s *GitHubSpec) { s.Format.Sections = []RecordSection{{Builtin: SectionFeatures}} },
		"a section's title": func(s *GitHubSpec) {
			s.Format.Sections = []RecordSection{{Title: "Added", Types: []string{"add"}}}
		},
		"a section's types": func(s *GitHubSpec) {
			s.Format.Sections = []RecordSection{{Title: "Added", Types: []string{"new"}}}
		},
		"a section's bump": func(s *GitHubSpec) {
			s.Format.Sections = []RecordSection{{Title: "Added", Types: []string{"add"}, Bump: "minor"}}
		},
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

	// A section order is the arrangement, not the set: two policies holding
	// the same sections in different orders render different entries and must
	// not share a releaser.
	e := GitHubSpec{Format: RecordFormat{Sections: []RecordSection{
		{Builtin: SectionFixes}, {Builtin: SectionFeatures}}}}
	f := GitHubSpec{Format: RecordFormat{Sections: []RecordSection{
		{Builtin: SectionFeatures}, {Builtin: SectionFixes}}}}
	assert.NotEqual(t, e.Key(), f.Key())
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

// keyExcludedRecordFormatLeaves names the RecordFormat leaves that are
// deliberately left out of writeKey, addressed the way recordFormatLeaves
// addresses them ("AuthorsTitle", "Header.Line", ...).
//
// It is empty, and it should stay empty. Every field of the format shapes the
// body a shared releaser renders, so a field outside the key would let two
// packages that render differently share one releaser, and the second package
// would silently be given the first one's format. An entry added here has to
// carry a comment saying why the field cannot change a rendered entry.
var keyExcludedRecordFormatLeaves = map[string]string{}

// TestRecordFormatKeyCoversEveryField is the seam test over the policy key.
//
// The key is written by hand, field by field, while RecordFormat grows by
// configuration: the two are exactly the kind of pair that drifts, and a field
// added to the format but forgotten in writeKey compiles cleanly, passes every
// test that does not name it, and is discovered only as one package wearing
// another's changelog format in a release that already went out.
//
// So the fields are not listed here. Reflection walks RecordFormat down to its
// leaves, entering a slice at a single element (which is the shape writeKey
// encodes), and for each leaf builds two formats that agree on everything else
// and differ in that one leaf alone. A leaf the key ignores makes the two keys
// equal, and the case fails naming the field. A field of a kind the marker
// builder does not know fails too, rather than passing by accident: a new kind
// in the format is a decision about the key, and it should be made on purpose.
func TestRecordFormatKeyCoversEveryField(t *testing.T) {
	// The key is read through the exported door rather than through writeKey,
	// because Key is what the releaser cache actually compares. Owner and Repo
	// are fixed so that only the format's contribution varies.
	keyOf := func(f RecordFormat) string {
		return GitHubSpec{Enabled: true, Owner: "acme", Repo: "mono", Format: f}.Key()
	}

	leaves := recordFormatLeaves(t, reflect.TypeOf(RecordFormat{}), nil)
	require.NotEmpty(t, leaves, "the walk found no fields, which means it is not walking")

	for _, path := range leaves {
		name := strings.Join(path, ".")
		t.Run(name, func(t *testing.T) {
			if why, ok := keyExcludedRecordFormatLeaves[name]; ok {
				t.Skip("deliberately outside the policy key: " + why)
			}
			var base, other RecordFormat
			fillRecordFormat(t, reflect.ValueOf(&base).Elem(), markerOne)
			fillRecordFormat(t, reflect.ValueOf(&other).Elem(), markerOne)
			setRecordFormatLeaf(t, reflect.ValueOf(&other).Elem(), path, markerTwo)

			require.NotEqual(t, base, other, "the two formats must actually differ in %s", name)
			assert.NotEqual(t, keyOf(base), keyOf(other),
				"%s is missing from GitHubSpec.Key: two packages that render differently would share a releaser", name)
		})
	}
}

// The two marker values every leaf is filled with. They differ in every kind
// the builder knows, so a leaf set to one and then to the other is a format
// that changed in exactly one place.
const (
	markerOne = "one"
	markerTwo = "two"
)

// recordFormatLeaves lists the paths of a format's leaves: the field names
// that reach each value the key could encode. A slice of structs is entered
// rather than treated as a leaf, so the fields of an EntryLine and of a
// RecordSection are walked too, and a filter added to either is covered by the
// same test that covers the format's own fields.
func recordFormatLeaves(t *testing.T, typ reflect.Type, prefix []string) [][]string {
	t.Helper()
	var out [][]string
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		path := append(append([]string(nil), prefix...), f.Name)
		switch {
		case f.Type.Kind() == reflect.Struct:
			out = append(out, recordFormatLeaves(t, f.Type, path)...)
		case f.Type.Kind() == reflect.Slice && f.Type.Elem().Kind() == reflect.Struct:
			out = append(out, recordFormatLeaves(t, f.Type.Elem(), path)...)
		default:
			out = append(out, path)
		}
	}
	return out
}

// fillRecordFormat sets every leaf of v to the marker, giving each slice of
// structs a single element. A fully populated pair is what makes the
// difference between the two values a single leaf: a base of zero values would
// let a slice's length carry the change instead of the field under test.
func fillRecordFormat(t *testing.T, v reflect.Value, marker string) {
	t.Helper()
	typ := v.Type()
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		switch {
		case f.Type.Kind() == reflect.Struct:
			fillRecordFormat(t, v.Field(i), marker)
		case f.Type.Kind() == reflect.Slice && f.Type.Elem().Kind() == reflect.Struct:
			s := reflect.MakeSlice(f.Type, 1, 1)
			fillRecordFormat(t, s.Index(0), marker)
			v.Field(i).Set(s)
		default:
			v.Field(i).Set(markerValue(t, f.Type, marker))
		}
	}
}

// setRecordFormatLeaf walks v along the path and writes the marker at its end.
// The slices on the way already hold their single element, because the value
// was filled before the walk.
func setRecordFormatLeaf(t *testing.T, v reflect.Value, path []string, marker string) {
	t.Helper()
	for i, name := range path {
		v = v.FieldByName(name)
		require.True(t, v.IsValid(), "no field %s on the way to %s", name, strings.Join(path, "."))
		if i == len(path)-1 {
			v.Set(markerValue(t, v.Type(), marker))
			return
		}
		if v.Kind() == reflect.Slice {
			require.Equal(t, 1, v.Len(), "the fill left %s without its element", name)
			v = v.Index(0)
		}
	}
}

// markerValue is a marker as a value of typ. It knows the kinds a record
// format is made of; anything else fails the test rather than being guessed
// at, because a field the builder cannot vary is a field the seam test would
// otherwise report as covered without ever having changed it.
func markerValue(t *testing.T, typ reflect.Type, marker string) reflect.Value {
	t.Helper()
	switch typ.Kind() {
	case reflect.String:
		return reflect.ValueOf(marker).Convert(typ)
	case reflect.Bool:
		return reflect.ValueOf(marker == markerOne).Convert(typ)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := int64(1)
		if marker == markerTwo {
			n = 2
		}
		return reflect.ValueOf(n).Convert(typ)
	case reflect.Slice:
		s := reflect.MakeSlice(typ, 1, 1)
		s.Index(0).Set(markerValue(t, typ.Elem(), marker))
		return s
	default:
		t.Fatalf("a record format field of kind %s has no marker: teach markerValue about it, "+
			"and decide whether GitHubSpec.Key should encode it", typ.Kind())
		return reflect.Value{}
	}
}
