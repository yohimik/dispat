package filter

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// The fixture workspace, laid out under one temp dir so every path in a test
// derives from the same string — mixing in os.Getwd or a /tmp literal is how
// macOS's /var vs /private/var pair makes prefix matching fail:
//
//	packages/core   packages/web    space "libs"
//	apps/site                       space "apps"
//	apps/group/deep                 standalone, nested inside space "apps"
//	tools/tool                      standalone, outside every space
//
// "group" is listed before "deep" on purpose: a resolver taking the first
// matching folder rather than the deepest would answer "group" for a dir
// inside "deep".
//
// The versioning groups cut across that layout, which is the point of having
// them: "libs" versions its two packages as one, so its name is a group as
// well as a space, and the declared group "shared" is joined by a package of
// the apps space and by a standalone package, so no folder describes it.
func fixture(t *testing.T) Workspace {
	t.Helper()
	root := t.TempDir()
	libs := &model.Space{Name: "libs", Versioning: model.VersioningFixed}
	apps := &model.Space{Name: "apps"}
	// site's config joins a declared group, so it carries a derived copy of
	// its space; tool is standalone, and its synthetic space joins the same
	// group.
	appsShared := &model.Space{Name: "apps", Versioning: model.VersioningFixedMajor, VersionGroup: "shared"}
	toolShared := &model.Space{Name: "tool", Versioning: model.VersioningFixed, VersionGroup: "shared"}
	pkg := func(name, dir string, space *model.Space) *model.Package {
		return &model.Package{Name: name, Dir: filepath.Join(root, filepath.FromSlash(dir)), Space: space}
	}
	return Workspace{
		Root: root,
		Packages: []*model.Package{
			pkg("core", "packages/core", libs),
			pkg("web", "packages/web", libs),
			pkg("group", "apps/group", apps),
			pkg("site", "apps/site", appsShared),
			pkg("deep", "apps/group/deep", nil),
			pkg("tool", "tools/tool", toolShared),
		},
		Spaces: map[string]string{"libs": "packages", "apps": "apps"},
		Groups: []string{"shared"},
	}
}

func TestResolveInactiveWithoutTermsOrDir(t *testing.T) {
	res, err := Resolve(Filter{}, fixture(t))
	require.NoError(t, err)
	assert.False(t, res.Active())
	assert.Nil(t, res.Names)
	assert.Empty(t, res.Description)
	assert.True(t, res.Has("anything"), "an inactive result selects everything")
	in := []string{"web", "core"}
	assert.Equal(t, in, res.Keep(in), "and keeps every name, in the caller's order")
}

func TestResolvePackageTerms(t *testing.T) {
	ws := fixture(t)
	for name, tc := range map[string]struct {
		terms []string
		want  []string
	}{
		"one name":                {[]string{"core"}, []string{"core"}},
		"several names":           {[]string{"web", "core"}, []string{"core", "web"}},
		"repeated names collapse": {[]string{"core", "core"}, []string{"core"}},
		"case does not matter":    {[]string{"CoRe"}, []string{"core"}},
		"a standalone package":    {[]string{"tool"}, []string{"tool"}},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := Resolve(Filter{Packages: tc.terms}, ws)
			require.NoError(t, err)
			assert.True(t, res.Active())
			assert.Equal(t, tc.want, res.Names, "the selection comes out in workspace order")
		})
	}
}

func TestResolveGlobTerms(t *testing.T) {
	ws := fixture(t)
	for name, tc := range map[string]struct {
		terms []string
		want  []string
	}{
		"a prefix glob": {[]string{"*e"}, []string{"core", "site"}},
		"a suffix glob": {[]string{"w*"}, []string{"web"}},
		"star is every package, standalone ones included": {
			[]string{"*"}, []string{"core", "web", "group", "site", "deep", "tool"}},
		"a glob crosses the separator": {[]string{"*o*"}, []string{"core", "group", "tool"}},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := Resolve(Filter{Packages: tc.terms}, ws)
			require.NoError(t, err)
			assert.Equal(t, tc.want, res.Names)
		})
	}
}

