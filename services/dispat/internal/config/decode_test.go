package config

// The conventions decoding keeps, pinned at the decoder rather than through a
// loaded repository.
//
// Everything else in this package tests the loader, which is the right level
// for what a configuration means. These tests are at the level below, because
// the rules here are not about meaning at all: they are the shape of the
// config language — which scalar stands in for which container, which key is
// refused, which absence is a nil and which is an empty list — and a rule of
// that kind is only visible in the one call that applies it.
//
// Each test names a convention rather than an implementation, so the decoder
// underneath can be replaced without any of them being rewritten. Error
// wording is deliberately never asserted whole: a test asserts that the
// message names the key the file got wrong, which is the promise the
// documentation makes (reference/plan-errors.md), and nothing more.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeRoot runs one raw document through exactly the path Load takes: the
// settings shape, then the decoder. Tests write
// the document the way a file writes it — mixed case included — so the case
// folding every convention below sits on top of is part of what they exercise.
func decodeRoot(t *testing.T, doc map[string]any) (File, error) {
	t.Helper()
	var cfg File
	return cfg, decodeRootConfig(settings(doc), &cfg)
}

// decodePackage is decodeRoot for a package folder's own file.
func decodePackage(t *testing.T, doc map[string]any) (PackageConfig, error) {
	t.Helper()
	var pc PackageConfig
	return pc, decodePackageConfig(settings(doc), &pc)
}

// decodeSpace is decodeRoot for a space folder's own file.
func decodeSpace(t *testing.T, doc map[string]any) (SpaceFile, error) {
	t.Helper()
	var sf SpaceFile
	return sf, decodeSpaceFile(settings(doc), &sf)
}

// TestDecodeUnknownKeyWithNoValueStillErrors: a key written with nothing after
// it is still a key the author wrote. The flattening keeps it (refs_test.go
// pins that), and the typo it usually is has to be refused even though the
// value it carries is nothing at all.
func TestDecodeUnknownKeyWithNoValueStillErrors(t *testing.T) {
	_, err := decodeRoot(t, map[string]any{"tagFormat": "v{version}", "tagfromat": nil})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tagfromat", "the message names the key the file got wrong")
}

// TestDecodeNilValueIsAKeyThatSaidNothing: the same shape on a key the model
// does have. It is used — no unknown-key error — and it leaves the field at
// its zero value, which is what lets validation apply the default.
func TestDecodeNilValueIsAKeyThatSaidNothing(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{"tagFormat": nil, "concurrency": nil})
	require.NoError(t, err)
	assert.Equal(t, "", cfg.TagFormat)
	assert.Nil(t, cfg.Concurrency)
}

// TestDecodeResolvedFieldsAreNotConfigKeys: the model carries fields the
// loader and validation fill in — the source file list, the resolved
// concurrencies, the parsed initial versions, the built parser. They are not
// part of the config language, so naming one in a file is a typo like any
// other rather than a way to write over a computed value.
func TestDecodeResolvedFieldsAreNotConfigKeys(t *testing.T) {
	for _, key := range []string{
		"sourceFiles", "buildConcurrency", "publishConcurrency",
		"initialVersions", "resolvedParser",
	} {
		t.Run(key, func(t *testing.T) {
			_, err := decodeRoot(t, map[string]any{key: nil})
			require.Error(t, err, "%s is not a key a file may write", key)
			assert.Contains(t, strings.ToLower(err.Error()), strings.ToLower(key))
		})
	}
}

