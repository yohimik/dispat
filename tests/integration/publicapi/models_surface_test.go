package publicapi_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/models"
)

// TestPublicAPIModelOptionPredicates drives every tri-state option field of the
// published configuration model through its nil, false and true states, so the
// defaults a configuration inherits by saying nothing stay the documented ones.
func TestPublicAPIModelOptionPredicates(t *testing.T) {
	t.Run("a changelog defaults to enabled with the documented spacing", func(t *testing.T) {
		var absent *models.ChangelogConfig
		if !absent.IsEnabled() {
			t.Error("an absent changelog is not enabled")
		}
		if got := absent.EntrySpacingOrDefault(); got != models.DefaultEntrySpacing {
			t.Errorf("EntrySpacingOrDefault() = %d, want %d", got, models.DefaultEntrySpacing)
		}
		if absent.RecordChannels() != nil {
			t.Errorf("RecordChannels() = %v, want nil", absent.RecordChannels())
		}
		empty := &models.ChangelogConfig{}
		if !empty.IsEnabled() || empty.EntrySpacingOrDefault() != models.DefaultEntrySpacing {
			t.Error("an empty changelog block changed a default")
		}
		off := &models.ChangelogConfig{
			Enabled:      models.Bool(false),
			EntrySpacing: models.Int(models.MaxEntrySpacing),
			Channels:     []string{"stable"},
		}
		if off.IsEnabled() {
			t.Error("enabled: false did not disable the changelog")
		}
		if got := off.EntrySpacingOrDefault(); got != models.MaxEntrySpacing {
			t.Errorf("EntrySpacingOrDefault() = %d, want %d", got, models.MaxEntrySpacing)
		}
		if got := off.RecordChannels(); !slices.Equal(got, []string{"stable"}) {
			t.Errorf("RecordChannels() = %v", got)
		}
		if models.MinEntrySpacing >= models.MaxEntrySpacing {
			t.Error("the entry-spacing bounds are inverted")
		}
	})

	t.Run("github releases default to enabled and neither draft nor all-packages", func(t *testing.T) {
		var absent *models.GitHubConfig
		if !absent.IsEnabled() || absent.IsAllPackagesEnabled() || absent.IsDraftEnabled() {
			t.Error("an absent github block changed a default")
		}
		if absent.RecordChannels() != nil {
			t.Errorf("RecordChannels() = %v, want nil", absent.RecordChannels())
		}
		empty := &models.GitHubConfig{}
		if !empty.IsEnabled() || empty.IsAllPackagesEnabled() || empty.IsDraftEnabled() {
			t.Error("an empty github block changed a default")
		}
		set := &models.GitHubConfig{
			Enabled:     models.Bool(false),
			AllPackages: models.Bool(true),
			Draft:       models.Bool(true),
			Channels:    []string{"beta"},
		}
		if set.IsEnabled() || !set.IsAllPackagesEnabled() || !set.IsDraftEnabled() {
			t.Errorf("github predicates disagree with %#v", set)
		}
		if got := set.RecordChannels(); !slices.Equal(got, []string{"beta"}) {
			t.Errorf("RecordChannels() = %v", got)
		}
	})

	t.Run("the release commit is off by default and forces and verifies when on", func(t *testing.T) {
		var absent *models.CommitConfig
		if absent.IsEnabled() || absent.IsPushEnabled() {
			t.Error("an absent commit block is enabled")
		}
		if !absent.IsForceEnabled() || !absent.IsVerifyEnabled() {
			t.Error("an absent commit block changed the force or verify default")
		}
		empty := &models.CommitConfig{}
		if empty.IsEnabled() || empty.IsPushEnabled() {
			t.Error("an empty commit block is enabled")
		}
		if !empty.IsForceEnabled() || !empty.IsVerifyEnabled() {
			t.Error("an empty commit block changed the force or verify default")
		}
		on := &models.CommitConfig{
			Enabled: models.Bool(true),
			Push:    true,
			Force:   models.Bool(false),
			Verify:  models.Bool(false),
		}
		if !on.IsEnabled() || !on.IsPushEnabled() {
			t.Error("an enabled pushing commit block reports otherwise")
		}
		if on.IsForceEnabled() || on.IsVerifyEnabled() {
			t.Error("force: false / verify: false did not take effect")
		}
		pushWithoutCommit := &models.CommitConfig{Push: true}
		if pushWithoutCommit.IsPushEnabled() {
			t.Error("push without an enabled commit reports as pushing")
		}
	})

	t.Run("a repository override participates unless it says otherwise", func(t *testing.T) {
		if !(models.RepositoryOverrideConfig{}).IsEnabled() {
			t.Error("an override with no enabled key does not participate")
		}
		off := models.RepositoryOverrideConfig{Enabled: models.Bool(false)}
		if off.IsEnabled() {
			t.Error("enabled: false did not remove the repository")
		}
	})

	t.Run("autoVersion is enabled by the presence of its block", func(t *testing.T) {
		var absent *models.AutoVersionConfig
		if absent.IsEnabled() || absent.IsWriteVersionEnabled() {
			t.Error("an absent autoVersion block is active")
		}
		present := &models.AutoVersionConfig{Manifests: "all"}
		if !present.IsEnabled() || !present.IsWriteVersionEnabled() {
			t.Error("a present autoVersion block is not active")
		}
		off := &models.AutoVersionConfig{
			Enabled:      models.Bool(false),
			WriteVersion: models.Bool(false),
		}
		if off.IsEnabled() || off.IsWriteVersionEnabled() {
			t.Error("enabled: false / writeVersion: false did not take effect")
		}
	})

	t.Run("an alias tag follows the run's force default and its channel list", func(t *testing.T) {
		plain := models.AliasTagConfig{Format: "v{major}", Moving: true}
		if !plain.IsForceEnabled(true) || plain.IsForceEnabled(false) {
			t.Error("an alias with no force key ignored the run's default")
		}
		if !plain.IsApplicableTo("stable") || !plain.IsApplicableTo("beta") {
			t.Error("an alias with no channel list did not apply to every channel")
		}
		pinned := models.AliasTagConfig{
			Format:   "v{major}",
			Force:    models.Bool(false),
			Channels: []string{"Stable"},
		}
		if pinned.IsForceEnabled(true) {
			t.Error("force: false was overridden by the run's default")
		}
		if !pinned.IsApplicableTo("stable") {
			t.Error("the channel list is not matched case-insensitively")
		}
		if pinned.IsApplicableTo("beta") {
			t.Error("the alias applied to a channel outside its list")
		}
	})

	t.Run("the update check defaults to on", func(t *testing.T) {
		var absent *models.File
		if !absent.IsUpdateCheckEnabled() {
			t.Error("an absent file disabled the update check")
		}
		if !(&models.File{}).IsUpdateCheckEnabled() {
			t.Error("a file that never mentions the update check disabled it")
		}
		off := &models.File{UpdateCheck: models.Bool(false)}
		if off.IsUpdateCheckEnabled() {
			t.Error("updateCheck: false did not take effect")
		}
	})

	t.Run("the deprecated spellings answer exactly as the preferred ones do", func(t *testing.T) {
		gh := &models.GitHubConfig{AllPackages: models.Bool(true), Draft: models.Bool(true)}
		//nolint:staticcheck // the deprecated aliases are part of the published surface.
		if gh.AllPackagesEnabled() != gh.IsAllPackagesEnabled() ||
			gh.DraftEnabled() != gh.IsDraftEnabled() {
			t.Error("a github alias disagrees with its preferred spelling")
		}
		commit := &models.CommitConfig{Enabled: models.Bool(true), Push: true}
		//nolint:staticcheck // the deprecated aliases are part of the published surface.
		if commit.PushEnabled() != commit.IsPushEnabled() ||
			commit.ForceEnabled() != commit.IsForceEnabled() ||
			commit.VerifyEnabled() != commit.IsVerifyEnabled() {
			t.Error("a commit alias disagrees with its preferred spelling")
		}
		alias := models.AliasTagConfig{Channels: []string{"beta"}}
		//nolint:staticcheck // the deprecated aliases are part of the published surface.
		if alias.ForceEnabled(true) != alias.IsForceEnabled(true) ||
			alias.AppliesTo("beta") != alias.IsApplicableTo("beta") {
			t.Error("an alias-tag alias disagrees with its preferred spelling")
		}
		auto := &models.AutoVersionConfig{Manifests: "root"}
		//nolint:staticcheck // the deprecated aliases are part of the published surface.
		if auto.WriteVersionEnabled() != auto.IsWriteVersionEnabled() {
			t.Error("an autoVersion alias disagrees with its preferred spelling")
		}
		file := &models.File{UpdateCheck: models.Bool(false)}
		//nolint:staticcheck // the deprecated aliases are part of the published surface.
		if file.UpdateCheckEnabled() != file.IsUpdateCheckEnabled() {
			t.Error("the update-check alias disagrees with its preferred spelling")
		}
	})

	t.Run("the pointer helpers hand back what they were given", func(t *testing.T) {
		if got := models.Bool(true); got == nil || !*got {
			t.Errorf("Bool(true) = %v", got)
		}
		if got := models.Int(7); got == nil || *got != 7 {
			t.Errorf("Int(7) = %v", got)
		}
		if got := models.DefaultNonPackageScopes(); !slices.Equal(got, []string{"release"}) {
			t.Errorf("DefaultNonPackageScopes() = %v", got)
		}
	})
}

