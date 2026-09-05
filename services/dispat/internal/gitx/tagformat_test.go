package gitx

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme/v2"
)

// Formats that spell the prerelease out must render both shapes — the
// prerelease one and the stable one, with the section's glued literals gone —
// and read either back into the same SemVer version. The round trip is the
// property everything rests on: the tag is only a spelling, and a spelling
// that loses the version loses the package's history.
func TestTagFormatPrereleasePlaceholders(t *testing.T) {
	pre := ccme.Version{Major: 1, Minor: 2, Patch: 3, Prerelease: []string{"beta", "4"}}
	stable := ccme.Version{Major: 1, Minor: 2, Patch: 3}

	for _, tc := range []struct {
		format    TagFormat
		pkg       string
		preTag    string
		stableTag string
	}{
		{"{name}@{version}-{channel}{counter}", "core", "core@1.2.3-beta4", "core@1.2.3"},
		{"{name}@{version}.{channel}.{counter}", "core", "core@1.2.3.beta.4", "core@1.2.3"},
		{"{name}@v{version}-{channel}.{counter}", "core", "core@v1.2.3-beta.4", "core@v1.2.3"},
		{"services/{name}@v{version}-{channel}{counter}", "core",
			"services/core@v1.2.3-beta4", "services/core@v1.2.3"},
		// No separating literals at all: the byte classes still split it —
		// the core is digits and dots, the channel cannot start past a digit
		// run's end, and the counter is what remains.
		{"{name}@{version}{channel}{counter}", "core", "core@1.2.3beta4", "core@1.2.3"},
		// A scoped npm name exercises the same "@" appearing as a literal.
		{"{name}@{version}-{channel}{counter}", "@acme/ui", "@acme/ui@1.2.3-beta4", "@acme/ui@1.2.3"},
	} {
		require.NoError(t, tc.format.Validate(), "%q", tc.format)

		assert.Equal(t, tc.preTag, tc.format.Render(tc.pkg, pre), "prerelease render %q", tc.format)
		assert.Equal(t, tc.stableTag, tc.format.Render(tc.pkg, stable), "stable render %q", tc.format)

		got, ok := tc.format.ParseVersion(tc.pkg, tc.preTag)
		require.True(t, ok, "parse %q under %q", tc.preTag, tc.format)
		assert.Equal(t, pre.String(), got.String(),
			"the version is rebuilt in SemVer's shape whatever the tag spells")

		got, ok = tc.format.ParseVersion(tc.pkg, tc.stableTag)
		require.True(t, ok, "parse %q under %q", tc.stableTag, tc.format)
		assert.Equal(t, stable.String(), got.String())

		// One glob must cover both shapes: baseline(P) is a selection over a
		// single listing, and a pattern that misses the prerelease tags would
		// silently restart every train at .0.
		assert.True(t, tc.format.Matches(tc.pkg, tc.preTag), "matches %q", tc.preTag)
		assert.True(t, tc.format.Matches(tc.pkg, tc.stableTag), "matches %q", tc.stableTag)
	}
}

func TestTagFormatCounterBeyondTheSpec(t *testing.T) {
	// §11.3's counter is a bare number, but an exact Release-As may carry any
	// prerelease SemVer allows — "2.0.0-rc.1.hotfix" — and whatever render can
	// write must read back, or the release's own tag becomes an unparseable
	// baseline one run later.
	v := ccme.Version{Major: 2, Prerelease: []string{"rc", "1", "hotfix"}}

	for _, tc := range []struct {
		format TagFormat
		tag    string
	}{
		{"{name}@{version}-{channel}.{counter}", "core@2.0.0-rc.1.hotfix"},
		{"{name}@v{version}-{channel}{counter}", "core@v2.0.0-rc1.hotfix"},
		{"{name}@{version}.{channel}.{counter}", "core@2.0.0.rc.1.hotfix"},
	} {
		assert.Equal(t, tc.tag, tc.format.Render("core", v), "render %q", tc.format)
		got, ok := tc.format.ParseVersion("core", tc.tag)
		require.True(t, ok, "parse %q under %q", tc.tag, tc.format)
		assert.Equal(t, v.String(), got.String(), "round trip %q", tc.format)
	}

	// The greedy split keeps the channel a whole identifier: "beta10" is
	// beta/10, never b/eta10 — which is also what keeps counters ordering
	// numerically instead of restarting a train at every tenth release.
	got, ok := TagFormat("{name}@{version}-{channel}{counter}").ParseVersion("core", "core@1.2.3-beta10")
	require.True(t, ok)
	assert.Equal(t, "1.2.3-beta.10", got.String())
}

