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
func fixture(t *testing.T) Workspace {
	t.Helper()
	root := t.TempDir()
	pkg := func(name, dir string) *model.Package {
		return &model.Package{Name: name, Dir: filepath.Join(root, filepath.FromSlash(dir))}
	}
	return Workspace{
		Root: root,
		Packages: []*model.Package{
			pkg("core", "packages/core"),
			pkg("web", "packages/web"),
			pkg("group", "apps/group"),
			pkg("site", "apps/site"),
			pkg("deep", "apps/group/deep"),
			pkg("tool", "tools/tool"),
		},
		Spaces: map[string]string{"libs": "packages", "apps": "apps"},
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

func TestResolveUnionOfPackageAndSpaceTerms(t *testing.T) {
	res, err := Resolve(Filter{Packages: []string{"tool"}, Spaces: []string{"libs"}}, fixture(t))
	require.NoError(t, err)
	assert.Equal(t, []string{"core", "web", "tool"}, res.Names)
	assert.Equal(t, `package tool and space "libs"`, res.Description)
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
		"the miss looks across the other flag")
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

	bare := Workspace{Root: ws.Root, Packages: ws.Packages}
	_, err = Resolve(Filter{Spaces: []string{"libs"}}, bare)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "this repository configures none")
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