// TestPublicAPIModelFoldLookups drives the case-insensitive name resolution
// every configuration map is read through, at each of the levels a package's
// scripts and entries are folded from.
func TestPublicAPIModelFoldLookups(t *testing.T) {
	file := &models.File{
		Scripts: map[string]models.Script{
			"Build": {"npm ci", "npm run build"},
			"test":  {"npm test"},
		},
		Packages: map[string]models.PackageConfig{
			"MyLib": {Versioning: models.VersioningIndependent},
		},
		Spaces: map[string]models.SpaceConfig{
			"Apps": {
				Path:    models.PathList{"apps"},
				Scripts: map[string]models.Script{"Publish": {"npm publish"}},
				Packages: map[string]models.PackageConfig{
					"Web": {TagFormat: "{name}@v{version}"},
				},
			},
		},
	}

	t.Run("a name is found whatever case either side spells it with", func(t *testing.T) {
		if got, _, ok := models.FoldLookup(file.Scripts, "Build"); !ok || got != "Build" {
			t.Errorf("exact lookup = %q, %v", got, ok)
		}
		if got, value, ok := models.FoldLookup(file.Scripts, "BUILD"); !ok || got != "Build" || len(value) != 2 {
			t.Errorf("folded lookup = %q, %v, %v", got, value, ok)
		}
		if key, _, ok := models.FoldLookup(file.Scripts, "absent"); ok || key != "" {
			t.Errorf("missing lookup = %q, %v", key, ok)
		}
	})

	t.Run("a file resolves scripts, packages and spaces", func(t *testing.T) {
		if s, ok := file.Script("build"); !ok || len(s) != 2 {
			t.Errorf("File.Script = %v, %v", s, ok)
		}
		if _, ok := file.Script("absent"); ok {
			t.Error("File.Script found a script that was never declared")
		}
		if pc, ok := file.Package("mylib"); !ok || pc.Versioning != models.VersioningIndependent {
			t.Errorf("File.Package = %#v, %v", pc, ok)
		}
		if key, _, ok := file.PackageEntry("MYLIB"); !ok || key != "MyLib" {
			t.Errorf("File.PackageEntry key = %q, %v", key, ok)
		}
		if sc, ok := file.Space("apps"); !ok || sc.Path.First() != "apps" {
			t.Errorf("File.Space = %#v, %v", sc, ok)
		}
		if key, _, ok := file.SpaceEntry("APPS"); !ok || key != "Apps" {
			t.Errorf("File.SpaceEntry key = %q, %v", key, ok)
		}
	})

	t.Run("a space resolves its own scripts and packages", func(t *testing.T) {
		space := file.Spaces["Apps"]
		if s, ok := space.Script("publish"); !ok || len(s) != 1 {
			t.Errorf("SpaceConfig.Script = %v, %v", s, ok)
		}
		if _, ok := space.Script("build"); ok {
			t.Error("SpaceConfig.Script fell back to the file's table")
		}
		if pc, ok := space.Package("web"); !ok || pc.TagFormat == "" {
			t.Errorf("SpaceConfig.Package = %#v, %v", pc, ok)
		}
		if key, _, ok := space.PackageEntry("WEB"); !ok || key != "Web" {
			t.Errorf("SpaceConfig.PackageEntry key = %q, %v", key, ok)
		}
	})

	t.Run("a space folder's own file resolves its packages", func(t *testing.T) {
		sf := models.SpaceFile{
			Packages: map[string]models.PackageConfig{"Docs": {Src: "docs"}},
		}
		if pc, ok := sf.Package("docs"); !ok || pc.Src != "docs" {
			t.Errorf("SpaceFile.Package = %#v, %v", pc, ok)
		}
		if key, _, ok := sf.PackageEntry("DOCS"); !ok || key != "Docs" {
			t.Errorf("SpaceFile.PackageEntry key = %q, %v", key, ok)
		}
		if _, ok := sf.Package("absent"); ok {
			t.Error("SpaceFile.Package found a package that was never declared")
		}
	})

	t.Run("a reference sequence flattens into the commands it runs", func(t *testing.T) {
		if got := file.Commands(nil); got != nil {
			t.Errorf("Commands(nil) = %v, want nil", got)
		}
		got := file.Commands([]string{"BUILD", "test", "absent"})
		want := []string{"npm ci", "npm run build", "npm test"}
		if !slices.Equal(got, want) {
			t.Errorf("Commands = %v, want %v", got, want)
		}
	})
}

