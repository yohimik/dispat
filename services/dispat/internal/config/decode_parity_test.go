package config

// A differential harness, alive only for the length of the migration.
//
// The first-party decoder replaces a reflected one, and the question that
// matters while the two exist side by side is not "does the new one work" but
// "does it decide the same thing". So this file keeps the old decoder — the
// mapstructure configuration and the four hooks, copied verbatim rather than
// called, so it stays the old decoder after the real one is deleted — and runs
// a corpus of documents through both.
//
// Every case says which of three things it expects: the two agree; the new one
// refuses what the old one silently accepted (a delta the plan enumerates and
// the user approved); or both accept and the value differs by design. There is
// no fourth answer, and a difference nobody declared fails the test.
//
// It is deleted in the commit that drops the mapstructure dependency. Nothing
// else may import it, and nothing here is a promise about the future: the
// conventions that outlive the migration are pinned in decode_test.go.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/go-viper/mapstructure/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	public "github.com/yohimik/dispat/pkg/models"
)

// outcome is what a corpus entry expects of the two decoders.
type outcome int

const (
	// agree: the same value, or the same refusal naming the same key.
	agree outcome = iota
	// refused: the old decoder accepted the document and the new one does not.
	refused
	// respelled: both accept, and the value differs by design.
	respelled
)

// parityCase is one document and what the two decoders are expected to make of
// it. key, when set, is the key both errors must name.
type parityCase struct {
	name string
	doc  map[string]any
	want outcome
	key  string
}

// TestDecoderAgreesWithMapstructureOnFiles runs the root-object corpus.
func TestDecoderAgreesWithMapstructureOnFiles(t *testing.T) {
	for _, c := range fileCorpus(t) {
		t.Run(c.name, func(t *testing.T) {
			src := settings(lowerTree(&tree{root: c.doc}, nil))
			var before, after File
			checkParity(t, c, legacyDecode(src, &before), decodeRootConfig(src, &after), before, after)
		})
	}
}

// TestDecoderAgreesWithMapstructureOnPackageFiles runs the same comparison for
// a package folder's own file, whose top-level object is a `packages` entry.
func TestDecoderAgreesWithMapstructureOnPackageFiles(t *testing.T) {
	for _, c := range packageCorpus() {
		t.Run(c.name, func(t *testing.T) {
			src := settings(lowerTree(&tree{root: c.doc}, nil))
			var before, after PackageConfig
			checkParity(t, c, legacyDecode(src, &before), decodePackageConfig(src, &after), before, after)
		})
	}
}

// TestDecoderAgreesWithMapstructureOnSpaceFiles runs it for a space folder's
// own file.
func TestDecoderAgreesWithMapstructureOnSpaceFiles(t *testing.T) {
	for _, c := range spaceCorpus() {
		t.Run(c.name, func(t *testing.T) {
			src := settings(lowerTree(&tree{root: c.doc}, nil))
			var before, after SpaceFile
			checkParity(t, c, legacyDecode(src, &before), decodeSpaceFile(src, &after), before, after)
		})
	}
}

// checkParity holds the three verdicts, so every corpus applies them the same
// way.
func checkParity[T any](t *testing.T, c parityCase, oldErr, newErr error, before, after T) {
	t.Helper()
	switch c.want {
	case agree:
		if oldErr != nil {
			require.Error(t, newErr, "the old decoder refused this, and the new one must too:\n%v", oldErr)
			if c.key != "" {
				assert.Contains(t, strings.ToLower(oldErr.Error()), c.key)
				assert.Contains(t, strings.ToLower(newErr.Error()), c.key,
					"both refusals name the same key")
			}
			return
		}
		require.NoError(t, newErr, "the old decoder accepted this")
		assert.Equal(t, before, after)
	case refused:
		require.NoError(t, oldErr, "this case exists because the old decoder accepted it")
		require.Error(t, newErr, "the new decoder refuses it by design")
		if c.key != "" {
			assert.Contains(t, strings.ToLower(newErr.Error()), c.key)
		}
	case respelled:
		require.NoError(t, oldErr)
		require.NoError(t, newErr)
		assert.NotEqual(t, before, after, "this case exists because the two spell the value differently")
	}
}