func TestResolveSpaceTerms(t *testing.T) {
	ws := fixture(t)
	for name, tc := range map[string]struct {
		terms []string
		want  []string
	}{
		"one space":    {[]string{"libs"}, []string{"core", "web"}},
		"another":      {[]string{"apps"}, []string{"group", "site"}},
		"both":         {[]string{"apps", "libs"}, []string{"core", "web", "group", "site"}},
		"case-blind":   {[]string{"LIBS"}, []string{"core", "web"}},
		"a space glob": {[]string{"*s"}, []string{"core", "web", "group", "site"}},
		"star is every configured space, and no standalone package": {
			[]string{"*"}, []string{"core", "web", "group", "site"}},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := Resolve(Filter{Spaces: tc.terms}, ws)
			require.NoError(t, err)
			assert.Equal(t, tc.want, res.Names)
		})
	}
	t.Run("a nested standalone package is not its space's", func(t *testing.T) {
		res, err := Resolve(Filter{Spaces: []string{"apps"}}, ws)
		require.NoError(t, err)
		assert.NotContains(t, res.Names, "deep",
			"deep sits under apps/group, not directly under the space folder")
	})
}

// TestResolveGroupTerms: a --group term names the packages that version
// together, which is a relationship and not a folder — it may hold packages
// from several spaces, and it holds nothing that versions on its own.
func TestResolveGroupTerms(t *testing.T) {
	ws := fixture(t)
	for name, tc := range map[string]struct {
		terms []string
		want  []string
	}{
		"a space that versions as one group": {[]string{"libs"}, []string{"core", "web"}},
		"a declared group crossing a space and a standalone package": {
			[]string{"shared"}, []string{"site", "tool"}},
		"both groups":  {[]string{"libs", "shared"}, []string{"core", "web", "site", "tool"}},
		"case-blind":   {[]string{"SHARED"}, []string{"site", "tool"}},
		"a group glob": {[]string{"*d"}, []string{"site", "tool"}},
		"star is every grouped package, and no independent one": {
			[]string{"*"}, []string{"core", "web", "site", "tool"}},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := Resolve(Filter{Groups: tc.terms}, ws)
			require.NoError(t, err)
			assert.True(t, res.Active())
			assert.Equal(t, tc.want, res.Names, "the selection comes out in workspace order")
		})
	}
}

func TestResolveUnionOfPackageAndSpaceTerms(t *testing.T) {
	ws := fixture(t)
	res, err := Resolve(Filter{Packages: []string{"tool"}, Spaces: []string{"libs"}}, ws)
	require.NoError(t, err)
	assert.Equal(t, []string{"core", "web", "tool"}, res.Names)
	assert.Equal(t, `package tool and space "libs"`, res.Description)

	res, err = Resolve(Filter{Packages: []string{"group"}, Spaces: []string{"libs"},
		Groups: []string{"shared"}}, ws)
	require.NoError(t, err)
	assert.Equal(t, []string{"core", "web", "group", "site", "tool"}, res.Names,
		"the three flags union, in workspace order")
	assert.Equal(t, `package group and space "libs" and versioning group "shared"`, res.Description)

	res, err = Resolve(Filter{Packages: []string{"core"}, Groups: []string{"libs"}}, ws)
	require.NoError(t, err)
	assert.Equal(t, []string{"core", "web"}, res.Names, "a package named twice over is selected once")
}

func TestResolveUnknownPackageIsAnError(t *testing.T) {
	ws := fixture(t)
	_, err := Resolve(Filter{Packages: []string{"ghost"}}, ws)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--package "ghost" matches no package`)
	assert.Contains(t, err.Error(), "core, web, group, site, deep, tool")

	_, err = Resolve(Filter{Packages: []string{"nothing-*"}}, ws)
	require.Error(t, err, "a glob matching nothing is a typo too")

	_, err = Resolve(Filter{Packages: []string{"libs"}}, ws)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `libs is a space — select it with --space`,
		"the miss looks across the other flags")

	_, err = Resolve(Filter{Packages: []string{"shared"}}, ws)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `shared is a versioning group — select it with --group`)
}

func TestResolveUnknownSpaceIsAnError(t *testing.T) {
	ws := fixture(t)
	_, err := Resolve(Filter{Spaces: []string{"ghost"}}, ws)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--space "ghost" matches no configured space`)
	assert.Contains(t, err.Error(), "configured: apps, libs")

	_, err = Resolve(Filter{Spaces: []string{"tool"}}, ws)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `tool is a package — select it with --package`,
		"a standalone package belongs to no space, so its name lands here")

	_, err = Resolve(Filter{Spaces: []string{"shared"}}, ws)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `shared is a versioning group — select it with --group`,
		"a group spans spaces, so its name is not one")

	bare := Workspace{Root: ws.Root, Packages: ws.Packages}
	_, err = Resolve(Filter{Spaces: []string{"libs"}}, bare)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "this repository configures none")
}