// TestPublicAPIModelScriptShapes round-trips a `scripts` entry through both
// shapes the key accepts and every error its normaliser reports.
func TestPublicAPIModelScriptShapes(t *testing.T) {
	t.Run("one command marshals as a bare string", func(t *testing.T) {
		data, err := json.Marshal(models.Script{"npm test"})
		if err != nil || string(data) != `"npm test"` {
			t.Fatalf("Marshal = %s, %v", data, err)
		}
		value, err := models.Script{"npm test"}.MarshalYAML()
		if err != nil || value != "npm test" {
			t.Errorf("MarshalYAML = %#v, %v", value, err)
		}
	})

	t.Run("several commands marshal as an array", func(t *testing.T) {
		data, err := json.Marshal(models.Script{"npm ci", "npm test"})
		if err != nil || string(data) != `["npm ci","npm test"]` {
			t.Fatalf("Marshal = %s, %v", data, err)
		}
		value, err := models.Script{"npm ci", "npm test"}.MarshalYAML()
		if err != nil {
			t.Fatalf("MarshalYAML: %v", err)
		}
		if list, ok := value.([]string); !ok || len(list) != 2 {
			t.Errorf("MarshalYAML = %#v", value)
		}
	})

	t.Run("an empty script marshals as an empty array", func(t *testing.T) {
		data, err := json.Marshal(models.Script{})
		if err != nil || string(data) != `[]` {
			t.Fatalf("Marshal = %s, %v", data, err)
		}
	})

	t.Run("both written shapes decode to the same sequence", func(t *testing.T) {
		var scalar models.Script
		if err := json.Unmarshal([]byte(`"npm test"`), &scalar); err != nil {
			t.Fatalf("Unmarshal scalar: %v", err)
		}
		var list models.Script
		if err := json.Unmarshal([]byte(`["npm test"]`), &list); err != nil {
			t.Fatalf("Unmarshal list: %v", err)
		}
		if !slices.Equal(scalar, list) {
			t.Errorf("scalar = %v, list = %v", scalar, list)
		}
	})

	t.Run("a malformed script entry is refused", func(t *testing.T) {
		var s models.Script
		if err := json.Unmarshal([]byte(`{`), &s); err == nil {
			t.Error("Unmarshal accepted malformed JSON")
		}
		if err := json.Unmarshal([]byte(`{"a":1}`), &s); err == nil {
			t.Error("Unmarshal accepted an object where a command belongs")
		}
		if err := json.Unmarshal([]byte(`[1]`), &s); err == nil {
			t.Error("Unmarshal accepted a number where a command belongs")
		}
	})

	t.Run("the normaliser reads every shape a reader can hand it", func(t *testing.T) {
		if got, err := models.NormalizeScript(nil, "scripts.build"); err != nil || got != nil {
			t.Errorf("nil = %v, %v", got, err)
		}
		if got, err := models.NormalizeScript("npm test", "scripts.build"); err != nil ||
			!slices.Equal(got, models.Script{"npm test"}) {
			t.Errorf("scalar = %v, %v", got, err)
		}
		if got, err := models.NormalizeScript([]any{"a", "b"}, "scripts.build"); err != nil ||
			!slices.Equal(got, models.Script{"a", "b"}) {
			t.Errorf("list = %v, %v", got, err)
		}
		if got, err := models.NormalizeScript([]string{"a"}, "scripts.build"); err != nil ||
			!slices.Equal(got, models.Script{"a"}) {
			t.Errorf("string list = %v, %v", got, err)
		}
		_, err := models.NormalizeScript([]any{"a", 2}, "scripts.build")
		if err == nil || !strings.Contains(err.Error(), "scripts.build[1]") {
			t.Errorf("a non-string element error = %v, want the index named", err)
		}
		_, err = models.NormalizeScript(42, "scripts.build")
		if err == nil || !strings.Contains(err.Error(), "scripts.build") {
			t.Errorf("a scalar-number error = %v, want the key named", err)
		}
	})
}