// fileCorpus is the root-object corpus: the two canonical configurations the
// package's own tests are built from, marshalled back to the shape a file
// holds, plus every shorthand, weak scalar and edge the language admits.
func fileCorpus(t *testing.T) []parityCase {
	t.Helper()
	cases := []parityCase{
		{name: "valid_config", doc: asDocument(t, validConfig())},
		{name: "minimal_config", doc: asDocument(t, minimalConfig())},
	}
	return append(cases, []parityCase{
		{name: "scalar_script", doc: map[string]any{"scripts": map[string]any{"build": "npm run build"}}},
		{name: "script_with_commas", doc: map[string]any{
			"scripts": map[string]any{"b": `docker build --output type=local,dest=out .`}}},
		{name: "script_list", doc: map[string]any{
			"scripts": map[string]any{"b": []any{"npm ci", "npm run build"}}}},
		{name: "nil_script", doc: map[string]any{"scripts": map[string]any{"b": nil}}},
		{name: "scalar_path", doc: map[string]any{
			"spaces": map[string]any{"libs": map[string]any{"path": "pkgs"}}}},
		{name: "path_with_comma", doc: map[string]any{
			"spaces": map[string]any{"libs": map[string]any{"path": "a,b"}}}},
		{name: "path_list", doc: map[string]any{
			"spaces": map[string]any{"libs": map[string]any{"path": []any{"a", "b"}}}}},
		{name: "path_number", doc: map[string]any{
			"spaces": map[string]any{"libs": map[string]any{"path": 42}}}},
		{name: "scalar_flow", doc: map[string]any{
			"spaces": map[string]any{"libs": map[string]any{"flow": map[string]any{"build": "a,b"}}}}},
		{name: "scalar_channels", doc: map[string]any{
			"changelog": map[string]any{"channels": "stable"}}},
		{name: "empty_channels", doc: map[string]any{
			"changelog": map[string]any{"channels": "", "file": "C.md"}}},
		{name: "empty_channel_list", doc: map[string]any{"changelog": map[string]any{"channels": []any{}}}},
		{name: "scalar_events", doc: map[string]any{
			"webhooks": []any{map[string]any{"url": "https://e", "events": "package.published"}}}},
		{name: "scalar_file_title", doc: map[string]any{
			"changelog": map[string]any{"fileTitle": "# Changelog, and more"}}},
		{name: "record_line_shapes", doc: map[string]any{"changelog": map[string]any{
			"header": []any{"one, one", []any{"two", "three"}, map[string]any{"line": "four", "package": "core"}}}}},
		{name: "record_line_map", doc: map[string]any{
			"changelog": map[string]any{"footer": map[string]any{"line": "x"}}}},
		{name: "record_line_number", doc: map[string]any{
			"changelog": map[string]any{"header": []any{map[string]any{"line": 42}}}}},
		{name: "record_line_nil_element", doc: map[string]any{
			"changelog": map[string]any{"header": []any{nil}}}},
		{name: "single_alias_tag", doc: map[string]any{
			"aliasTags": map[string]any{"format": "v{major}", "moving": true}}},
		{name: "single_webhook_header", doc: map[string]any{"webhooks": []any{map[string]any{
			"url": "https://e", "headers": map[string]any{"name": "A", "value": "B"}}}}},
		{name: "single_replace_rule", doc: map[string]any{"autoVersion": map[string]any{
			"replace": map[string]any{"files": "a", "find": "b", "write": "c"}}}},
		{name: "dependency_map_form", doc: map[string]any{"dependencies": map[string]any{
			"App": []any{"core", map[string]any{"provider": "utils", "keep": true}}}}},
		{name: "dependency_scalar_form", doc: map[string]any{
			"dependencies": map[string]any{"app": "core"}}},
		{name: "dependency_bad_form", doc: map[string]any{"dependencies": "core"}, key: "dependencies"},
		{name: "concurrency_scalar", doc: map[string]any{"concurrency": 3}},
		{name: "concurrency_pair", doc: map[string]any{"concurrency": []any{4, 2}}},
		{name: "concurrency_floats", doc: map[string]any{"concurrency": []any{4.0, 2.0}}},
		{name: "concurrency_int64", doc: map[string]any{"concurrency": []any{int64(4), int64(2)}}},
		{name: "concurrency_flag_strings", doc: map[string]any{"concurrency": []string{"1", "3"}}},
		{name: "concurrency_comma_string", doc: map[string]any{"concurrency": "4,2"}},
		{name: "concurrency_empty_string", doc: map[string]any{"concurrency": ""}},
		{name: "quoted_number", doc: map[string]any{"parser": map[string]any{"maxDescriptionLength": "72"}}},
		{name: "empty_string_number", doc: map[string]any{"parser": map[string]any{"maxDescriptionLength": ""}}},
		{name: "int64_number", doc: map[string]any{"parser": map[string]any{"maxDescriptionLength": int64(7)}}},
		{name: "bare_number_string", doc: map[string]any{"tagFormat": 42}},
		{name: "big_float_string", doc: map[string]any{"tagFormat": 1234567890123.0}},
		{name: "quoted_bool", doc: map[string]any{"updateCheck": "true"}},
		{name: "empty_string_bool", doc: map[string]any{"updateCheck": ""}},
		{name: "number_bool", doc: map[string]any{"isBuildWaitingPublish": 1}},
		{name: "bad_bool", doc: map[string]any{"updateCheck": "yes"}, key: "updatecheck"},
		{name: "generic_map_values", doc: map[string]any{
			"initials": map[string]any{"core": 3, "utils": 1.5, "app": nil}}},
		{name: "env_map", doc: map[string]any{"env": map[string]any{"COUNT": 3, "FLAG": true}}, want: respelled},
		{name: "custom_object", doc: map[string]any{
			"custom": map[string]any{"team": "platform", "budget": 3, "list": []any{1, "b"}}}},
		{name: "nil_values", doc: map[string]any{
			"tagFormat": nil, "concurrency": nil, "changelog": nil, "custom": nil, "updateCheck": nil}},
		{name: "empty_object_pruned", doc: map[string]any{"autoVersion": map[string]any{}}},
		{name: "dotted_key", doc: map[string]any{"changelog.file": "X.md"}},
		{name: "mixed_case_keys", doc: map[string]any{
			"Packages": map[string]any{"CoreLib": map[string]any{"TagFormat": "V{Version}"}},
			"Spaces":   map[string]any{"Libs": map[string]any{"Path": "Pkgs"}}}},
		{name: "nil_package_entry", doc: map[string]any{"packages": map[string]any{"core": nil}}},
		{name: "nil_webhook_element", doc: map[string]any{"webhooks": []any{nil}}},
		{name: "empty_webhook_list", doc: map[string]any{"webhooks": []any{}}},
		{name: "squashed_entry_format", doc: map[string]any{"changelog": map[string]any{
			"file": "N.md", "breakingTitle": "B", "authors": map[string]any{"placement": "inline"}}}},
		{name: "version_groups", doc: map[string]any{
			"versionGroups": map[string]any{"G": map[string]any{"versioning": "fixed"}}}},
		{name: "unknown_root_key", doc: map[string]any{"typokey": 1}, key: "typokey"},
		{name: "unknown_nil_key", doc: map[string]any{"typokey": nil}, key: "typokey"},
		{name: "unknown_space_key", doc: map[string]any{
			"spaces": map[string]any{"libs": map[string]any{"typokey": 1}}}, key: "typokey"},
		{name: "unknown_package_key", doc: map[string]any{
			"packages": map[string]any{"core": map[string]any{"typokey": 1}}}, key: "typokey"},
		{name: "unknown_changelog_key", doc: map[string]any{
			"changelog": map[string]any{"typokey": 1}}, key: "typokey"},
		{name: "resolved_field_key", doc: map[string]any{"initialversions": nil}, key: "initialversions"},
		{name: "list_into_string", doc: map[string]any{"tagFormat": []any{"a"}}, key: "tagformat"},
		{name: "map_into_string", doc: map[string]any{"tagFormat": map[string]any{"a": 1}}, key: "tagformat"},
		{name: "scalar_into_object", doc: map[string]any{"changelog": 4}, key: "changelog"},
		{name: "string_into_map", doc: map[string]any{"initials": "x"}, key: "initials"},
		{name: "scalar_into_scripts", doc: map[string]any{"scripts": "x"}, key: "scripts"},

		// The deltas the plan enumerates: the old decoder made something of
		// each of these, and what it made was never what the file meant.
		{name: "delta_fractional_number", want: refused, key: "maxdescriptionlength",
			doc: map[string]any{"parser": map[string]any{"maxDescriptionLength": 1.5}}},
		{name: "delta_scalar_into_object_list", want: refused, key: "webhooks",
			doc: map[string]any{"webhooks": ""}},
		{name: "delta_number_script", want: refused, key: "scripts",
			doc: map[string]any{"scripts": map[string]any{"build": 42}}},
		{name: "delta_number_in_script_list", want: refused, key: "scripts",
			doc: map[string]any{"scripts": map[string]any{"build": []any{1}}}},
		{name: "delta_bool_string", want: respelled, doc: map[string]any{"tagFormat": true}},
		{name: "delta_bool_in_generic_map", want: respelled,
			doc: map[string]any{"initials": map[string]any{"core": true}}},
	}...)
}