// TestDecodeSquashedEntryFormatIsOneObject: `changelog` is its own fields plus
// the shared entry-format ones, and the file writes them side by side with no
// sign of the boundary. Both halves fill from one object, and a typo lands in
// neither, which is the case a squashed embed is easiest to get wrong in: the
// key belongs to no half, and an implementation that asks each half separately
// can decide the other one used it.
func TestDecodeSquashedEntryFormatIsOneObject(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{"changelog": map[string]any{
		"file":          "NOTES.md",
		"breakingTitle": "Breaking",
	}})
	require.NoError(t, err)
	require.NotNil(t, cfg.Changelog)
	assert.Equal(t, "NOTES.md", cfg.Changelog.File, "the object's own field")
	assert.Equal(t, "Breaking", cfg.Changelog.BreakingTitle, "and the shared one, from one object")

	_, err = decodeRoot(t, map[string]any{"changelog": map[string]any{"breakingtitel": "Breaking"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "breakingtitel", "a key belonging to neither half is unknown")
}

// TestDecodeRecordLineScalarIsOneLine: a record line holds prose, and prose
// contains commas. The scalar shorthand `"header": "a, b"` is one line of text
// and must not become two — the shorthand exists to save writing an object,
// not to introduce a separator the author never agreed to.
func TestDecodeRecordLineScalarIsOneLine(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{"changelog": map[string]any{
		"header":    "released by dispat, on the runner",
		"fileTitle": "# Changelog, of everything",
	}})
	require.NoError(t, err)
	require.NotNil(t, cfg.Changelog)
	require.Len(t, cfg.Changelog.Header, 1, "one string is one record line, commas and all")
	assert.Equal(t, []string{"released by dispat, on the runner"}, cfg.Changelog.Header[0].Line)
	require.Len(t, cfg.Changelog.FileTitle, 1)
	assert.Equal(t, []string{"# Changelog, of everything"}, cfg.Changelog.FileTitle[0].Line)
}

// TestDecodeRecordLineShorthandsInsideAList: the same shorthand one level
// down. An element is a full object, a bare string, or a bare array of
// strings, and the three mix freely inside one list.
func TestDecodeRecordLineShorthandsInsideAList(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{"changelog": map[string]any{
		"footer": []any{
			"one, still one",
			[]any{"two", "three"},
			map[string]any{"line": "four", "package": "core"},
		},
	}})
	require.NoError(t, err)
	require.NotNil(t, cfg.Changelog)
	require.Len(t, cfg.Changelog.Footer, 3)
	assert.Equal(t, []string{"one, still one"}, cfg.Changelog.Footer[0].Line)
	assert.Equal(t, []string{"two", "three"}, cfg.Changelog.Footer[1].Line)
	assert.Equal(t, []string{"four"}, cfg.Changelog.Footer[2].Line)
	assert.Equal(t, []string{"core"}, cfg.Changelog.Footer[2].Package)
}

// TestDecodeGenericListSplitsAScalarOnCommas: the other side of the same rule.
// A plain list of names — script references, ignore patterns, channels — does
// take the comma shorthand, which is what makes `--concurrency 4,2` and
// `"build": "lint,build"` mean what they read as. The two rules are pinned
// together because the bug is always one leaking into the other.
func TestDecodeGenericListSplitsAScalarOnCommas(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{
		"spaces": map[string]any{"libs": map[string]any{
			"path": "pkgs",
			"flow": map[string]any{"build": "lint,build"},
		}},
		"ignore": "docs,fixtures",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"lint", "build"}, cfg.Spaces["libs"].Flow.Build)
	assert.Equal(t, []string{"docs", "fixtures"}, cfg.Ignore)
}

// TestDecodeScriptAndPathScalarsNeverSplit: a script is shell text and a path
// is a folder name, and both may hold a comma the file means literally. They
// take the same scalar shorthand as a list of names and must not take its
// splitting with it.
func TestDecodeScriptAndPathScalarsNeverSplit(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{
		"scripts": map[string]any{"build": `docker build --output type=local,dest=out .`},
		"spaces":  map[string]any{"libs": map[string]any{"path": "pkgs,with,commas"}},
	})
	require.NoError(t, err)
	assert.Equal(t, Script{`docker build --output type=local,dest=out .`}, cfg.Scripts["build"])
	assert.Equal(t, PathList{"pkgs,with,commas"}, cfg.Spaces["libs"].Path)
}