// TestResolveUnknownGroupIsAnError: naming a group that does not version as
// one is the mistake worth explaining, so the miss lists the groups there are
// and looks across the other two flags.
func TestResolveUnknownGroupIsAnError(t *testing.T) {
	ws := fixture(t)
	_, err := Resolve(Filter{Groups: []string{"ghost"}}, ws)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--group "ghost" matches no versioning group`)
	assert.Contains(t, err.Error(), "configured: libs, shared")

	_, err = Resolve(Filter{Groups: []string{"apps"}}, ws)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `apps is a space — select it with --space`,
		"a space whose packages version on their own has no group to name")

	_, err = Resolve(Filter{Groups: []string{"core"}}, ws)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `core is a package — select it with --package`)

	bare := Workspace{Root: ws.Root, Packages: []*model.Package{{Name: "solo"}}}
	_, err = Resolve(Filter{Groups: []string{"libs"}}, bare)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "this repository configures none")
}

// TestResolveGroupWithNoPackagesIsAnError: a group declared and joined by
// nobody is recognised, and still refuses to act on nothing.
func TestResolveGroupWithNoPackagesIsAnError(t *testing.T) {
	ws := fixture(t)
	ws.Groups = append(ws.Groups, "empty")
	_, err := Resolve(Filter{Groups: []string{"empty"}}, ws)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--group "empty" matches no package (versioning group "empty" holds none)`)
}

func TestResolveSpaceWithNoPackagesIsAnError(t *testing.T) {
	ws := fixture(t)
	ws.Spaces["empty"] = "empty"
	_, err := Resolve(Filter{Spaces: []string{"empty"}}, ws)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `--space "empty" matches no package (space "empty" holds none)`)
}

func TestResolveInfersFromTheInvocationFolder(t *testing.T) {
	ws := fixture(t)
	for name, tc := range map[string]struct {
		dir    string
		want   []string
		descr  string
		active bool
	}{
		"a package folder": {
			"packages/core", []string{"core"}, "core", true},
		"a folder inside a package": {
			"packages/core/src/deep", []string{"core"}, "core", true},
		"a space folder": {
			"packages", []string{"core", "web"}, `space "libs"`, true},
		"a folder inside a space but outside every package": {
			"packages/.cache", []string{"core", "web"}, `space "libs"`, true},
		"the deepest match wins": {
			"apps/group/deep/src", []string{"deep"}, "deep", true},
		"the monorepo root": {
			".", nil, "", false},
		"a folder outside every space": {
			"tools", nil, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(ws.Root, filepath.FromSlash(tc.dir))
			res, err := Resolve(Filter{Dir: dir}, ws)
			require.NoError(t, err)
			assert.Equal(t, tc.active, res.Active())
			assert.Equal(t, tc.want, res.Names)
			assert.Equal(t, tc.descr, res.Description)
		})
	}
	t.Run("a sibling sharing a name prefix is not inside the package", func(t *testing.T) {
		res, err := Resolve(Filter{Dir: filepath.Join(ws.Root, "packages", "core-extra")}, ws)
		require.NoError(t, err)
		assert.Equal(t, []string{"core", "web"}, res.Names, "it is still inside the space, though")
	})
}

func TestResolveInferenceSkipsASpaceRootedAtTheMonorepoRoot(t *testing.T) {
	ws := fixture(t)
	ws.Spaces["top"] = "."
	// A package sitting directly under the root is the space's.
	ws.Packages = append(ws.Packages,
		&model.Package{Name: "rooted", Dir: filepath.Join(ws.Root, "rooted")})

	res, err := Resolve(Filter{Dir: ws.Root}, ws)
	require.NoError(t, err)
	assert.False(t, res.Active(), "standing at the top must not narrow anything")

	res, err = Resolve(Filter{Spaces: []string{"top"}}, ws)
	require.NoError(t, err)
	assert.Equal(t, []string{"rooted"}, res.Names, "an explicit term still reaches it")
}