// packageCorpus is the corpus for a package folder's own file: the shorthands
// that reach a package entry, and its own provider list.
func packageCorpus() []parityCase {
	return []parityCase{
		{name: "scalar_dependencies", doc: map[string]any{"dependencies": "core"}},
		{name: "dependency_objects", doc: map[string]any{
			"dependencies": []any{"core", map[string]any{"provider": "utils", "kind": "devDependencies"}}}},
		{name: "bad_dependencies", doc: map[string]any{"dependencies": 42}, key: "dependencies"},
		{name: "scalar_flow", doc: map[string]any{"flow": map[string]any{"build": "a,b"}}},
		{name: "scalar_script", doc: map[string]any{"scripts": map[string]any{"b": "echo a,b"}}},
		{name: "alias_tags", doc: map[string]any{"aliasTags": map[string]any{"format": "v{version}"}}},
		{name: "records", doc: map[string]any{"changelog": map[string]any{"header": "one line"}}},
		{name: "manifest_names", doc: map[string]any{"manifestNames": "a,b"}},
		{name: "unknown_key", doc: map[string]any{"typokey": 1}, key: "typokey"},
		{name: "path_refused_elsewhere", doc: map[string]any{"path": "somewhere"}},
	}
}

// spaceCorpus is the corpus for a space folder's own file.
func spaceCorpus() []parityCase {
	return []parityCase{
		{name: "consumer_keyed_dependencies", doc: map[string]any{
			"dependencies": map[string]any{"app": "core"}}},
		{name: "packages_entries", doc: map[string]any{
			"packages": map[string]any{"Core": map[string]any{"src": "src"}}}},
		{name: "scalar_flow", doc: map[string]any{"flow": map[string]any{"publish": "a"}}},
		{name: "env_layer", doc: map[string]any{"env": map[string]any{"A": "b"}}},
		{name: "unknown_key", doc: map[string]any{"typokey": 1}, key: "typokey"},
	}
}

