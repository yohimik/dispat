// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelevantTagSnapshotRetainsExactReleaseAndAliasRefs(t *testing.T) {
	const raw = "core@1.2.3\t1111111111111111111111111111111111111111\t\n" +
		"v1\t2222222222222222222222222222222222222222\taaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
		"services/api/v2.0.0\t3333333333333333333333333333333333333333\t\n" +
		"other@9.0.0\t4444444444444444444444444444444444444444\t\n" +
		LockTagName + "\t5555555555555555555555555555555555555555\t\n" +
		LockAttemptTagPrefix + "run\t6666666666666666666666666666666666666666\t\n"

	got := parseRelevantTagSnapshot(raw, NewTagSnapshotMatcher([]TagNamespace{
		{Package: "core", Release: DefaultTagFormat, Aliases: []AliasFormat{"v{major}"}},
		{Package: "api", Release: "services/{name}/v{version}"},
	}))

	require.Len(t, got, 3)
	assert.Equal(t, TagRefTarget{Object: strings.Repeat("1", 40)}, got["core@1.2.3"])
	assert.Equal(t, TagRefTarget{Object: strings.Repeat("2", 40), Peeled: strings.Repeat("a", 40)}, got["v1"])
	assert.Equal(t, strings.Repeat("a", 40), got["v1"].Commit())
	assert.Equal(t, strings.Repeat("3", 40), got["services/api/v2.0.0"].Commit())
	assert.NotContains(t, got, "other@9.0.0")
	assert.NotContains(t, got, LockTagName)
}

func TestRelevantTagSnapshotDetachesRetainedFields(t *testing.T) {
	const object = "1111111111111111111111111111111111111111"
	raw := strings.Repeat("unrelated-padding", 1<<16) + "\ncore@1.0.0\t" + object + "\t\n"

	got := parseRelevantTagSnapshot(raw, NewTagSnapshotMatcher([]TagNamespace{{Package: "core", Release: DefaultTagFormat}}))
	ref := got["core@1.0.0"]
	var retainedName string
	for name := range got {
		retainedName = name
	}
	for _, value := range []string{retainedName, ref.Object} {
		ptr := uintptr(unsafe.Pointer(unsafe.StringData(value)))
		start := uintptr(unsafe.Pointer(unsafe.StringData(raw)))
		assert.False(t, ptr >= start && ptr < start+uintptr(len(raw)), "%q aliases the raw inventory", value)
	}
}

func TestRelevantTagSnapshotSupportsBroadOverlappingFormats(t *testing.T) {
	const raw = "1.2.3\taaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\t\n" +
		"v2\tbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\t\n"
	got := parseRelevantTagSnapshot(raw, NewTagSnapshotMatcher([]TagNamespace{
		{Package: "one", Release: "{version}"},
		{Package: "two", Release: "{version}", Aliases: []AliasFormat{"v{major}"}},
	}))
	assert.Len(t, got, 2)
}