// TestDecodeEmptyListsKeepTheirEmptiness: the three ways a list can hold
// nothing are three different statements, and downstream reads them as such —
// an empty channels list records every release, an absent one inherits. A
// decoder that folded the empty forms into nil would silently move a
// configuration from one to the other.
func TestDecodeEmptyListsKeepTheirEmptiness(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{"changelog": map[string]any{
		"channels": "",
		"file":     "CHANGELOG.md",
	}})
	require.NoError(t, err)
	require.NotNil(t, cfg.Changelog)
	assert.NotNil(t, cfg.Changelog.Channels, "an empty string is an empty list, not an absence")
	assert.Empty(t, cfg.Changelog.Channels)

	cfg, err = decodeRoot(t, map[string]any{"changelog": map[string]any{"channels": []any{}}})
	require.NoError(t, err)
	require.NotNil(t, cfg.Changelog)
	assert.NotNil(t, cfg.Changelog.Channels, "and so is an empty array")
	assert.Empty(t, cfg.Changelog.Channels)

	cfg, err = decodeRoot(t, map[string]any{"changelog": map[string]any{"file": "CHANGELOG.md"}})
	require.NoError(t, err)
	require.NotNil(t, cfg.Changelog)
	assert.Nil(t, cfg.Changelog.Channels, "a key nobody wrote stays nil")
}

// TestDecodeWeakScalarsFillTypedFields: the config language has no types of
// its own — a bare number is a fine string, a quoted one a fine number, and
// both spellings of a boolean are a boolean. It is what lets one value be
// written the way its format writes it, and what makes a config authored by a
// template loadable.
func TestDecodeWeakScalarsFillTypedFields(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{
		"parser":                map[string]any{"maxDescriptionLength": "72"},
		"tagFormat":             42,
		"updateCheck":           "true",
		"isBuildWaitingPublish": 1,
		"initials":              map[string]any{"core": 3, "utils": 1.5},
	})
	require.NoError(t, err)
	require.NotNil(t, cfg.Parser)
	assert.Equal(t, 72, cfg.Parser.MaxDescriptionLength, "a quoted number is a number")
	assert.Equal(t, "42", cfg.TagFormat, "and a bare number is a string")
	require.NotNil(t, cfg.UpdateCheck)
	assert.True(t, *cfg.UpdateCheck, `"true" is true`)
	require.NotNil(t, cfg.IsBuildWaitingPublish)
	assert.True(t, *cfg.IsBuildWaitingPublish, "and so is 1")
	assert.Equal(t, map[string]string{"core": "3", "utils": "1.5"}, cfg.Initials,
		"a generic map's values render the way the env pass renders them")
}

// TestDecodeSingleObjectLiftsIntoAList: the one-element shorthand reaches
// lists of objects too, so a repository with exactly one alias tag or one
// webhook writes the object rather than an array around it.
func TestDecodeSingleObjectLiftsIntoAList(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{
		"aliasTags": map[string]any{"format": "v{major}", "moving": true},
	})
	require.NoError(t, err)
	require.Len(t, cfg.AliasTags, 1)
	assert.Equal(t, "v{major}", cfg.AliasTags[0].Format)
	assert.True(t, cfg.AliasTags[0].Moving)
}

// TestDecodeErrorNamesTheKeyAtEveryDepth: what the documentation promises
// about an unknown key is that the message names it, wherever in the file it
// sits (reference/plan-errors.md). The wording is not pinned; the name is.
func TestDecodeErrorNamesTheKeyAtEveryDepth(t *testing.T) {
	cases := map[string]map[string]any{
		"root": {"typokey": 1},
		"space": {"spaces": map[string]any{"libs": map[string]any{
			"path": "pkgs", "typokey": 1,
		}}},
		"package": {"packages": map[string]any{"core": map[string]any{"typokey": 1}}},
		"nested":  {"parser": map[string]any{"limits": map[string]any{"typokey": 1}}},
		"list_element": {"webhooks": []any{
			map[string]any{"url": "https://example.com"},
			map[string]any{"url": "https://example.com", "typokey": 1},
		}},
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decodeRoot(t, doc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "typokey")
		})
	}
}