// TestPublicAPIModelDependencyShapes round-trips the `dependencies` key through
// its canonical form and drives every error its normalisers report, including
// the map shape a YAML reader can produce.
func TestPublicAPIModelDependencyShapes(t *testing.T) {
	t.Run("edges marshal back into the canonical consumer map", func(t *testing.T) {
		deps := models.Dependencies{
			{Consumer: "app", Provider: "core"},
			{Consumer: "app", Provider: "utils", Keep: true},
			{Consumer: "docs", Provider: "core", Kind: "devDependencies", External: true},
		}
		data, err := json.Marshal(deps)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		const want = `{"app":["core",{"keep":true,"provider":"utils"}],` +
			`"docs":[{"external":true,"kind":"devDependencies","provider":"core"}]}`
		if string(data) != want {
			t.Fatalf("Marshal = %s\nwant %s", data, want)
		}
		value, err := deps.MarshalYAML()
		if err != nil {
			t.Fatalf("MarshalYAML: %v", err)
		}
		grouped, ok := value.(map[string][]any)
		if !ok || len(grouped["app"]) != 2 {
			t.Errorf("MarshalYAML = %#v", value)
		}
		byConsumer := deps.Grouped()
		if len(byConsumer["app"]) != 2 || len(byConsumer["docs"]) != 1 {
			t.Errorf("Grouped() = %#v", byConsumer)
		}
	})

	t.Run("the canonical form round-trips", func(t *testing.T) {
		var decoded models.Dependencies
		src := `{"app":["core",{"provider":"utils","keep":true}],"docs":{"provider":"core"}}`
		if err := json.Unmarshal([]byte(src), &decoded); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if len(decoded) != 3 {
			t.Fatalf("decoded %d edges: %#v", len(decoded), decoded)
		}
		if decoded[0].Consumer != "app" || decoded[2].Consumer != "docs" {
			t.Errorf("consumers are not in sorted order: %#v", decoded)
		}
		again, err := json.Marshal(decoded)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var twice models.Dependencies
		if err := json.Unmarshal(again, &twice); err != nil {
			t.Fatalf("Unmarshal round two: %v", err)
		}
		if !slices.Equal(decoded, twice) {
			t.Errorf("round trip changed the edges:\n%#v\n%#v", decoded, twice)
		}
	})

	t.Run("a package's own provider list keeps the consumer implicit", func(t *testing.T) {
		list := models.Providers("core", "utils")
		if len(list) != 2 || list[0].Consumer != "" {
			t.Fatalf("Providers = %#v", list)
		}
		data, err := json.Marshal(list)
		if err != nil || string(data) != `["core","utils"]` {
			t.Fatalf("Marshal = %s, %v", data, err)
		}
		value, err := list.MarshalYAML()
		if err != nil {
			t.Fatalf("MarshalYAML: %v", err)
		}
		if items, ok := value.([]any); !ok || len(items) != 2 {
			t.Errorf("MarshalYAML = %#v", value)
		}
		var decoded models.ProviderList
		if err := json.Unmarshal([]byte(`"core"`), &decoded); err != nil {
			t.Fatalf("Unmarshal scalar: %v", err)
		}
		if len(decoded) != 1 || decoded[0].Provider != "core" {
			t.Errorf("scalar list = %#v", decoded)
		}
	})

	t.Run("an object entry carries kind, keep and external", func(t *testing.T) {
		var list models.ProviderList
		src := `[{"provider":"core","kind":"peerDependencies","keep":true,"external":true}]`
		if err := json.Unmarshal([]byte(src), &list); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		e := list[0]
		if e.Provider != "core" || e.Kind != "peerDependencies" || !e.Keep || !e.External {
			t.Errorf("edge = %#v", e)
		}
	})

	t.Run("the normalisers read what a YAML reader produces", func(t *testing.T) {
		raw := map[any]any{
			"app": []any{"core", map[any]any{"provider": "utils", "kind": "devDependencies"}},
		}
		deps, err := models.NormalizeDependencies(raw)
		if err != nil {
			t.Fatalf("NormalizeDependencies: %v", err)
		}
		if len(deps) != 2 || deps[1].Kind != "devDependencies" {
			t.Errorf("deps = %#v", deps)
		}
		if got, err := models.NormalizeDependencies(nil); err != nil || got != nil {
			t.Errorf("nil = %v, %v", got, err)
		}
		if got, err := models.NormalizeProviders(nil, "dependencies"); err != nil || got != nil {
			t.Errorf("nil providers = %v, %v", got, err)
		}
		single, err := models.NormalizeProviders(map[string]any{"provider": "core"}, "dependencies")
		if err != nil || len(single) != 1 || single[0].Provider != "core" {
			t.Errorf("lone object = %#v, %v", single, err)
		}
	})

	t.Run("a malformed dependencies value names where it went wrong", func(t *testing.T) {
		cases := []struct {
			name string
			raw  any
			want string
		}{
			{"a list where the consumer map belongs", []any{"core"}, "keyed by consumer"},
			{"a scalar where the consumer map belongs", 42, "keyed by consumer"},
			{"a consumer whose value is nothing", map[string]any{"app": nil}, `dependencies["app"]`},
			{"a consumer whose value is a number", map[string]any{"app": 42}, `dependencies["app"]`},
			{"a provider entry that is a number", map[string]any{"app": []any{42}}, `dependencies["app"][0]`},
			{"a provider name that is not a string",
				map[string]any{"app": []any{map[string]any{"provider": 42}}}, "provider wants"},
			{"a kind that is not a string",
				map[string]any{"app": []any{map[string]any{"kind": 42}}}, "kind wants"},
			{"a keep that is not a boolean",
				map[string]any{"app": []any{map[string]any{"keep": "yes"}}}, "keep wants"},
			{"an external that is not a boolean",
				map[string]any{"app": []any{map[string]any{"external": "yes"}}}, "external wants"},
			{"an unknown key inside a provider object",
				map[string]any{"app": []any{map[string]any{"consumer": "app"}}}, "unknown key"},
			{"an object key that is not a string",
				map[any]any{42: "core"}, "keyed by consumer"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				_, err := models.NormalizeDependencies(c.raw)
				if err == nil {
					t.Fatalf("NormalizeDependencies accepted %#v", c.raw)
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), c.want)
				}
			})
		}
	})

	t.Run("a malformed provider list names where it went wrong", func(t *testing.T) {
		for _, c := range []struct {
			name string
			raw  any
			want string
		}{
			{"a number where a provider belongs", 42, "wants a provider name"},
			{"nothing where a provider belongs", nil, ""},
			{"an element that is a number", []any{42}, "packages.app.dependencies[0]"},
		} {
			t.Run(c.name, func(t *testing.T) {
				got, err := models.NormalizeProviders(c.raw, "packages.app.dependencies")
				if c.want == "" {
					if err != nil || got != nil {
						t.Fatalf("NormalizeProviders = %#v, %v", got, err)
					}
					return
				}
				if err == nil {
					t.Fatalf("NormalizeProviders accepted %#v", c.raw)
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), c.want)
				}
			})
		}
	})

	t.Run("malformed JSON is refused by both decoders", func(t *testing.T) {
		var deps models.Dependencies
		if err := json.Unmarshal([]byte(`{`), &deps); err == nil {
			t.Error("Dependencies accepted malformed JSON")
		}
		// encoding/json checks a document's syntax before it hands the bytes to
		// a custom decoder, so the decoder's own refusal is only reachable by
		// calling it the way a hand-written reader does.
		if err := deps.UnmarshalJSON([]byte(`{`)); err == nil {
			t.Error("Dependencies.UnmarshalJSON accepted malformed bytes")
		}
		var direct models.ProviderList
		if err := direct.UnmarshalJSON([]byte(`[`)); err == nil {
			t.Error("ProviderList.UnmarshalJSON accepted malformed bytes")
		}
		var script models.Script
		if err := script.UnmarshalJSON([]byte(`[`)); err == nil {
			t.Error("Script.UnmarshalJSON accepted malformed bytes")
		}
		if err := json.Unmarshal([]byte(`["core"]`), &deps); err == nil {
			t.Error("Dependencies accepted a list")
		}
		var list models.ProviderList
		if err := json.Unmarshal([]byte(`{`), &list); err == nil {
			t.Error("ProviderList accepted malformed JSON")
		}
		if err := json.Unmarshal([]byte(`42`), &list); err == nil {
			t.Error("ProviderList accepted a number")
		}
	})
}

