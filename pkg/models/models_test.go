package models

import (
	"encoding/json"
	"testing"
)

// The models are data; what is worth testing here is the pure helpers and
// the one contract external tooling relies on: a marshalled model is a
// loadable config (the json tags are the config file's own keys — the CLI's
// own config tests load marshalled models end to end).

func TestEnabledDefaults(t *testing.T) {
	if !(&ChangelogConfig{}).IsEnabled() {
		t.Error("changelog defaults to enabled")
	}
	if (&ChangelogConfig{Enabled: Bool(false)}).IsEnabled() {
		t.Error("changelog can be disabled")
	}
	if !(&GitHubConfig{}).IsEnabled() {
		t.Error("github defaults to enabled")
	}
	if (&GitHubConfig{Enabled: Bool(false)}).IsEnabled() {
		t.Error("github can be disabled")
	}
	if (&CommitConfig{}).IsEnabled() {
		t.Error("the release commit defaults to disabled")
	}
	if !(&CommitConfig{Enabled: Bool(true)}).IsEnabled() {
		t.Error("the release commit can be enabled")
	}
	if (&CommitConfig{Enabled: Bool(true)}).PushEnabled() {
		t.Error("push needs its own flag")
	}
	if !(&CommitConfig{Enabled: Bool(true), Push: true}).PushEnabled() {
		t.Error("push follows commit")
	}
	if (&CommitConfig{Push: true}).PushEnabled() {
		t.Error("push without commit is inert")
	}
	if len((&ChangelogConfig{}).RecordChannels()) != 0 {
		t.Error("every release gets a changelog entry by default")
	}
	if got := (&ChangelogConfig{Channels: []string{"stable"}}).RecordChannels(); len(got) != 1 ||
		got[0] != "stable" {
		t.Error("the changelog can name the channels it records on")
	}
	if len((&GitHubConfig{}).RecordChannels()) != 0 {
		t.Error("every release gets a github release by default")
	}
	if got := (&GitHubConfig{Channels: []string{"beta"}}).RecordChannels(); len(got) != 1 ||
		got[0] != "beta" {
		t.Error("github releases can name the channels they are created on")
	}
	var nilChangelog *ChangelogConfig
	var nilGitHub *GitHubConfig
	if nilChangelog.RecordChannels() != nil || nilGitHub.RecordChannels() != nil {
		t.Error("an absent object means every default, every channel included")
	}
	if !(&CommitConfig{}).VerifyEnabled() {
		t.Error("push verification defaults to enabled")
	}
	if !(&CommitConfig{Verify: Bool(true)}).VerifyEnabled() {
		t.Error("verification can be stated explicitly")
	}
	if (&CommitConfig{Verify: Bool(false)}).VerifyEnabled() {
		t.Error("verification can be disabled")
	}
	if !(&File{}).UpdateCheckEnabled() {
		t.Error("the update check defaults to enabled")
	}
	if (&File{UpdateCheck: Bool(false)}).UpdateCheckEnabled() {
		t.Error("the update check can be disabled")
	}
	var nilFile *File
	if !nilFile.UpdateCheckEnabled() {
		t.Error("an absent file means every default")
	}
}

// The entry seam is the one option whose absence and whose zero are different
// answers: a file writing 0 is refused by the loader, while a file writing
// nothing takes the default. The pointer is what tells the two apart, and Int
// is how a caller building a model in code writes one.
func TestEntrySpacingDefaults(t *testing.T) {
	if got := (&ChangelogConfig{}).EntrySpacingOrDefault(); got != DefaultEntrySpacing {
		t.Errorf("an unset entrySpacing takes the default: got %d, want %d", got, DefaultEntrySpacing)
	}
	if got := (&ChangelogConfig{EntrySpacing: Int(1)}).EntrySpacingOrDefault(); got != 1 {
		t.Errorf("a stated entrySpacing is the answer: got %d, want 1", got)
	}
	if got := (&ChangelogConfig{EntrySpacing: Int(MaxEntrySpacing)}).EntrySpacingOrDefault(); got != MaxEntrySpacing {
		t.Errorf("the ceiling is a value like any other: got %d, want %d", got, MaxEntrySpacing)
	}
	var nilChangelog *ChangelogConfig
	if got := nilChangelog.EntrySpacingOrDefault(); got != DefaultEntrySpacing {
		t.Errorf("an absent object means the default: got %d, want %d", got, DefaultEntrySpacing)
	}
	if n := Int(3); n == nil || *n != 3 {
		t.Error("Int carries the value it was given")
	}
	if MinEntrySpacing >= MaxEntrySpacing || DefaultEntrySpacing < MinEntrySpacing ||
		DefaultEntrySpacing > MaxEntrySpacing {
		t.Error("the default has to sit inside the bounds the loader validates against")
	}
}