// asDocument renders a typed configuration as the raw document a file holds,
// which is what the loader's own tests write and therefore the corpus entry
// closest to a real repository.
func asDocument(t *testing.T, cfg File) map[string]any {
	t.Helper()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	return doc
}

// legacyDecode is the decoder this migration replaces: mapstructure with weak
// typing, exact keys, and the four shorthand hooks composed in front of the
// duration and string-to-slice ones. It is a copy rather than a call, so that
// the comparison outlives the original.
func legacyDecode(src map[string]any, dst any) error {
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName:          "mapstructure",
		MatchName:        strings.EqualFold,
		WeaklyTypedInput: true,
		ErrorUnused:      true,
		Result:           dst,
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			legacyDependencyHook, legacyRecordLinesHook, legacyScriptHook, legacyPathHook,
			mapstructure.StringToTimeDurationHookFunc(),
			legacySliceHook(","),
		),
	})
	if err != nil {
		return err
	}
	return dec.Decode(src)
}

var (
	legacyDependenciesType = reflect.TypeOf(public.Dependencies(nil))
	legacyProviderListType = reflect.TypeOf(public.ProviderList(nil))
	legacyEntryLinesType   = reflect.TypeOf([]public.EntryLine(nil))
	legacyScriptType       = reflect.TypeOf(public.Script(nil))
	legacyPathListType     = reflect.TypeOf(public.PathList(nil))
)

func legacyDependencyHook(_, to reflect.Type, data any) (any, error) {
	switch to {
	case legacyDependenciesType:
		return public.NormalizeDependencies(data)
	case legacyProviderListType:
		return public.NormalizeProviders(data, "dependencies")
	default:
		return data, nil
	}
}

func legacyScriptHook(_, to reflect.Type, data any) (any, error) {
	if to != legacyScriptType {
		return data, nil
	}
	if s, ok := data.(string); ok {
		return []string{s}, nil
	}
	return data, nil
}

func legacyPathHook(_, to reflect.Type, data any) (any, error) {
	if to != legacyPathListType {
		return data, nil
	}
	if s, ok := data.(string); ok {
		return []string{s}, nil
	}
	return data, nil
}

func legacyRecordLinesHook(_, to reflect.Type, data any) (any, error) {
	if to != legacyEntryLinesType {
		return data, nil
	}
	if s, ok := data.(string); ok {
		return []any{map[string]any{"line": []string{s}}}, nil
	}
	items, ok := data.([]any)
	if !ok {
		return data, nil
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		if _, isMap := stringKeyMap(item); isMap {
			out = append(out, item)
			continue
		}
		if lines, ok := stringList(item); ok {
			out = append(out, map[string]any{"line": lines})
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func legacySliceHook(sep string) mapstructure.DecodeHookFunc {
	return func(from, to reflect.Type, data any) (any, error) {
		if from.Kind() != reflect.String || to.Kind() != reflect.Slice {
			return data, nil
		}
		raw := data.(string)
		if raw == "" {
			return []string{}, nil
		}
		return strings.Split(raw, sep), nil
	}
}