func TestTagFormatPrereleaseValidate(t *testing.T) {
	for _, bad := range []struct {
		format TagFormat
		why    string
	}{
		// One placeholder without the other cannot name a train's releases
		// apart, so the mistake is refused at load rather than at tag time.
		{"{name}@{version}-{channel}", "channel without counter"},
		{"{name}@{version}-{counter}", "counter without channel"},
		{"{name}@{version}-{counter}.{channel}", "counter before channel"},
		{"{name}@{channel}{counter}-{version}", "prerelease before version"},
		{"{name}@{version}-{channel}{channel}{counter}", "duplicate channel"},
		{"{name}@{version}-{channel}{counter}{counter}", "duplicate counter"},
		{"{name}@{version}-{channel}{name}{counter}", "placeholder inside the dropped section"},
	} {
		assert.Error(t, bad.format.Validate(), "%q (%s)", bad.format, bad.why)
	}

	// An unknown "{...}" stays literal text, exactly as it always has; here
	// it makes the rendered sample fail git's ref rules, not the grammar.
	assert.NoError(t, TagFormat("{name}@{version}-{channel}.{counter}").Validate())
}

func TestBaselineUnderPrereleaseFormat(t *testing.T) {
	// The listing must read both shapes of a spelled-out format, or a package
	// mid-train has no baseline and the next run believes the train never
	// started.
	root, cli := initRepo(t)
	ctx := context.Background()
	f := TagFormat("{name}@v{version}-{channel}{counter}")
	require.NoError(t, f.Validate())

	tagAt(t, root, "core@v1.2.3", "2026-01-01T10:00:00")
	tagAt(t, root, "core@v1.3.0-beta0", "2026-01-02T10:00:00")
	tagAt(t, root, "core@v1.3.0-beta1", "2026-01-03T10:00:00")

	tags := tagsOf(t, cli, ctx, "core", f)

	tag, found := tags.Baseline()
	require.True(t, found)
	assert.Equal(t, "core@v1.3.0-beta1", tag.Name)
	assert.Equal(t, "1.3.0-beta.1", tag.Version.String(),
		"the SemVer separators come back even though the tag never wrote them")

	stableTag, found := tags.StableBaseline()
	require.True(t, found)
	assert.Equal(t, "core@v1.2.3", stableTag.Name,
		"the stable baseline skips the prerelease shape of the same format")
}

func TestRenderInvalidFormatFallsBack(t *testing.T) {
	// Unreachable through the CLI (every configured format is validated at
	// load), but the fallback contract is part of Render's API: a format that
	// cannot compile renders the normative default rather than nothing, and
	// RenderVersion falls back to the SemVer string.
	v := ccme.Version{Major: 1, Minor: 2, Patch: 3, Prerelease: []string{"beta", "4"}}
	f := TagFormat("no placeholders at all")
	require.Error(t, f.Validate())
	assert.Equal(t, DefaultTagFormat.Render("core", v), f.Render("core", v))
	assert.Equal(t, "core@1.2.3-beta.4", f.Render("core", v))
	assert.Equal(t, v.String(), f.RenderVersion(v))
}