// TestPublicAPIModelPathListShapes round-trips a space's `path` key through the
// scalar and array forms it accepts.
func TestPublicAPIModelPathListShapes(t *testing.T) {
	if got := (models.PathList{}).First(); got != "" {
		t.Errorf("First() on an empty list = %q", got)
	}
	if got := (models.PathList{"apps", "tools"}).First(); got != "apps" {
		t.Errorf("First() = %q, want the primary folder", got)
	}

	data, err := json.Marshal(models.PathList{"apps"})
	if err != nil || string(data) != `"apps"` {
		t.Fatalf("one folder marshalled to %s, %v", data, err)
	}
	data, err = json.Marshal(models.PathList{"apps", "tools"})
	if err != nil || string(data) != `["apps","tools"]` {
		t.Fatalf("two folders marshalled to %s, %v", data, err)
	}

	var scalar models.PathList
	if err := json.Unmarshal([]byte(`"apps"`), &scalar); err != nil {
		t.Fatalf("Unmarshal scalar: %v", err)
	}
	if !slices.Equal(scalar, models.PathList{"apps"}) {
		t.Errorf("scalar = %v", scalar)
	}
	var list models.PathList
	if err := json.Unmarshal([]byte(`["apps","tools"]`), &list); err != nil {
		t.Fatalf("Unmarshal list: %v", err)
	}
	if !slices.Equal(list, models.PathList{"apps", "tools"}) {
		t.Errorf("list = %v", list)
	}
	if err := json.Unmarshal([]byte(`42`), &list); err == nil {
		t.Error("PathList accepted a number")
	}
}