// TestDecodeFoldsFieldKeysAndKeepsEntryKeys: the two halves of the rule, in one
// place because they are easy to confuse.
//
// A field key names something the model has — `Packages`, `Path` — and the
// fields table is keyed in lower case, so the key is folded to find its setter
// and the file may spell it however it likes. An entry key names something the
// repository has — a package, a space, a script — and is kept exactly as the
// file wrote it, matched case-insensitively wherever it is looked up. Values
// are never touched either way.
func TestDecodeFoldsFieldKeysAndKeepsEntryKeys(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{
		"Packages": map[string]any{"CoreLib": map[string]any{"tagFormat": "V{Version}"}},
		"Spaces":   map[string]any{"Libs": map[string]any{"Path": "Pkgs"}},
		"Scripts":  map[string]any{"Build": "echo Build"},
	})
	require.NoError(t, err)
	require.Contains(t, cfg.Packages, "CoreLib", "an entry key is the name the author wrote")
	assert.NotContains(t, cfg.Packages, "corelib")
	assert.Equal(t, "V{Version}", cfg.Packages["CoreLib"].TagFormat, "and its value is untouched")
	require.Contains(t, cfg.Spaces, "Libs")
	assert.Equal(t, PathList{"Pkgs"}, cfg.Spaces["Libs"].Path,
		"`Path` found its setter folded, and `Pkgs` is the folder the file named")
	require.Contains(t, cfg.Scripts, "Build")
	assert.Equal(t, Script{"echo Build"}, cfg.Scripts["Build"])

	// And the lookups that make the kept spelling usable.
	pc, ok := cfg.Package("corelib")
	require.True(t, ok)
	assert.Equal(t, "V{Version}", pc.TagFormat)
	_, ok = cfg.Script("build")
	assert.True(t, ok)
}

