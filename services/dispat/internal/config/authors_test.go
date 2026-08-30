package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

// Unit tests of the authors entry-format object: the ladder that folds it, the
// mapping onto the resolved record policy, and the values it refuses.

func TestOverlayAuthorsFieldByField(t *testing.T) {
	// The whole point of an object rather than six flat keys: a nearer layer
	// states the one thing it cares about and inherits the rest.
	base := &AuthorsConfig{
		Placement: "section", Format: "fullname", Commits: "ccme",
		Include: []string{"*"}, Exclude: []string{"*bot*"}, Title: "Authors",
	}

	assert.Equal(t, base, overlayAuthors(base, nil), "a layer that says nothing changes nothing")

	got := overlayAuthors(base, &AuthorsConfig{Title: "Contributors"})
	assert.Equal(t, "Contributors", got.Title)
	assert.Equal(t, "section", got.Placement, "the other five inherit")
	assert.Equal(t, []string{"*bot*"}, got.Exclude)
	assert.Equal(t, "Authors", base.Title, "the base is not mutated")

	fresh := overlayAuthors(nil, &AuthorsConfig{Placement: "inline"})
	require.NotNil(t, fresh)
	assert.Equal(t, "inline", fresh.Placement)
	assert.Empty(t, fresh.Format, "nothing is invented for the keys the layer omitted")
}

func TestOverlayAuthorsListsReplaceWhole(t *testing.T) {
	// Adding to an inherited list could never take a pattern away again, which
	// is the same reason header and footer replace.
	base := &AuthorsConfig{Include: []string{"a*", "b*"}, Exclude: []string{"*bot*"}}
	got := overlayAuthors(base, &AuthorsConfig{Include: []string{"c*"}})
	assert.Equal(t, []string{"c*"}, got.Include)
	assert.Equal(t, []string{"*bot*"}, got.Exclude, "the list the layer did not state is inherited")
}

func TestOverlayAuthorsNearerOffDefeatsBroaderBoth(t *testing.T) {
	// "off" is spelled out as a placement rather than expressed by leaving the
	// key out precisely so a package can switch off what its space turned on.
	// If absence meant "off", no layer could ever inherit anything.
	space := &AuthorsConfig{Placement: "both", Title: "Contributors"}
	pkg := overlayAuthors(space, &AuthorsConfig{Placement: "off"})
	assert.Equal(t, "off", pkg.Placement)
	assert.Equal(t, "Contributors", pkg.Title, "switching off does not un-state the rest")
}

func TestOverlayFormatCarriesTheAuthorsObject(t *testing.T) {
	base := EntryFormatConfig{
		FeaturesTitle: "Features",
		Authors:       &AuthorsConfig{Placement: "section", Format: "fullname"},
	}
	over := EntryFormatConfig{Authors: &AuthorsConfig{Format: "username"}}

	got := overlayFormat(base, over)
	assert.Equal(t, "Features", got.FeaturesTitle)
	assert.Equal(t, "section", got.Authors.Placement)
	assert.Equal(t, "username", got.Authors.Format)

	// The whole ladder, through the two objects that carry the format.
	cl := overlayChangelog(
		&ChangelogConfig{EntryFormatConfig: EntryFormatConfig{
			Authors: &AuthorsConfig{Placement: "both", Include: []string{"*"}}}},
		&ChangelogConfig{EntryFormatConfig: EntryFormatConfig{
			Authors: &AuthorsConfig{Include: []string{"team-*"}}}})
	assert.Equal(t, "both", cl.Authors.Placement)
	assert.Equal(t, []string{"team-*"}, cl.Authors.Include)

	gh := overlayGitHub(
		&GitHubConfig{EntryFormatConfig: EntryFormatConfig{
			Authors: &AuthorsConfig{Placement: "both"}}},
		&GitHubConfig{EntryFormatConfig: EntryFormatConfig{
			Authors: &AuthorsConfig{Placement: "off"}}})
	assert.Equal(t, "off", gh.Authors.Placement)
}