func TestResolveExplicitTermsBeatInference(t *testing.T) {
	ws := fixture(t)
	inside := filepath.Join(ws.Root, "packages", "core")
	res, err := Resolve(Filter{Packages: []string{"web"}, Dir: inside}, ws)
	require.NoError(t, err)
	assert.Equal(t, []string{"web"}, res.Names)

	res, err = Resolve(Filter{Spaces: []string{"apps"}, Dir: inside}, ws)
	require.NoError(t, err)
	assert.Equal(t, []string{"group", "site"}, res.Names)
}

func TestResolveReservesNoTerm(t *testing.T) {
	root := t.TempDir()
	ws := Workspace{
		Root:     root,
		Packages: []*model.Package{{Name: "all", Dir: filepath.Join(root, "packages", "all")}},
		Spaces:   map[string]string{"all": "packages"},
	}
	res, err := Resolve(Filter{Packages: []string{"all"}}, ws)
	require.NoError(t, err)
	assert.Equal(t, []string{"all"}, res.Names, `"all" is an ordinary name`)

	res, err = Resolve(Filter{Spaces: []string{"all"}}, ws)
	require.NoError(t, err)
	assert.Equal(t, []string{"all"}, res.Names)
}

func TestResolveMatchesRelativePathsAgainstAbsoluteOnes(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	ws := Workspace{
		Root:     ".",
		Packages: []*model.Package{{Name: "core", Dir: filepath.Join("packages", "core")}},
		Spaces:   map[string]string{"libs": "packages"},
	}
	res, err := Resolve(Filter{Dir: filepath.Join("packages", "core")}, ws)
	require.NoError(t, err)
	assert.Equal(t, []string{"core"}, res.Names)

	res, err = Resolve(Filter{Dir: filepath.Join(root, "packages")}, ws)
	require.NoError(t, err)
	assert.Equal(t, []string{"core"}, res.Names, "an absolute dir meets a relative workspace")
}

func TestKeepAndHas(t *testing.T) {
	res, err := Resolve(Filter{Packages: []string{"core", "tool"}}, fixture(t))
	require.NoError(t, err)
	assert.True(t, res.Has("core"))
	assert.False(t, res.Has("web"))
	assert.Equal(t, []string{"tool", "core"}, res.Keep([]string{"tool", "web", "core"}),
		"Keep preserves the caller's order, not the workspace's")
	assert.Empty(t, res.Keep([]string{"web"}))
	assert.Empty(t, res.Keep(nil))
}

func TestDescriptionRenderings(t *testing.T) {
	ws := fixture(t)
	for name, tc := range map[string]struct {
		f    Filter
		want string
	}{
		"one package term names what it resolved to": {
			Filter{Packages: []string{"CoRe"}}, "core"},
		"several package terms are echoed": {
			Filter{Packages: []string{"core", "web"}}, "packages core, web"},
		"a package glob is echoed": {
			Filter{Packages: []string{"*e"}}, `packages matching "*e"`},
		"one space term": {
			Filter{Spaces: []string{"libs"}}, `space "libs"`},
		"several space terms": {
			Filter{Spaces: []string{"libs", "apps"}}, `spaces "libs", "apps"`},
		"a space glob": {
			Filter{Spaces: []string{"*s"}}, `spaces matching "*s"`},
		"one group term": {
			Filter{Groups: []string{"shared"}}, `versioning group "shared"`},
		"several group terms": {
			Filter{Groups: []string{"shared", "libs"}}, `versioning groups "shared", "libs"`},
		"a group glob": {
			Filter{Groups: []string{"*d"}}, `versioning groups matching "*d"`},
		"the inferred folder reads like the term it stands for": {
			Filter{Dir: filepath.Join(ws.Root, "apps", "site")}, "site"},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := Resolve(tc.f, ws)
			require.NoError(t, err)
			assert.Equal(t, tc.want, res.Description)
		})
	}
}