// TestDecodeRefusesTwoSpellingsOfOneKey: a name written twice in one object has
// no lookup that could choose between them, so the load says so instead of
// letting the map iteration decide. Free-form `custom` is exempt: dispat looks
// nothing up in there.
func TestDecodeRefusesTwoSpellingsOfOneKey(t *testing.T) {
	for name, doc := range map[string]map[string]any{
		"field keys": {"logLevel": "info", "loglevel": "warn"},
		"scripts": {"scripts": map[string]any{
			"Build": "echo one", "build": "echo two"}},
		"packages": {"packages": map[string]any{
			"Core": map[string]any{"src": "a"}, "core": map[string]any{"src": "b"}}},
		"spaces": {"spaces": map[string]any{
			"Libs": map[string]any{"path": "a"}, "libs": map[string]any{"path": "b"}}},
		"initials": {"initials": map[string]any{"Core": "1.0.0", "core": "2.0.0"}},
		"parser types": {"parser": map[string]any{"types": map[string]any{
			"Feat": "minor", "feat": "patch"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeRoot(t, doc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "collide case-insensitively")
		})
	}

	cfg, err := decodeRoot(t, map[string]any{
		"custom": map[string]any{"Team": "a", "team": "b"},
	})
	require.NoError(t, err, "custom is the repository's own data, not a namespace dispat resolves in")
	assert.Len(t, cfg.Custom, 2)
}

// TestDecodeReportsTheSameKeyEveryTime: a map has no order, so a document with
// two mistakes in it could report either one. It reports the same one on every
// load, because a build that fails differently from one run to the next is a
// build nobody can fix.
func TestDecodeReportsTheSameKeyEveryTime(t *testing.T) {
	doc := map[string]any{"aaatypo": 1, "zzztypo": 2}
	_, first := decodeRoot(t, doc)
	require.Error(t, first)
	assert.Contains(t, first.Error(), "aaatypo", "the first key in order is the one reported")
	for i := 0; i < 32; i++ {
		_, err := decodeRoot(t, doc)
		require.Error(t, err)
		assert.Equal(t, first.Error(), err.Error(), "and the message never varies")
	}
}

// TestDecodeFolderFilesShareTheRootStance: a package folder's file and a space
// folder's file are the same config language as the root, decoded into
// different top-level objects. A shorthand the root accepts is not a syntax
// error one folder down, and an unknown key is refused there too.
func TestDecodeFolderFilesShareTheRootStance(t *testing.T) {
	pc, err := decodePackage(t, map[string]any{
		"scripts":      map[string]any{"build": "echo a,b"},
		"flow":         map[string]any{"build": "build"},
		"dependencies": "core",
		"aliasTags":    map[string]any{"format": "v{version}"},
	})
	require.NoError(t, err)
	assert.Equal(t, Script{"echo a,b"}, pc.Scripts["build"])
	assert.Equal(t, []string{"build"}, pc.Flow.Build)
	assert.Equal(t, Providers("core"), pc.Dependencies,
		"a package's own list names providers, the consumer being the package")
	require.Len(t, pc.AliasTags, 1)
	assert.Contains(t, pc.Scripts, "build")

	_, err = decodePackage(t, map[string]any{"tagfromat": "v"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tagfromat")

	sf, err := decodeSpace(t, map[string]any{
		"scripts":      map[string]any{"build": "echo b"},
		"dependencies": map[string]any{"app": "core"},
		"packages":     map[string]any{"Core": map[string]any{"src": "src"}},
	})
	require.NoError(t, err)
	assert.Equal(t, Dependencies{{Consumer: "app", Provider: "core"}}, sf.Dependencies,
		"a space's object is keyed by consumer, like the root's")
	assert.Contains(t, sf.Packages, "Core", "an entry key keeps its spelling one folder down too")

	_, err = decodeSpace(t, map[string]any{"tagfromat": "v"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tagfromat")
}

// TestDecodeAllocatesAnObjectOnlyWhenTheFileWroteOne: the optional sub-objects
// are pointers because nil and an empty object mean different things — nil is
// "this layer says nothing", so a nearer layer's value survives and the
// defaults apply. An object written with no keys at all is pruned before the
// decoder sees it (refs_test.go pins the pruning), so it says nothing either.
func TestDecodeAllocatesAnObjectOnlyWhenTheFileWroteOne(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{"tagFormat": "v{version}"})
	require.NoError(t, err)
	assert.Nil(t, cfg.Changelog, "an absent object stays absent")
	assert.Nil(t, cfg.AutoVersion)

	cfg, err = decodeRoot(t, map[string]any{"autoVersion": map[string]any{}})
	require.NoError(t, err)
	assert.Nil(t, cfg.AutoVersion, "and so does an empty one, which the flattening prunes")

	cfg, err = decodeRoot(t, map[string]any{"autoVersion": map[string]any{"enabled": true}})
	require.NoError(t, err)
	require.NotNil(t, cfg.AutoVersion, "one key is enough to make the object present")
	assert.True(t, cfg.AutoVersion.IsEnabled())
}

// TestDecodeRefusesWhatItCannotHonestlyRead pins the refusals the first-party
// decoder introduced, which is the whole of what it changed about the language.
//
// Each of these used to produce a value: a truncated number, an empty list, a
// command that was never a command. None of those values was what the file
// said, and every one of them fails somewhere far from the line that caused
// it — a concurrency of 2, a level that silently opted out of its webhooks, a
// shell invocation of the string "42". A refusal at load time names the key
// instead.
func TestDecodeRefusesWhatItCannotHonestlyRead(t *testing.T) {
	for _, c := range []struct {
		name string
		doc  map[string]any
		key  string
	}{
		{
			name: "a fraction is not a whole number",
			doc:  map[string]any{"parser": map[string]any{"maxDescriptionLength": 1.5}},
			key:  "maxdescriptionlength",
		},
		{
			name: "a scalar is not a list of objects",
			doc:  map[string]any{"webhooks": ""},
			key:  "webhooks",
		},
		{
			name: "a number is not a shell command",
			doc:  map[string]any{"scripts": map[string]any{"build": 42}},
			key:  "build",
		},
		{
			name: "nor is one inside a list of commands",
			doc:  map[string]any{"scripts": map[string]any{"build": []any{"npm ci", 42}}},
			key:  "build",
		},
		{
			name: "an object is not a name",
			doc:  map[string]any{"tagFormat": map[string]any{"a": 1}},
			key:  "tagformat",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := decodeRoot(t, c.doc)
			require.Error(t, err)
			assert.Contains(t, strings.ToLower(err.Error()), c.key, "the refusal names the key")
		})
	}
}

// TestDecodeSpellsABooleanAsItIsWritten: a boolean written where text belongs
// renders as the word, not as the digit a C-shaped conversion would produce.
// It is the same rendering the env pass gives the same value, which is what
// keeps one configuration from meaning two things depending on which pass read
// it.
func TestDecodeSpellsABooleanAsItIsWritten(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{
		"tagFormat": true,
		"initials":  map[string]any{"core": false},
	})
	require.NoError(t, err)
	assert.Equal(t, "true", cfg.TagFormat)
	assert.Equal(t, map[string]string{"core": "false"}, cfg.Initials)
}

// TestDecodeRefusesAValueOfTheWrongShape sweeps the refusals from the other
// side: for every kind of key the language has — an object, a free-form
// object, a map of names, a list of names, a list of numbers, a path, a
// boolean — a value that is not one of those is named rather than coerced into
// something.
//
// It is one table because the failure it guards against is a single missing
// branch: a setter that falls through its shape check writes a zero value and
// says nothing, which is the silent config the unknown-key refusal exists to
// prevent, arriving by another door.
func TestDecodeRefusesAValueOfTheWrongShape(t *testing.T) {
	for _, c := range []struct {
		name string
		doc  map[string]any
		key  string
	}{
		{"free-form object", map[string]any{"custom": "x"}, "custom"},
		{"map of objects", map[string]any{"spaces": "x"}, "spaces"},
		{"map of names", map[string]any{"initials": map[string]any{"core": []any{1}}}, "initials.core"},
		{"element of a list of names", map[string]any{"ignore": []any{map[string]any{"a": 1}}}, "ignore[0]"},
		{"scalar where names belong", map[string]any{"ignore": map[string]any{"a": 1}}, "ignore"},
		{"element of a list of numbers", map[string]any{"concurrency": []any{map[string]any{"a": 1}}}, "concurrency[0]"},
		{"scalar where numbers belong", map[string]any{"concurrency": map[string]any{"a": 1}}, "concurrency"},
		{"text where a number belongs", map[string]any{"concurrency": "two"}, "concurrency[0]"},
		{"element of a path", map[string]any{
			"spaces": map[string]any{"libs": map[string]any{"path": []any{map[string]any{"a": 1}}}}},
			"spaces.libs.path[0]"},
		{"object where a path belongs", map[string]any{
			"spaces": map[string]any{"libs": map[string]any{"path": map[string]any{"a": 1}}}},
			"spaces.libs.path"},
		{"text where a boolean belongs", map[string]any{"unsafeDisableLock": "yes"}, "unsafeDisableLock"},
		{"object where a boolean belongs", map[string]any{"updateCheck": map[string]any{"a": 1}}, "updateCheck"},
		{"scalar where a record line belongs", map[string]any{
			"changelog": map[string]any{"header": []any{42}}}, "changelog.header[0]"},
		{"scalar where an object belongs", map[string]any{"changelog": 4}, "changelog"},
		{"text where a map of names belongs", map[string]any{"initials": "x"}, "initials"},
		{"text where the scripts object belongs", map[string]any{"scripts": "x"}, "scripts"},
		{"number where a package's providers belong", map[string]any{
			"packages": map[string]any{"core": map[string]any{"dependencies": 42}}},
			"packages.core.dependencies"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := decodeRoot(t, c.doc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.key,
				"the refusal names the key by its path, spelled as the file wrote it")
		})
	}
}

// TestDecodeReadsEveryFormatsSpellingOfANumber: each parser hands back its own
// Go type for a number — JSON a float64, YAML an int, TOML an int64 — and a
// flag hands over the text the operator typed. The four are the same number
// here, and a boolean is the 1 or 0 a shell-shaped value arrives as, so one
// configuration cannot mean different things depending on the file extension
// it was written under.
func TestDecodeReadsEveryFormatsSpellingOfANumber(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{"parser": map[string]any{
		"maxDescriptionLength": int64(72),
		"limits": map[string]any{
			"unitsPerMessage":   64,
			"scopeTermsPerUnit": 256.0,
			"messageBytes":      "1024",
		},
	}})
	require.NoError(t, err)
	require.NotNil(t, cfg.Parser)
	require.NotNil(t, cfg.Parser.Limits)
	assert.Equal(t, 72, cfg.Parser.MaxDescriptionLength)
	assert.Equal(t, 64, cfg.Parser.Limits.UnitsPerMessage)
	assert.Equal(t, 256, cfg.Parser.Limits.ScopeTermsPerUnit)
	assert.Equal(t, 1024, cfg.Parser.Limits.MessageBytes)

	cfg, err = decodeRoot(t, map[string]any{
		"parser":      map[string]any{"maxDescriptionLength": true},
		"concurrency": []any{int64(4), "2", false, nil},
	})
	require.NoError(t, err)
	require.NotNil(t, cfg.Parser)
	assert.Equal(t, 1, cfg.Parser.MaxDescriptionLength, "a boolean counts as one")
	assert.Equal(t, []int{4, 2, 0, 0}, cfg.Concurrency,
		"and inside a list, false and nothing at all both count as none")
}

