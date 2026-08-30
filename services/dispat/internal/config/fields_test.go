package config

// The one test that reflects, and the reason it is allowed to.
//
// The field tables are handwritten, which is what makes the decoder a table
// lookup instead of a walk over the model's types. The cost of writing them by
// hand is that they can fall behind the model: a field added to a struct with
// no line in its table has no key, so a file naming it fails with an unknown
// key that is perfectly true and completely baffling.
//
// So the models' own json tags are the oracle, read here with reflect — in a
// test, where the reflect surface costs nothing at runtime and nothing to the
// TinyGo fork — and compared against the tables in both directions. A field
// with no key and a key with no field both fail by name.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFieldTablesCoverEveryModelField compares each table against the struct
// it decodes into, key by key.
func TestFieldTablesCoverEveryModelField(t *testing.T) {
	for _, c := range []struct {
		name  string
		model any
		table fields
	}{
		{"File", File{}, fileFields(&File{})},
		{"SpaceConfig", SpaceConfig{}, spaceConfigFields(&SpaceConfig{})},
		{"SpaceFile", SpaceFile{}, spaceFileFields(&SpaceFile{})},
		{"PackageConfig", PackageConfig{}, packageConfigFields(&PackageConfig{})},
		{"VersionGroupConfig", VersionGroupConfig{}, versionGroupFields(&VersionGroupConfig{})},
		{"SpaceFlowConfig", SpaceFlowConfig{}, spaceFlowFields(&SpaceFlowConfig{})},
		{"RunConfig", RunConfig{}, runFields(&RunConfig{})},
		{"ChangelogConfig", ChangelogConfig{}, changelogFields(&ChangelogConfig{})},
		{"GitHubConfig", GitHubConfig{}, gitHubFields(&GitHubConfig{})},
		{"EntryFormatConfig", EntryFormatConfig{}, entryFormatFields(&EntryFormatConfig{})},
		{"EntryLine", EntryLine{}, entryLineFields(&EntryLine{})},
		{"AuthorsConfig", AuthorsConfig{}, authorsFields(&AuthorsConfig{})},
		{"SectionConfig", SectionConfig{}, sectionFields(&SectionConfig{})},
		{"CommitRefsConfig", CommitRefsConfig{}, commitRefsFields(&CommitRefsConfig{})},
		{"CommitConfig", CommitConfig{}, commitFields(&CommitConfig{})},
		{"AliasTagConfig", AliasTagConfig{}, aliasTagFields(&AliasTagConfig{})},
		{"WebhookConfig", WebhookConfig{}, webhookFields(&WebhookConfig{})},
		{"WebhookHeader", WebhookHeader{}, webhookHeaderFields(&WebhookHeader{})},
		{"AutoVersionConfig", AutoVersionConfig{}, autoVersionFields(&AutoVersionConfig{})},
		{"AutoVersionReplaceConfig", AutoVersionReplaceConfig{},
			autoVersionReplaceFields(&AutoVersionReplaceConfig{})},
		{"ParserConfig", ParserConfig{}, parserFields(&ParserConfig{})},
		{"ParserPropagationConfig", ParserPropagationConfig{},
			parserPropagationFields(&ParserPropagationConfig{})},
		{"ParserLimitsConfig", ParserLimitsConfig{}, parserLimitsFields(&ParserLimitsConfig{})},
	} {
		t.Run(c.name, func(t *testing.T) {
			assert.ElementsMatch(t, modelKeys(reflect.TypeOf(c.model)), tableKeys(c.table),
				"%s: every field of the model is a key of its table and the other way round; "+
					"a field missing here has no key at all, and a key missing there writes nowhere",
				c.name)
		})
	}
}

// TestResolvedFieldsAreOutsideTheLanguage names the fields deliberately absent
// from every table: what the loader and validation compute is not something a
// file may write, and `json:"-"` is how the model says so. Without this the
// drift test above could be satisfied by adding them, which would turn a
// computed value into an overridable one.
func TestResolvedFieldsAreOutsideTheLanguage(t *testing.T) {
	table := fileFields(&File{})
	for _, name := range []string{
		"SourceFiles", "BuildConcurrency", "PublishConcurrency",
		"InitialVersions", "ResolvedParser",
	} {
		field, ok := reflect.TypeOf(File{}).FieldByName(name)
		if assert.True(t, ok, "File has no field %s", name) {
			assert.Equal(t, "-", field.Tag.Get("json"), "%s is not part of the config language", name)
		}
		assert.NotContains(t, table, strings.ToLower(name))
	}
}

// modelKeys are the keys a struct's json tags say it holds, lowered the way
// the tree lowers them. A `-` tag is a field outside the config language, and
// an embedded struct is squashed: its fields are written at this level.
func modelKeys(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous {
			out = append(out, modelKeys(f.Type)...)
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, strings.ToLower(name))
	}
	return out
}

// tableKeys are the keys a fields table holds.
func tableKeys(f fields) []string {
	out := make([]string, 0, len(f))
	for key := range f {
		out = append(out, key)
	}
	return out
}