// TestPublicAPIModelWebhookVocabulary drives the event vocabulary, the
// subscription grammar and the format tokenizer the loader and the delivery
// renderer share.
func TestPublicAPIModelWebhookVocabulary(t *testing.T) {
	t.Run("the event list is fixed and complete", func(t *testing.T) {
		events := models.WebhookEvents()
		for _, want := range []string{
			models.WebhookReleaseStarted, models.WebhookReleaseFinished,
			models.WebhookStageStarted, models.WebhookStageSucceeded,
			models.WebhookPackagePublished, models.WebhookPackageFailed,
			models.WebhookPackageSkipped, models.WebhookPackageCancelled,
			models.WebhookScriptProgress,
		} {
			if !slices.Contains(events, want) {
				t.Errorf("WebhookEvents() is missing %q", want)
			}
		}
		if len(events) != 9 {
			t.Errorf("WebhookEvents() = %d entries, want 9", len(events))
		}
	})

	t.Run("a subscription pattern is accepted or refused", func(t *testing.T) {
		for _, ok := range []string{
			"*", "release.started", "package.*", "stage.*", "release.*", "script.*",
			"script.progress", "script.deployed", "script.step-2", "script.a_b",
		} {
			if !models.IsKnownWebhookPattern(ok) {
				t.Errorf("IsKnownWebhookPattern(%q) = false", ok)
			}
		}
		for _, bad := range []string{
			"", "release.starded", "run.*", "plan.finished", "script.",
			"script.2fast", "script.a b", "nonsense",
		} {
			if models.IsKnownWebhookPattern(bad) {
				t.Errorf("IsKnownWebhookPattern(%q) = true", bad)
			}
		}
	})

	t.Run("a script-raised word is one token", func(t *testing.T) {
		for _, ok := range []string{"deployed", "a", "step-2", "a_b", "Deployed"} {
			if !models.IsWebhookScriptWord(ok) {
				t.Errorf("IsWebhookScriptWord(%q) = false", ok)
			}
		}
		for _, bad := range []string{"", "2fast", "-leading", "_leading", "a b", "a.b", "a/b"} {
			if models.IsWebhookScriptWord(bad) {
				t.Errorf("IsWebhookScriptWord(%q) = true", bad)
			}
		}
		if got := models.WebhookScriptEvent("deployed"); got != "script.deployed" {
			t.Errorf("WebhookScriptEvent = %q", got)
		}
	})

	t.Run("a subscription admits exactly the events it names", func(t *testing.T) {
		for _, c := range []struct {
			pattern, event string
			want           bool
		}{
			{"*", "release.started", true},
			{"*", "script.progress", true},
			{"package.*", "package.failed", true},
			{"package.*", "packages.failed", false},
			{"package.*", "stage.started", false},
			{"release.started", "release.started", true},
			{"release.started", "release.finished", false},
		} {
			if got := models.IsWebhookEventAdmitted(c.pattern, c.event); got != c.want {
				t.Errorf("IsWebhookEventAdmitted(%q, %q) = %v, want %v",
					c.pattern, c.event, got, c.want)
			}
		}
	})

	t.Run("the format fields are the scalar payload fields", func(t *testing.T) {
		fields := models.WebhookFormatFields()
		for _, want := range []string{"event", "package", "version", "message"} {
			if !slices.Contains(fields, want) {
				t.Errorf("WebhookFormatFields() is missing %q", want)
			}
			if !models.IsKnownWebhookFormatField(want) {
				t.Errorf("IsKnownWebhookFormatField(%q) = false", want)
			}
		}
		if slices.Contains(fields, "packages") {
			t.Error("the list-valued packages field must not be a format field")
		}
		for _, bad := range []string{"packages", "", "nonsense"} {
			if models.IsKnownWebhookFormatField(bad) {
				t.Errorf("IsKnownWebhookFormatField(%q) = true", bad)
			}
		}
	})

	t.Run("the tokenizer replaces bare words and nothing else", func(t *testing.T) {
		expand := func(field string) string { return "<" + field + ">" }
		for _, c := range []struct {
			in, want string
		}{
			{"", ""},
			{"no tokens here", "no tokens here"},
			{"{event}", "<event>"},
			{"a {event} b {package} c", "a <event> b <package> c"},
			{`{"text":"{event}"}`, `{"text":"<event>"}`},
			{"{}", "{}"},
			{"{2fast}", "{2fast}"},
			{"{unterminated", "{unterminated"},
			{"{event", "{event"},
			{"}{", "}{"},
			{"{a-b}", "{a-b}"},
		} {
			if got := models.ExpandWebhookFormat(c.in, expand); got != c.want {
				t.Errorf("ExpandWebhookFormat(%q) = %q, want %q", c.in, got, c.want)
			}
		}
	})

	t.Run("a webhook declaration round-trips as configuration", func(t *testing.T) {
		wh := models.WebhookConfig{
			Name:      "dashboard",
			URL:       "https://example.invalid/hook",
			Method:    "POST",
			Events:    []string{"package.*"},
			Headers:   []models.WebhookHeader{{Name: "X-Token", Value: "$HOOK_TOKEN"}},
			SecretEnv: "HOOK_SECRET",
			Timeout:   10,
			Env:       "CI=true",
			Format:    `{"text":"{package} {version}"}`,
		}
		data, err := json.Marshal(wh)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var back models.WebhookConfig
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if back.Name != wh.Name || back.Headers[0].Name != "X-Token" || back.Format != wh.Format {
			t.Errorf("round trip changed the webhook: %#v", back)
		}
		for _, p := range back.Events {
			if !models.IsKnownWebhookPattern(p) {
				t.Errorf("the round-tripped subscription %q is not known", p)
			}
		}
	})

	t.Run("the deprecated webhook spellings answer as the preferred ones do", func(t *testing.T) {
		//nolint:staticcheck // the deprecated aliases are part of the published surface.
		if models.KnownWebhookPattern("package.*") != models.IsKnownWebhookPattern("package.*") {
			t.Error("KnownWebhookPattern disagrees with its preferred spelling")
		}
		//nolint:staticcheck // the deprecated aliases are part of the published surface.
		if models.KnownWebhookFormatField("event") != models.IsKnownWebhookFormatField("event") {
			t.Error("KnownWebhookFormatField disagrees with its preferred spelling")
		}
		//nolint:staticcheck // the deprecated aliases are part of the published surface.
		if models.MatchWebhookEvent("package.*", "package.failed") !=
			models.IsWebhookEventAdmitted("package.*", "package.failed") {
			t.Error("MatchWebhookEvent disagrees with its preferred spelling")
		}
	})
}