func TestRecordFormatFlattensTheAuthorsObject(t *testing.T) {
	got := recordFormat(EntryFormatConfig{Authors: &AuthorsConfig{
		Placement: "both", Format: "username", Commits: "all",
		Include: []string{"a*"}, Exclude: []string{"*bot*"}, Title: "Contributors",
	}}, nil)
	assert.Equal(t, "both", got.AuthorsPlacement)
	assert.Equal(t, "username", got.AuthorsFormat)
	assert.Equal(t, "all", got.AuthorsCommits)
	assert.Equal(t, []string{"a*"}, got.AuthorsInclude)
	assert.Equal(t, []string{"*bot*"}, got.AuthorsExclude)
	assert.Equal(t, "Contributors", got.AuthorsTitle)

	// No object means the renderer defaults, which is the shape every existing
	// configuration has.
	empty := recordFormat(EntryFormatConfig{}, nil)
	assert.Empty(t, empty.AuthorsPlacement)
	assert.Empty(t, empty.AuthorsInclude)
}

func TestValidateAuthorsRejectsUnknownValues(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  AuthorsConfig
		want string
	}{
		"placement": {AuthorsConfig{Placement: "everywhere"},
			`authors.placement: unknown value "everywhere" (want one of off, inline, section, both)`},
		"format": {AuthorsConfig{Format: "handle"},
			`authors.format: unknown value "handle" (want one of fullname, username)`},
		"commits": {AuthorsConfig{Commits: "every"},
			`authors.commits: unknown value "every" (want one of ccme, all)`},
		"blank include": {AuthorsConfig{Include: []string{"a*", "  "}},
			"authors.include[1]: pattern must not be empty"},
		"blank exclude": {AuthorsConfig{Exclude: []string{""}},
			"authors.exclude[0]: pattern must not be empty"},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateAuthors("changelog", &tc.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), "changelog", "the error names the block it came from")
		})
	}

	// The valid values, and the empty string that means "the layer says
	// nothing", all pass.
	assert.NoError(t, validateAuthors("changelog", nil))
	assert.NoError(t, validateAuthors("changelog", &AuthorsConfig{}))
	for _, p := range []string{"off", "inline", "section", "both"} {
		assert.NoError(t, validateAuthors("changelog", &AuthorsConfig{Placement: p}), p)
	}
	for _, f := range []string{"fullname", "username"} {
		assert.NoError(t, validateAuthors("changelog", &AuthorsConfig{Format: f}), f)
	}
	for _, c := range []string{"ccme", "all"} {
		assert.NoError(t, validateAuthors("changelog", &AuthorsConfig{Commits: c}), c)
	}
}

func TestValidateAuthorsEnumIsSharedWithTheFlags(t *testing.T) {
	// The `changelog` and `github` commands reject a bad flag in the words the
	// config file would have been rejected in, so the two cannot drift.
	require.Error(t, ValidateAuthorsEnum("placement", "everywhere"))
	assert.Contains(t, ValidateAuthorsEnum("format", "handle").Error(),
		"want one of fullname, username")
	assert.NoError(t, ValidateAuthorsEnum("placement", ""), "an absent flag is not a bad value")
	assert.NoError(t, ValidateAuthorsEnum("commits", "all"))
	assert.NoError(t, ValidateAuthorsEnum("nonsense", "whatever"), "an unknown key checks nothing")
}

func TestLoadRejectsABadAuthorsValue(t *testing.T) {
	// Through the loader, so the block label the operator sees is the real one.
	cfg := models.File{
		Spaces: map[string]models.SpaceConfig{"libs": {Path: models.PathList{"pkgs"}}},
		GitHub: &models.GitHubConfig{EntryFormatConfig: models.EntryFormatConfig{
			Authors: &models.AuthorsConfig{Placement: "everywhere"}}},
	}
	_, err := loadModel(t, cfg, "pkgs/core")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authors.placement")
	assert.Contains(t, err.Error(), "github")
}

func TestLoadRejectsAnUnknownKeyInsideAuthors(t *testing.T) {
	// The object is decoded exactly like every other, so a typo inside it is a
	// load failure rather than a silently ignored setting.
	root := writeRawRepo(t, map[string]any{
		"spaces": map[string]any{"libs": map[string]any{"path": "pkgs"}},
		"changelog": map[string]any{
			"authors": map[string]any{"placement": "section", "titel": "Authors"},
		},
	}, "pkgs/core")
	_, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid format")
}