// TestDecodeReadsEveryFormatsSpellingOfABoolean is the same rule for the
// booleans: both words, both digits, and whichever numeric type the format
// produced.
func TestDecodeReadsEveryFormatsSpellingOfABoolean(t *testing.T) {
	for _, c := range []struct {
		name string
		val  any
		want bool
	}{
		{"true", true, true},
		{"the word", "true", true},
		{"the digit", 1, true},
		{"a whole number", int64(0), false},
		{"a fractional number", 2.5, true},
		{"nothing at all", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := decodeRoot(t, map[string]any{"updateCheck": c.val})
			require.NoError(t, err)
			require.NotNil(t, cfg.UpdateCheck, "the file wrote the key, so the option is stated")
			assert.Equal(t, c.want, *cfg.UpdateCheck)
		})
	}
}

// TestDecodeLiftsALoneScalarIntoItsContainer: the one-element shorthand, on
// the keys whose container is not a list of objects. A channel written on its
// own is the one channel, a folder written on its own is the space's one
// folder, and a number written where either belongs is the text of that
// number, because the config language has no types of its own to object with.
func TestDecodeLiftsALoneScalarIntoItsContainer(t *testing.T) {
	cfg, err := decodeRoot(t, map[string]any{
		"changelog": map[string]any{"channels": 42, "header": []any{nil}},
		"spaces":    map[string]any{"libs": map[string]any{"path": 42}},
		"parser":    map[string]any{"maxDescriptionLength": ""},
	})
	require.NoError(t, err)
	require.NotNil(t, cfg.Changelog)
	assert.Equal(t, []string{"42"}, cfg.Changelog.Channels)
	assert.Equal(t, []EntryLine{{}}, cfg.Changelog.Header,
		"a record line written as nothing is a line that says nothing, not a missing element")
	assert.Equal(t, PathList{"42"}, cfg.Spaces["libs"].Path)
	require.NotNil(t, cfg.Parser)
	assert.Equal(t, 0, cfg.Parser.MaxDescriptionLength, "and an empty string is no number at all")
}