// TestPublicAPIModelFileRoundTrip authors a whole configuration as typed values
// and marshals it back into the loadable document the CLI reads, which is what
// the published model exists to make possible.
func TestPublicAPIModelFileRoundTrip(t *testing.T) {
	file := models.File{
		LogLevel:  "debug",
		LogFormat: "json",
		TagFormat: "{name}@v{version}",
		Shell:     []string{"/bin/sh", "-c"},
		Scripts: map[string]models.Script{
			"build":   {"npm run build"},
			"publish": {"npm ci", "npm publish"},
		},
		Spaces: map[string]models.SpaceConfig{
			"apps": {
				Path:       models.PathList{"apps"},
				Versioning: models.VersioningFixedMajorMinorSparse,
				Flow:       &models.SpaceFlowConfig{Build: []string{"build"}, Publish: []string{"publish"}},
				AutoVersion: &models.AutoVersionConfig{
					Manifests:           "all",
					Range:               "caret",
					SyncLock:            []string{"build"},
					SyncLockConcurrency: 1,
				},
			},
		},
		Packages: map[string]models.PackageConfig{
			"web": {
				Dependencies: models.Providers("core"),
				AliasTags: []models.AliasTagConfig{
					{Format: "v{major}", Moving: true, Channels: []string{"stable"}},
				},
			},
		},
		VersionGroups: map[string]models.VersionGroupConfig{
			"libs": {Versioning: models.VersioningFixed},
		},
		Dependencies: models.Dependencies{{Consumer: "docs", Provider: "core"}},
		Changelog:    &models.ChangelogConfig{EntrySpacing: models.Int(3)},
		GitHub:       &models.GitHubConfig{Enabled: models.Bool(false)},
		Commit:       &models.CommitConfig{Enabled: models.Bool(true), Push: true},
		Webhooks: []models.WebhookConfig{
			{URL: "https://example.invalid/hook", Events: []string{"*"}},
		},
		Env:         map[string]string{"CI": "true"},
		Custom:      map[string]any{"ours": map[string]any{"k": "v"}},
		Initials:    map[string]string{"web": "1.0.0"},
		UpdateCheck: models.Bool(false),
	}

	data, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"path":"apps"`) {
		t.Errorf("a single space folder did not marshal as a scalar: %s", data)
	}
	if !strings.Contains(string(data), `"build":"npm run build"`) {
		t.Errorf("a single-command script did not marshal as a scalar: %s", data)
	}
	if !strings.Contains(string(data), `"dependencies":{"docs":["core"]}`) {
		t.Errorf("the top-level edges did not marshal canonically: %s", data)
	}

	var back models.File
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Spaces["apps"].Path.First() != "apps" {
		t.Errorf("space path = %v", back.Spaces["apps"].Path)
	}
	if got, ok := back.Script("PUBLISH"); !ok || len(got) != 2 {
		t.Errorf("Script(PUBLISH) = %v, %v", got, ok)
	}
	if back.IsUpdateCheckEnabled() {
		t.Error("updateCheck: false did not survive the round trip")
	}
	if !back.Commit.IsPushEnabled() || back.GitHub.IsEnabled() {
		t.Errorf("the option blocks did not survive the round trip: %#v %#v", back.Commit, back.GitHub)
	}
	if got := back.Changelog.EntrySpacingOrDefault(); got != 3 {
		t.Errorf("entry spacing = %d, want 3", got)
	}
	if len(back.Dependencies) != 1 || back.Dependencies[0].Consumer != "docs" {
		t.Errorf("edges = %#v", back.Dependencies)
	}
	if got := back.Packages["web"].Dependencies; len(got) != 1 || got[0].Provider != "core" {
		t.Errorf("package edges = %#v", got)
	}
	alias := back.Packages["web"].AliasTags[0]
	if !alias.IsApplicableTo("stable") || alias.IsApplicableTo("beta") {
		t.Errorf("alias channels did not survive the round trip: %#v", alias)
	}

	again, err := json.Marshal(back)
	if err != nil {
		t.Fatalf("Marshal round two: %v", err)
	}
	if string(again) != string(data) {
		t.Errorf("the document is not stable across a round trip:\n%s\n%s", data, again)
	}
}
