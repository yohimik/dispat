package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The webhooks list is validated as strictly as any other section, because a
// webhook that never fires is worse than one that is refused at load time: a
// misspelled event name or a broken URL would otherwise go unnoticed until
// the release nobody was told about.

func TestLoadWebhooksValid(t *testing.T) {
	cfg := minimalConfig()
	cfg.Webhooks = []WebhookConfig{
		{URL: "https://ci.example.com/hooks/dispat", Events: []string{"release.started", "release.finished"}},
		{Name: "tracker", URL: "http://tracker.internal/dispat", Method: "put",
			Events:    []string{"package.*"},
			Headers:   []WebhookHeader{{Name: "X-Api-Key", Value: "$TRACKER_TOKEN"}},
			SecretEnv: "HOOK_SECRET", Timeout: 3},
	}
	loaded, err := loadModel(t, cfg, "pkgs/core")
	require.NoError(t, err)
	require.Len(t, loaded.Webhooks, 2)
	// An absent method resolves to POST and a stated one is normalized to
	// upper case, so downstream code compares against one spelling.
	assert.Equal(t, "POST", loaded.Webhooks[0].Method)
	assert.Equal(t, "PUT", loaded.Webhooks[1].Method)
	// Header names come through the load verbatim: a list of objects, so
	// viper's map-key lowercasing never touches them.
	assert.Equal(t, "X-Api-Key", loaded.Webhooks[1].Headers[0].Name)
}

func TestLoadWebhooksAbsent(t *testing.T) {
	// A configuration that says nothing about webhooks loads exactly as it
	// always did: the key is optional and nothing fills it in.
	loaded, err := loadModel(t, minimalConfig(), "pkgs/core")
	require.NoError(t, err)
	assert.Nil(t, loaded.Webhooks)
}

func TestLoadWebhooksRejections(t *testing.T) {
	for name, tc := range map[string]struct {
		webhook WebhookConfig
		want    string
	}{
		"missing url":       {WebhookConfig{}, "webhooks[0]: url is required"},
		"unparseable url":   {WebhookConfig{URL: "://bad"}, "is invalid"},
		"ftp scheme":        {WebhookConfig{URL: "ftp://example.com/hook"}, "must use http or https"},
		"no host":           {WebhookConfig{URL: "https:///hook"}, "has no host"},
		"bad method":        {WebhookConfig{URL: "https://example.com", Method: "DELETE"}, `method "DELETE" is invalid`},
		"unknown event":     {WebhookConfig{URL: "https://example.com", Events: []string{"package.publishd"}}, `unknown event "package.publishd"`},
		"unknown family":    {WebhookConfig{URL: "https://example.com", Events: []string{"run.*"}}, `unknown event "run.*"`},
		"empty event":       {WebhookConfig{URL: "https://example.com", Events: []string{""}}, "unknown event"},
		"empty header name": {WebhookConfig{URL: "https://example.com", Headers: []WebhookHeader{{Value: "v"}}}, "headers[0]: name is required"},
		"colon in header":   {WebhookConfig{URL: "https://example.com", Headers: []WebhookHeader{{Name: "X-Key:", Value: "v"}}}, "must not contain spaces or colons"},
		"space in header":   {WebhookConfig{URL: "https://example.com", Headers: []WebhookHeader{{Name: "X Key", Value: "v"}}}, "must not contain spaces or colons"},
		"negative timeout":  {WebhookConfig{URL: "https://example.com", Timeout: -1}, "timeout must be >= 0"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := minimalConfig()
			cfg.Webhooks = []WebhookConfig{tc.webhook}
			_, err := loadModel(t, cfg, "pkgs/core")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			// Every message names the entry, so a list of several webhooks
			// still points at the one that is wrong.
			assert.Contains(t, err.Error(), "webhooks[0]")
		})
	}
}

func TestLoadWebhooksDuplicateName(t *testing.T) {
	// Names label log lines, so two webhooks sharing one would make a
	// delivery warning ambiguous. The error names both positions.
	cfg := minimalConfig()
	cfg.Webhooks = []WebhookConfig{
		{Name: "ci", URL: "https://a.example.com"},
		{Name: "ci", URL: "https://b.example.com"},
	}
	_, err := loadModel(t, cfg, "pkgs/core")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `webhooks[1]: name "ci" is already used by webhooks[0]`)
}

