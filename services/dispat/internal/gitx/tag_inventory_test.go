// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTagsForPackagesMatchesIndependentPerPackageSemantics(t *testing.T) {
	const raw = "core@2.0.0\t1111111111111111111111111111111111111111\t\n" +
		"services/api/v1.4.0\t2222222222222222222222222222222222222222\taaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
		"1.3.0-release-tail\t3333333333333333333333333333333333333333\t\n" +
		"core-utils@9.9.9\t4444444444444444444444444444444444444444\t\n" +
		"core@not-semver\t5555555555555555555555555555555555555555\t\n" +
		"  core@2.2.0  \t  8888888888888888888888888888888888888888  \t   \n" +
		LockTagName + "\t6666666666666666666666666666666666666666\t\n" +
		LockAttemptTagPrefix + "123\t7777777777777777777777777777777777777777\t\n"
	formats := map[string]TagFormat{
		"core":       DefaultTagFormat,
		"core-utils": DefaultTagFormat,
		"api":        "services/{name}/v{version}",
		"tail":       "{version}-release-{name}",
		"wide":       "{version}",
		"invalid":    "v{major}",
	}

	got, err := parseTagsForPackages(raw, formats)
	require.NoError(t, err)
	for pkg, format := range formats {
		assert.Equal(t, referenceTagsForPackage(raw, pkg, format), got[pkg], pkg)
	}
	require.Len(t, got["api"], 1)
	require.Len(t, got["core"], 3)
	assert.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", got["api"][0].Commit,
		"annotated tags use the peeled commit")
	assert.False(t, got["core"][1].Parsed, "matching unparseable releases remain visible")
	assert.Equal(t, "core@2.2.0", got["core"][2].Name, "field padding is trimmed without changing tab structure")
	assert.Len(t, got["wide"], 6, "a broad custom format receives every non-reserved well-formed ref")
}

func TestParseTagInventoryRefusesMalformedNonemptyRecords(t *testing.T) {
	for _, raw := range []string{
		"missing-fields\n",
		"core@1.0.0\t\t\n",
		"core@1.0.0\tnot-an-object-id\t\n",
		"core@1.0.0\t1111111111111111111111111111111111111111\tnot-an-object-id\n",
	} {
		_, err := parseTagsForPackages(raw, map[string]TagFormat{"core": DefaultTagFormat})
		assert.ErrorContains(t, err, "malformed")
	}
}

func TestMatchedTagsDetachFromRawInventory(t *testing.T) {
	const commit = "1111111111111111111111111111111111111111"
	raw := LockAttemptTagPrefix + strings.Repeat("padding", 1<<13) + "\t2222222222222222222222222222222222222222\t\n" +
		"core@1.2.3\t" + commit + "\t\n"

	bulk, err := parseTagsForPackages(raw, map[string]TagFormat{
		"core": DefaultTagFormat,
		"wide": "{version}",
	})
	require.NoError(t, err)
	require.Len(t, bulk["core"], 1)
	require.Len(t, bulk["wide"], 1)
	assertDetachedFromInventory(t, bulk["core"][0].Name, raw)
	assertDetachedFromInventory(t, bulk["core"][0].Commit, raw)
	assert.Same(t, unsafe.StringData(bulk["core"][0].Name), unsafe.StringData(bulk["wide"][0].Name),
		"overlapping package matches share the one detached tag name")
	assert.Same(t, unsafe.StringData(bulk["core"][0].Commit), unsafe.StringData(bulk["wide"][0].Commit),
		"overlapping package matches share the one detached commit")

	single, err := parseTags(raw, "core", DefaultTagFormat)
	require.NoError(t, err)
	require.Len(t, single, 1)
	assertDetachedFromInventory(t, single[0].Name, raw)
	assertDetachedFromInventory(t, single[0].Commit, raw)
}

func assertDetachedFromInventory(t *testing.T, value, inventory string) {
	t.Helper()
	valuePointer := uintptr(unsafe.Pointer(unsafe.StringData(value)))
	inventoryStart := uintptr(unsafe.Pointer(unsafe.StringData(inventory)))
	inventoryEnd := inventoryStart + uintptr(len(inventory))
	assert.False(t, valuePointer >= inventoryStart && valuePointer < inventoryEnd,
		"%q aliases the raw inventory allocation", value)
}

// referenceTagsForPackage is the deliberately simple O(tags) interpretation
// used before bulk indexing. Keeping it independent from the shared matcher
// makes the parity test protect ordering, peeling and malformed-ref behavior.
func referenceTagsForPackage(out, pkg string, format TagFormat) Tags {
	format = format.WithDefault()
	var tags Tags
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if name == LockTagName || strings.HasPrefix(name, LockAttemptTagPrefix) || !format.IsMatch(pkg, name) {
			continue
		}
		tag := Tag{Name: name, Commit: strings.TrimSpace(fields[1])}
		if len(fields) > 2 {
			if peeled := strings.TrimSpace(fields[2]); peeled != "" {
				tag.Commit = peeled
			}
		}
		if version, ok := format.ParseVersion(pkg, name); ok {
			tag.Version, tag.Parsed = version, true
		}
		tags = append(tags, tag)
	}
	return tags
}

var benchmarkTagResults map[string]Tags

func BenchmarkParseTagsForPackagesIndexed(b *testing.B) {
	standardRaw, standardFormats := benchmarkTagInventory(512, 16, false)
	overlapRaw, overlapFormats := benchmarkTagInventory(64, 16, true)

	for _, tc := range []struct {
		name    string
		raw     string
		formats map[string]TagFormat
	}{
		{name: "standard_prefixes", raw: standardRaw, formats: standardFormats},
		{name: "overlapping_custom_formats", raw: overlapRaw, formats: overlapFormats},
	} {
		b.Run(tc.name+"/indexed", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.raw)))
			for i := 0; i < b.N; i++ {
				benchmarkTagResults, _ = parseTagsForPackages(tc.raw, tc.formats)
			}
		})
		b.Run(tc.name+"/per_package_scan", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.raw)))
			for i := 0; i < b.N; i++ {
				result := make(map[string]Tags, len(tc.formats))
				for pkg, format := range tc.formats {
					result[pkg], _ = parseTags(tc.raw, pkg, format)
				}
				benchmarkTagResults = result
			}
		})
	}
}

func benchmarkTagInventory(packages, versions int, overlap bool) (string, map[string]TagFormat) {
	formats := make(map[string]TagFormat, packages)
	var raw strings.Builder
	for p := 0; p < packages; p++ {
		pkg := fmt.Sprintf("pkg-%03d", p)
		if overlap {
			formats[pkg] = TagFormat("{version}-release-" + pkg)
		} else {
			formats[pkg] = DefaultTagFormat
		}
		for version := versions - 1; version >= 0; version-- {
			name := fmt.Sprintf("%s@1.%d.0", pkg, version)
			if overlap {
				name = fmt.Sprintf("1.%d.0-release-%s", version, pkg)
			}
			fmt.Fprintf(&raw, "%s\t%040x\t\n", name, p*versions+version+1)
		}
	}
	return raw.String(), formats
}