func TestRenderVersion(t *testing.T) {
	// The version section of the tag alone: {version} through {counter} with
	// the literals between them, nothing of the name or the decoration glued
	// before the version.
	pre := ccme.Version{Major: 1, Patch: 1, Prerelease: []string{"beta", "4"}}
	stable := ccme.Version{Major: 1, Patch: 1}
	for _, tc := range []struct {
		format     string
		wantPre    string
		wantStable string
	}{
		{"{name}@{version}", "1.0.1-beta.4", "1.0.1"},
		{"{name}@v{version}-{channel}{counter}", "1.0.1-beta4", "1.0.1"},
		{"{name}@{version}.{channel}.{counter}", "1.0.1.beta.4", "1.0.1"},
		{"services/{name}@v{version}", "1.0.1-beta.4", "1.0.1"},
	} {
		f := TagFormat(tc.format)
		require.NoError(t, f.Validate(), tc.format)
		assert.Equal(t, tc.wantPre, f.RenderVersion(pre), "%s, prerelease", tc.format)
		assert.Equal(t, tc.wantStable, f.RenderVersion(stable), "%s, stable", tc.format)
	}
}

// TestAliasFormatRendersVersionComponents: the three placeholders an alias
// adds, which is what a moving series tag is written from.
func TestAliasFormatRenders(t *testing.T) {
	v := ccme.Version{Major: 1, Minor: 4, Patch: 2}
	pre := ccme.Version{Major: 1, Minor: 4, Patch: 2, Prerelease: []string{"rc", "1"}}
	for _, tc := range []struct{ format, stable, prerelease string }{
		{"v{version}", "v1.4.2", "v1.4.2-rc.1"},
		{"v{major}", "v1", "v1"},
		{"v{major}.{minor}", "v1.4", "v1.4"},
		{"{name}-{major}.{minor}.{patch}", "pkg-1.4.2", "pkg-1.4.2"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			assert.Equal(t, tc.stable, AliasFormat(tc.format).Render("pkg", v))
			assert.Equal(t, tc.prerelease, AliasFormat(tc.format).Render("pkg", pre))
		})
	}
}