func TestWebhookLadderReplacesWholesale(t *testing.T) {
	// The webhook list folds through the same ladder as aliasTags: the
	// nearest level that states one wins whole. A package that says nothing
	// inherits the root list; one that states its own replaces it; and an
	// explicit empty list is the opt-out — silence for that package, with
	// the levels above unable to add anything back.
	root := WebhookConfig{Name: "root", URL: "https://root.example.com", Method: "POST"}
	space := WebhookConfig{Name: "space", URL: "https://space.example.com", Method: "POST"}
	own := WebhookConfig{Name: "own", URL: "https://own.example.com", Method: "POST"}

	cfg := validConfig()
	cfg.Webhooks = []WebhookConfig{root}
	withLibs(&cfg, func(s *SpaceConfig) {
		s.Webhooks = []WebhookConfig{space}
		s.Packages = map[string]PackageConfig{
			"core": {Webhooks: []WebhookConfig{own}},
		}
	})
	dir := writeModelRepo(t, cfg, "packages/libs/core", "packages/libs/lib", "packages/apps/app")
	loaded, err := Load(filepath.Join(dir, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err := Discover(loaded, dir)
	require.NoError(t, err)

	byName := map[string][]WebhookConfig{}
	for _, p := range pkgs {
		byName[p.Name] = p.Webhooks
	}
	require.Len(t, byName["core"], 1)
	assert.Equal(t, "own", byName["core"][0].Name, "the package's own list replaces its space's")
	require.Len(t, byName["lib"], 1)
	assert.Equal(t, "space", byName["lib"][0].Name, "an unstated package inherits its space's list")
	require.Len(t, byName["app"], 1)
	assert.Equal(t, "root", byName["app"][0].Name, "a space that says nothing passes the root list down")
}

func TestWebhookEmptyListOptsOut(t *testing.T) {
	// `webhooks: []` written at a package is the opt-out: stated, so it
	// replaces the inherited list with nothing. The typed model cannot
	// express it — omitempty drops an empty list at marshal — so this goes
	// through the raw shape a real file writes, which also pins that viper
	// carries an empty array through where it prunes an empty object.
	root := writeRawRepo(t, map[string]any{
		"scripts":  map[string]any{"build": "echo b"},
		"webhooks": []any{map[string]any{"url": "https://root.example.com"}},
		"spaces": map[string]any{"libs": map[string]any{
			"path": "pkgs", "flow": map[string]any{"build": "build"},
			"packages": map[string]any{"data": map[string]any{"webhooks": []any{}}},
		}},
	}, "pkgs/data", "pkgs/lib")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	pkgs, _, _, err := Discover(loaded, root)
	require.NoError(t, err)
	byName := map[string][]WebhookConfig{}
	for _, p := range pkgs {
		byName[p.Name] = p.Webhooks
	}
	require.NotNil(t, byName["data"], "the empty list is stated, not absent")
	assert.Empty(t, byName["data"], "and it opts the package out entirely")
	assert.Len(t, byName["lib"], 1, "the sibling keeps the inherited list")
}

func TestWebhookLayeredListsAreValidated(t *testing.T) {
	// A broken webhook is refused wherever it is written, with the message
	// naming the level that holds it: a package-level typo must not load
	// just because the root list is clean.
	bad := WebhookConfig{URL: "ftp://example.com"}

	space := validConfig()
	withLibs(&space, func(s *SpaceConfig) { s.Webhooks = []WebhookConfig{bad} })
	_, err := loadModel(t, space, "packages/libs/core", "packages/apps/app")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `webhooks[0]`)
	assert.Contains(t, err.Error(), "must use http or https")

	pkg := validConfig()
	withLibs(&pkg, func(s *SpaceConfig) {
		s.Packages = map[string]PackageConfig{"core": {Webhooks: []WebhookConfig{bad}}}
	})
	_, err = loadModel(t, pkg, "packages/libs/core", "packages/apps/app")
	require.Error(t, err, "a package layer is validated at load, before any discovery")
	assert.Contains(t, err.Error(), `packages["core"]: webhooks[0]`)
	assert.Contains(t, err.Error(), "must use http or https")
}

func TestLoadWebhooksFormat(t *testing.T) {
	// A format template loads when its tokens name payload fields, and is
	// refused when one does not: a template naming a field the payload never
	// carries would render an empty hole on every delivery, forever.
	ok := minimalConfig()
	ok.Webhooks = []WebhookConfig{{URL: "https://example.com",
		Format: `{"text": "released {package} {version}"}`}}
	loaded, err := loadModel(t, ok, "pkgs/core")
	require.NoError(t, err)
	assert.Contains(t, loaded.Webhooks[0].Format, "{package}")

	bad := minimalConfig()
	bad.Webhooks = []WebhookConfig{{URL: "https://example.com", Format: "{pakage} moved"}}
	_, err = loadModel(t, bad, "pkgs/core")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `webhooks[0]: format: unknown field {pakage}`)
}

func TestLoadWebhooksEnvGateAndScriptEvents(t *testing.T) {
	// The env gate accepts the `dispat if` grammar and refuses what that
	// grammar refuses; a subscription may name any script-raised event,
	// because the config cannot know in advance what a script will call its
	// events.
	ok := minimalConfig()
	ok.Webhooks = []WebhookConfig{{URL: "https://example.com", Env: "CI=true",
		Events: []string{"script.deployed", "script.*", "package.published"}}}
	loaded, err := loadModel(t, ok, "pkgs/core")
	require.NoError(t, err)
	assert.Equal(t, "CI=true", loaded.Webhooks[0].Env)

	bad := minimalConfig()
	bad.Webhooks = []WebhookConfig{{URL: "https://example.com", Env: "2CI=x"}}
	_, err = loadModel(t, bad, "pkgs/core")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhooks[0]: env:")
	assert.Contains(t, err.Error(), "not a variable name")
}

func TestLoadWebhooksScalarEventShorthand(t *testing.T) {
	// `events: "package.published"` is the one-subscription shorthand: weak
	// decoding lifts the scalar into a one-element list. Event names contain
	// no commas, so the comma-splitting that plagues other string-to-slice
	// conversions cannot bite here.
	root := writeRawRepo(t, map[string]any{
		"scripts": map[string]any{"build": "echo b"},
		"spaces":  map[string]any{"libs": map[string]any{"path": "pkgs", "flow": map[string]any{"build": "build"}}},
		"webhooks": []any{
			map[string]any{"url": "https://example.com/hook", "events": "package.published"},
		},
	}, "pkgs/core")
	loaded, err := Load(filepath.Join(root, "dispat.json"), nil)
	require.NoError(t, err)
	require.Len(t, loaded.Webhooks, 1)
	assert.Equal(t, []string{"package.published"}, loaded.Webhooks[0].Events)
}