// TestRecordFormatRoundTrip: the record options added for links, sections and
// commit references marshal under their config keys and survive the trip. The
// typed integration harness authors configs by marshalling these models, so a
// tag that drifts from the loader's field table is a key a real run rejects.
func TestRecordFormatRoundTrip(t *testing.T) {
	f := File{
		Changelog: &ChangelogConfig{
			EntrySpacing: Int(1),
			EntryFormatConfig: EntryFormatConfig{
				DependencyLink: "auto",
				NoChangesText:  "see the dispat changelog",
				Sections: []SectionConfig{
					{Title: "Added", Types: []string{"add"}, Bump: "minor"},
					{Title: "features"},
				},
				CommitRefs: &CommitRefsConfig{Placement: "suffix", Link: "auto"},
			},
		},
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	cl := raw["changelog"].(map[string]any)
	for _, key := range []string{"entrySpacing", "dependencyLink", "noChangesText", "sections", "commitRefs"} {
		if _, ok := cl[key]; !ok {
			t.Errorf("changelog is missing key %q: %s", key, data)
		}
	}

	var back File
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	got := back.Changelog
	if got.EntrySpacing == nil || *got.EntrySpacing != 1 {
		t.Errorf("entrySpacing lost: %+v", got.EntrySpacing)
	}
	if got.DependencyLink != "auto" || got.NoChangesText != "see the dispat changelog" {
		t.Errorf("link and text fields lost: %+v", got.EntryFormatConfig)
	}
	if len(got.Sections) != 2 || got.Sections[0].Title != "Added" ||
		got.Sections[0].Bump != "minor" || len(got.Sections[0].Types) != 1 ||
		got.Sections[1].Title != "features" {
		t.Errorf("sections lost: %+v", got.Sections)
	}
	if got.CommitRefs == nil || got.CommitRefs.Placement != "suffix" || got.CommitRefs.Link != "auto" {
		t.Errorf("commitRefs lost: %+v", got.CommitRefs)
	}
	// A format that never set the new options marshals without their keys, so
	// yesterday's configs keep reading exactly as they were written.
	plain, err := json.Marshal(File{Changelog: &ChangelogConfig{}})
	if err != nil {
		t.Fatal(err)
	}
	var plainRaw map[string]any
	if err := json.Unmarshal(plain, &plainRaw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"entrySpacing", "dependencyLink", "noChangesText", "sections", "commitRefs"} {
		if _, ok := plainRaw["changelog"].(map[string]any)[key]; ok {
			t.Errorf("unset option %q must not marshal: %s", key, plain)
		}
	}
}

func TestScriptLookupsAreCaseInsensitive(t *testing.T) {
	// A map key holds the case its file wrote, and a name is matched against it
	// case-insensitively, so neither side has to remember the other's spelling.
	f := File{Scripts: map[string]Script{"build": {"make"}}}
	if s, ok := f.Script("BUILD"); !ok || len(s) != 1 || s[0] != "make" {
		t.Errorf("Script(BUILD) = %q, %v", s, ok)
	}
	if _, ok := f.Script("missing"); ok {
		t.Error("unknown scripts do not resolve")
	}

	sc := SpaceConfig{Scripts: map[string]Script{"lint": {"npm run lint"}}}
	if s, ok := sc.Script("LINT"); !ok || len(s) != 1 || s[0] != "npm run lint" {
		t.Errorf("SpaceConfig.Script(LINT) = %q, %v", s, ok)
	}
	if _, ok := sc.Script("format"); ok {
		t.Error("unknown scripts do not resolve")
	}

	pf := File{Packages: map[string]PackageConfig{"core": {TagFormat: "v{version}"}}}
	if pc, ok := pf.Package("Core"); !ok || pc.TagFormat != "v{version}" {
		t.Errorf("Package(Core) = %+v, %v", pc, ok)
	}
	if _, ok := pf.Package("app"); ok {
		t.Error("packages without an entry do not resolve")
	}

	// The same lookup at the two levels that hold a packages map of their own:
	// a space in the root file, and a space folder's config file.
	sp := SpaceConfig{Packages: map[string]PackageConfig{"core": {TagFormat: "s{version}"}}}
	if pc, ok := sp.Package("CORE"); !ok || pc.TagFormat != "s{version}" {
		t.Errorf("SpaceConfig.Package(CORE) = %+v, %v", pc, ok)
	}
	if _, ok := sp.Package("app"); ok {
		t.Error("packages without an entry do not resolve")
	}

	sf := SpaceFile{Packages: map[string]PackageConfig{"core": {TagFormat: "f{version}"}}}
	if pc, ok := sf.Package("Core"); !ok || pc.TagFormat != "f{version}" {
		t.Errorf("SpaceFile.Package(Core) = %+v, %v", pc, ok)
	}
	if _, ok := sf.Package("app"); ok {
		t.Error("packages without an entry do not resolve")
	}

	// Spaces resolve by name the same way, for the same folding reason.
	sfile := File{Spaces: map[string]SpaceConfig{"libs": {Path: PathList{"packages"}}}}
	if s, ok := sfile.Space("LIBS"); !ok || s.Path.First() != "packages" {
		t.Errorf("Space(LIBS) = %+v, %v", s, ok)
	}
	if _, ok := sfile.Space("apps"); ok {
		t.Error("spaces without an entry do not resolve")
	}
}

// TestLookupsMatchWhicheverCaseIsWritten: a map key spelled with capitals is
// found by a name spelled without them, and the other way round, which is what
// makes the config language case-insensitive without renaming anything. The
// entry variants answer with the key as well, because a caller recording what
// it consumed, or editing it, has to name it the way the author did.
func TestLookupsMatchWhicheverCaseIsWritten(t *testing.T) {
	f := File{
		Scripts:  map[string]Script{"Build": {"make"}},
		Packages: map[string]PackageConfig{"MyLib": {TagFormat: "v{version}"}},
		Spaces:   map[string]SpaceConfig{"Libs": {Path: PathList{"packages"}}},
	}
	if s, ok := f.Script("build"); !ok || s[0] != "make" {
		t.Errorf("Script(build) against a Build key = %q, %v", s, ok)
	}
	if pc, ok := f.Package("mylib"); !ok || pc.TagFormat != "v{version}" {
		t.Errorf("Package(mylib) against a MyLib key = %+v, %v", pc, ok)
	}
	if key, pc, ok := f.PackageEntry("MYLIB"); !ok || key != "MyLib" || pc.TagFormat != "v{version}" {
		t.Errorf("PackageEntry(MYLIB) = %q, %+v, %v; want the author's spelling", key, pc, ok)
	}
	if key, sc, ok := f.SpaceEntry("libs"); !ok || key != "Libs" || sc.Path.First() != "packages" {
		t.Errorf("SpaceEntry(libs) = %q, %+v, %v; want the author's spelling", key, sc, ok)
	}
	if key, _, ok := f.PackageEntry("absent"); ok || key != "" {
		t.Errorf("PackageEntry(absent) = %q, %v; want no key at all", key, ok)
	}

	sc := SpaceConfig{Packages: map[string]PackageConfig{"MyLib": {TagFormat: "s{version}"}}}
	if key, pc, ok := sc.PackageEntry("mylib"); !ok || key != "MyLib" || pc.TagFormat != "s{version}" {
		t.Errorf("SpaceConfig.PackageEntry(mylib) = %q, %+v, %v", key, pc, ok)
	}
	sf := SpaceFile{Packages: map[string]PackageConfig{"MyLib": {TagFormat: "f{version}"}}}
	if key, pc, ok := sf.PackageEntry("MYLIB"); !ok || key != "MyLib" || pc.TagFormat != "f{version}" {
		t.Errorf("SpaceFile.PackageEntry(MYLIB) = %q, %+v, %v", key, pc, ok)
	}

	// The exact key wins before the scan begins, which is what keeps the common
	// lookup one map read.
	if key, _, ok := FoldLookup(map[string]int{"a": 1}, "a"); !ok || key != "a" {
		t.Errorf("FoldLookup(a) = %q, %v", key, ok)
	}
	if _, _, ok := FoldLookup(map[string]int{"a": 1}, "b"); ok {
		t.Error("a name nothing spells does not resolve")
	}
}

func TestCommandsPreservesOrder(t *testing.T) {
	f := File{Scripts: map[string]Script{"a": {"cmd-a"}, "b": {"cmd-b"}}}
	got := f.Commands([]string{"b", "a"})
	if len(got) != 2 || got[0] != "cmd-b" || got[1] != "cmd-a" {
		t.Errorf("Commands = %v", got)
	}
	if f.Commands(nil) != nil {
		t.Error("no refs, no commands")
	}
}

func TestDefaultNonPackageScopes(t *testing.T) {
	got := DefaultNonPackageScopes()
	if len(got) != 1 || got[0] != "release" {
		t.Errorf("DefaultNonPackageScopes = %v", got)
	}
}

func TestPathListRoundTrip(t *testing.T) {
	// One folder marshals back to the scalar form the author most likely
	// wrote, several stay a list, and both shapes unmarshal.
	one, err := json.Marshal(PathList{"packages"})
	if err != nil {
		t.Fatal(err)
	}
	if string(one) != `"packages"` {
		t.Errorf("a single path marshals as a scalar, got %s", one)
	}
	two, err := json.Marshal(PathList{"packages", "more"})
	if err != nil {
		t.Fatal(err)
	}
	if string(two) != `["packages","more"]` {
		t.Errorf("several paths marshal as a list, got %s", two)
	}

	for raw, want := range map[string]int{`"packages"`: 1, `["packages","more"]`: 2, `[]`: 0} {
		var p PathList
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		if len(p) != want {
			t.Errorf("unmarshal %s: got %v", raw, p)
		}
	}
	var p PathList
	if err := json.Unmarshal([]byte(`7`), &p); err == nil {
		t.Error("a number is neither shape and must fail")
	}

	if got := (PathList{"a", "b"}).First(); got != "a" {
		t.Errorf("First() = %q", got)
	}
	if got := (PathList{}).First(); got != "" {
		t.Errorf("First() of an empty list = %q", got)
	}
}

func TestMarshalledModelUsesTheConfigKeys(t *testing.T) {
	// The json tag is the config file's key, so a marshalled model is a
	// loadable dispat.json; resolved fields never leak into the file.
	f := File{
		Scripts: map[string]Script{"build": {"make"}},
		Spaces: map[string]SpaceConfig{
			"libs": {Path: PathList{"packages"}, Versioning: VersioningFixed,
				Flow:    &SpaceFlowConfig{Build: []string{"build"}},
				Scripts: map[string]Script{"lint": {"make lint"}}},
		},
		Dependencies:     []DependencyConfig{{Consumer: "app", Provider: "core"}},
		BuildConcurrency: 99, // resolved: must not marshal
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	space := raw["spaces"].(map[string]any)["libs"].(map[string]any)
	for _, key := range []string{"path", "versioning", "flow", "scripts"} {
		if _, ok := space[key]; !ok {
			t.Errorf("space is missing key %q: %v", key, space)
		}
	}
	if _, ok := raw["BuildConcurrency"]; ok {
		t.Error("resolved fields must not marshal")
	}
	// The optional sub-objects are pointers precisely so that a model that
	// never set them marshals without the keys at all — not as "{}" noise.
	for _, key := range []string{"changelog", "github", "commit", "run", "parser"} {
		if _, ok := raw[key]; ok {
			t.Errorf("unset optional object %q must not marshal", key)
		}
	}
	if string(data) != "" && json.Valid(data) != true {
		t.Error("marshalled config must be valid JSON")
	}
}

func TestPackageConfigRoundTrip(t *testing.T) {
	// A package override marshals under the config keys and survives the trip
	// with its tri-state pointers intact — nil stays absent (inherit), an
	// explicit false stays false (override), which is the whole reason the
	// scalars are pointers here where SpaceConfig's are plain.
	f := File{
		VersionGroups: map[string]VersionGroupConfig{"core": {Versioning: VersioningFixed}},
		Spaces: map[string]SpaceConfig{
			"libs": {Path: PathList{"packages"}},
		},
		Packages: map[string]PackageConfig{
			"app": {
				RevertOnFail: Bool(false),
				VersionGroup: "core",
				Concurrency:  []int{2, 1},
				Changelog:    &ChangelogConfig{Enabled: Bool(false)},
				Dependencies: Providers("core"),
			},
			"cli": {Path: "tools/cli"},
		},
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var back File
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	pc, ok := back.Package("app")
	if !ok {
		t.Fatalf("override lost in the round trip: %s", data)
	}
	if pc.RevertOnFail == nil || *pc.RevertOnFail {
		t.Errorf("explicit false must survive as false, got %v", pc.RevertOnFail)
	}
	if pc.IsBuildWaitingPublish != nil {
		t.Errorf("unset pointer must survive as nil, got %v", pc.IsBuildWaitingPublish)
	}
	if pc.VersionGroup != "core" || len(pc.Concurrency) != 2 {
		t.Errorf("scalar fields lost: %+v", pc)
	}
	if len(pc.Dependencies) != 1 || pc.Dependencies[0].Provider != "core" {
		t.Errorf("dependencies lost: %+v", pc)
	}
	if cli, ok := back.Package("cli"); !ok || cli.Path != "tools/cli" {
		t.Errorf("standalone path lost: %+v", cli)
	}
	if back.VersionGroups["core"].Versioning != VersioningFixed {
		t.Errorf("versionGroups lost: %+v", back.VersionGroups)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["versionGroups"]; !ok {
		t.Errorf("versionGroups must marshal under its config key: %s", data)
	}
}

func TestAutoVersionConfigAccessors(t *testing.T) {
	var nilAV *AutoVersionConfig
	if nilAV.IsEnabled() {
		t.Error("a nil autoVersion block is off")
	}
	if nilAV.WriteVersionEnabled() {
		t.Error("a nil block writes nothing")
	}
	if !(&AutoVersionConfig{}).IsEnabled() {
		t.Error("a present block defaults to enabled")
	}
	if (&AutoVersionConfig{Enabled: Bool(false)}).IsEnabled() {
		t.Error("enabled:false turns the block off")
	}
	if !(&AutoVersionConfig{}).WriteVersionEnabled() {
		t.Error("writeVersion defaults to true")
	}
	if (&AutoVersionConfig{WriteVersion: Bool(false)}).WriteVersionEnabled() {
		t.Error("writeVersion can be disabled")
	}
}

func TestGitHubAllPackagesEnabled(t *testing.T) {
	var nilCfg *GitHubConfig
	if nilCfg.AllPackagesEnabled() {
		t.Error("nil config: disabled")
	}
	if (&GitHubConfig{}).AllPackagesEnabled() {
		t.Error("unset field: disabled")
	}
	if !(&GitHubConfig{AllPackages: Bool(true)}).AllPackagesEnabled() {
		t.Error("set true: enabled")
	}
	if (&GitHubConfig{AllPackages: Bool(false)}).AllPackagesEnabled() {
		t.Error("set false: disabled")
	}
}

func TestGitHubDraftEnabled(t *testing.T) {
	var nilCfg *GitHubConfig
	if nilCfg.DraftEnabled() {
		t.Error("nil config: disabled")
	}
	if (&GitHubConfig{}).DraftEnabled() {
		t.Error("unset field: disabled")
	}
	if !(&GitHubConfig{Draft: Bool(true)}).DraftEnabled() {
		t.Error("set true: enabled")
	}
	if (&GitHubConfig{Draft: Bool(false)}).DraftEnabled() {
		t.Error("set false: disabled")
	}
}

func TestSpaceFileRoundTrip(t *testing.T) {
	// A space folder's config file marshals under the config keys and keeps
	// its tri-state pointers, for the same reason a package override does: an
	// explicit false has to override the root file's true, and an unset key
	// has to stay absent so it inherits.
	sf := SpaceFile{
		RevertOnFail: Bool(false),
		TagFormat:    "libs/{name}@{version}",
		Scripts:      map[string]Script{"build": {"make"}},
		Flow:         &SpaceFlowConfig{Build: []string{"build"}},
		Packages: map[string]PackageConfig{
			"core": {IsBuildWaitingPublish: Bool(true), Dependencies: Providers("utils")},
		},
	}
	data, err := json.Marshal(sf)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	// The space's own location is the folder the file sits in, so the model
	// has no path field at all to marshal one.
	if _, ok := raw["path"]; ok {
		t.Errorf("a space file must not carry a path: %s", data)
	}
	for _, key := range []string{"revertOnFail", "tagFormat", "scripts", "flow", "packages"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("space file is missing key %q: %s", key, data)
		}
	}
	if _, ok := raw["versioning"]; ok {
		t.Errorf("an unset scalar must not marshal: %s", data)
	}

	var back SpaceFile
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.RevertOnFail == nil || *back.RevertOnFail {
		t.Errorf("explicit false must survive as false, got %v", back.RevertOnFail)
	}
	if back.IsBuildWaitingPublish != nil {
		t.Errorf("unset pointer must survive as nil, got %v", back.IsBuildWaitingPublish)
	}
	pc, ok := back.Package("core")
	if !ok {
		t.Fatalf("packages entry lost in the round trip: %s", data)
	}
	if pc.IsBuildWaitingPublish == nil || !*pc.IsBuildWaitingPublish {
		t.Errorf("the entry's own pointer must survive: %+v", pc)
	}
	if len(pc.Dependencies) != 1 || pc.Dependencies[0].Provider != "utils" {
		t.Errorf("entry dependencies lost: %+v", pc)
	}
}

func TestEnvAndCustomRoundTrip(t *testing.T) {
	// The static env and the free-form custom object exist at four levels,
	// and every one of them has to survive a marshal/unmarshal cycle under
	// its config key: the model is what external tooling authors configs
	// with, and an env key that does not round-trip is a variable a script
	// never sees.
	f := File{
		Env:    map[string]string{"MiXed_Case": "kept", "SHARED": "root"},
		Custom: map[string]any{"team": "platform"},
		Run:    &RunConfig{AllowBranch: []string{"main", "release/*"}},
		Spaces: map[string]SpaceConfig{
			"libs": {
				Path:   PathList{"packages"},
				Env:    map[string]string{"SHARED": "space"},
				Custom: map[string]any{"owner": "libs-team"},
			},
		},
		Packages: map[string]PackageConfig{
			"core": {
				Env:    map[string]string{"SHARED": "package"},
				Custom: map[string]any{"tier": "1"},
			},
		},
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"env", "custom"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("top level is missing key %q: %s", key, data)
		}
		space := raw["spaces"].(map[string]any)["libs"].(map[string]any)
		if _, ok := space[key]; !ok {
			t.Errorf("space is missing key %q: %s", key, data)
		}
		pkg := raw["packages"].(map[string]any)["core"].(map[string]any)
		if _, ok := pkg[key]; !ok {
			t.Errorf("package entry is missing key %q: %s", key, data)
		}
	}
	if _, ok := raw["run"].(map[string]any)["allowBranch"]; !ok {
		t.Errorf("run.allowBranch must marshal under its config key: %s", data)
	}

	var back File
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	// Case is the whole point of the env objects: the model must not fold it.
	if back.Env["MiXed_Case"] != "kept" {
		t.Errorf("env key case lost in the round trip: %+v", back.Env)
	}
	if back.Spaces["libs"].Env["SHARED"] != "space" {
		t.Errorf("space env lost: %+v", back.Spaces["libs"].Env)
	}
	pc, ok := back.Package("core")
	if !ok || pc.Env["SHARED"] != "package" {
		t.Errorf("package env lost: %+v", pc)
	}
	if back.Custom["team"] != "platform" || pc.Custom["tier"] != "1" {
		t.Errorf("custom objects lost: %+v / %+v", back.Custom, pc.Custom)
	}
	if len(back.Run.AllowBranch) != 2 || back.Run.AllowBranch[1] != "release/*" {
		t.Errorf("allowBranch lost: %+v", back.Run)
	}
}

func TestSpaceFileCarriesEnvAndCustom(t *testing.T) {
	// A space folder's own config file is a fourth env layer, so it needs the
	// same two keys as the root file's space entry.
	sf := SpaceFile{
		Env:    map[string]string{"GOFLAGS": "-mod=mod"},
		Custom: map[string]any{"note": "in-folder"},
	}
	data, err := json.Marshal(sf)
	if err != nil {
		t.Fatal(err)
	}
	var back SpaceFile
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Env["GOFLAGS"] != "-mod=mod" {
		t.Errorf("space file env lost: %s", data)
	}
	if back.Custom["note"] != "in-folder" {
		t.Errorf("space file custom lost: %s", data)
	}
}