// TestAliasFormatValidate: an alias is write-only, so it drops the rules that
// exist purely so a tag can be read back, and keeps the ones that make it a
// usable ref name.
func TestAliasFormatValidate(t *testing.T) {
	for _, ok := range []string{"v{version}", "v{major}", "v{major}.{minor}", "{name}/v{major}"} {
		assert.NoError(t, AliasFormat(ok).Validate(), ok)
	}
	for _, tc := range []struct{ format, want string }{
		{"latest", "names no part of the version"},
		{"{name}", "names no part of the version"},
		{"v{version}-{channel}", "uses {channel} without {counter}"},
		{"v{version}{version}", "more than one {version} placeholder"},
		{"/v{major}", "which git rejects"},
		{"v{major}..{minor}", "which git rejects"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			err := AliasFormat(tc.format).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestTagFormatRefusesAliasOnlyPlaceholders: a release tag has to be readable
// back into the version that made it, and "v1" names no release in particular.
func TestTagFormatRefusesAliasOnlyPlaceholders(t *testing.T) {
	for _, format := range []string{"{name}@{major}", "{name}@{version}.{minor}", "{name}@{version}+{patch}"} {
		err := TagFormat(format).Validate()
		require.Error(t, err, format)
		assert.Contains(t, err.Error(), "only available in aliasTags")
	}
}

// TestAliasFormatMatchesTheNamesItWrites: telling an alias apart from a
// release tag nobody can parse is what makes the single-repository "v1"
// convention safe, and the two look identical from the tagFormat's side.
//
// The test is on both answers. A name the alias could have written is one, and
// a name it could not is not, however close it looks: that second half is what
// keeps a malformed release tag inside the listing, where the initials
// fallback still sees it.
func TestAliasFormatMatchesTheNamesItWrites(t *testing.T) {
	for name, tc := range map[string]struct {
		format AliasFormat
		pkg    string
		tag    string
		want   bool
	}{
		"the name it wrote":                     {format: "v{major}", pkg: "core", tag: "v1", want: true},
		"a later major":                         {format: "v{major}", pkg: "core", tag: "v27", want: true},
		"a release tag of the same line":        {format: "v{major}", pkg: "core", tag: "v1.4.2"},
		"a release tag somebody mistyped":       {format: "v{major}", pkg: "core", tag: "v1.0.0.0"},
		"another prefix entirely":               {format: "v{major}", pkg: "core", tag: "core@1.4.2"},
		"the bare prefix with nothing after it": {format: "v{major}", pkg: "core", tag: "v"},
		"a package-qualified alias":             {format: "{name}-v{major}", pkg: "core", tag: "core-v1", want: true},
		"the same alias of another package":     {format: "{name}-v{major}", pkg: "core", tag: "utils-v1"},
		"a suffix the format writes":            {format: "v{major}-latest", pkg: "core", tag: "v1-latest", want: true},
		"the suffix missing":                    {format: "v{major}-latest", pkg: "core", tag: "v1"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.format.Matches(tc.pkg, tc.tag))
		})
	}
}

// TestAliasFormatMatchesItsOwnPrereleaseRenders: an alias spelling a whole
// {version} writes whatever version it is given, prereleases included, so it
// has to recognise what it wrote. Reading the version class as digits and dots
// regardless of the format made "core-v1.4.2-beta.4" unrecognisable, and the
// alias then sat in its own package's listing as the newest thing nobody could
// parse.
//
// The other half is the acceptance. Anything the class allows would otherwise
// pass, and "core-vgarbage" would take a genuinely malformed release tag out
// of the listing along with it.
func TestAliasFormatMatchesItsOwnPrereleaseRenders(t *testing.T) {
	const alias = AliasFormat("{name}-v{version}")
	for _, v := range []ccme.Version{
		{Major: 1, Minor: 4, Patch: 2},
		{Major: 1, Minor: 4, Patch: 2, Prerelease: []string{"beta", "4"}},
		{Major: 2, Prerelease: []string{"rc", "0"}},
	} {
		rendered := alias.Render("core", v)
		assert.True(t, alias.Matches("core", rendered), "%s is a name this alias writes", rendered)
	}

	assert.False(t, alias.Matches("core", "core-vgarbage"),
		"the version has to be a version, not merely bytes the class allows")
	assert.False(t, alias.Matches("core", "core-v1.0.0.0"), "nor a release tag somebody mistyped")
	assert.False(t, alias.Matches("core", "other-v1.4.2"), "nor another package's")
}

// TestAliasFormatMatchesAPrereleaseSpellingFormat: a format writing the
// channel and counter itself renders a stable version with that whole section
// dropped, exactly as a tagFormat does, so both shapes have to be recognised.
func TestAliasFormatMatchesAPrereleaseSpellingFormat(t *testing.T) {
	const alias = AliasFormat("v{version}-{channel}.{counter}")
	stable := alias.Render("core", ccme.Version{Major: 1, Minor: 4, Patch: 2})
	pre := alias.Render("core", ccme.Version{Major: 1, Minor: 4, Patch: 2, Prerelease: []string{"beta", "4"}})
	assert.Equal(t, "v1.4.2", stable, "the stable render drops the section it cannot fill")
	assert.True(t, alias.Matches("core", stable))
	assert.True(t, alias.Matches("core", pre))
	assert.False(t, alias.Matches("core", "v1.0.0.0"))
}

// TestAliasFormatMatchesNothingWithoutAPlaceholder: a format that writes a
// constant has no shape to recognise, only a name. The alias validation
// refuses those, so this is the guard rather than a case with behaviour.
func TestAliasFormatMatchesNothingWithoutAPlaceholder(t *testing.T) {
	assert.False(t, AliasFormat("latest").Matches("core", "latest"))
}

// TestCompiledReadersOfAFormatThatDoesNotCompile: both compiled halves answer
// the same thing their uncompiled forms do about a format that is not one, so
// a caller holding a reader never has to ask whether it holds a good one.
func TestCompiledReadersOfAFormatThatDoesNotCompile(t *testing.T) {
	// Two {version} placeholders: readable back as nothing in particular, and
	// refused by Validate for exactly that reason.
	broken := TagFormat("{version}-{version}")
	require.Error(t, broken.Validate())
	_, ok := broken.Reader("core").ParseVersion("1.2.3-1.2.3")
	assert.False(t, ok)
	_, ok = broken.ParseVersion("core", "1.2.3-1.2.3")
	assert.False(t, ok, "the compiled reader and the format agree")

	assert.False(t, AliasMatcher{}.Matches("v1"), "a matcher of nothing matches nothing")
}
