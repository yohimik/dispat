package workspaceenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLivePinCreationRejectsInvalidWorkspaceIdentities(t *testing.T) {
	root, config, _, _ := livePinFixture(t)
	for _, tc := range []struct {
		name, root, config string
		owners             map[string]string
		repositories       []string
		want               string
	}{
		{name: "missing root", root: filepath.Join(root, "missing"), config: config, want: "canonicalizing live pin root"},
		{name: "missing config", root: root, config: filepath.Join(root, "missing.json"), want: "canonicalizing live pin config"},
		{name: "empty source", root: root, config: config, repositories: []string{""}, want: "invalid source repository identity"},
		{name: "control as source", root: root, config: config, repositories: []string{"control"}, want: "invalid source repository identity"},
		{name: "duplicate source", root: root, config: config, repositories: []string{"source", "source"}, want: "duplicate source repository identity"},
		{name: "foreign package owner", root: root, config: config, owners: map[string]string{"PACKAGE_LIB": "foreign"}, repositories: []string{"source"}, want: "unknown repository"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewLivePins(tc.root, tc.config, tc.owners, tc.repositories)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, store, "invalid identities must not create inheritable coordination")
		})
	}
}

func TestLivePinReadersRequireValidInheritedOwnerMetadata(t *testing.T) {
	root, config, owners, env := livePinFixture(t)
	store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	env = append(env, store.Environment())
	for _, tc := range []struct{ name, replacement string }{
		{"malformed owner map", Owners + "={"},
		{"null owner map", Owners + "=null"},
		{"malformed repository list", Repositories + "={"},
		{"unknown owner", Owners + `={"PACKAGE_LIB":"foreign"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := append(append([]string(nil), env...), tc.replacement)
			reader, err := OpenLivePins(root, config, changed)
			require.Error(t, err)
			assert.Nil(t, reader)
		})
	}
	reader, err := OpenLivePins(root, config, env)
	require.NoError(t, err)
	_, err = reader.Pins("foreign")
	assert.ErrorContains(t, err, "not an exact source repository identity")
	reader, err = OpenLivePins(root, config, nil)
	require.NoError(t, err)
	assert.Nil(t, reader)
	require.NoError(t, RememberLivePin(root, config, nil, "source-a", strings.Repeat("a", 40)))
}

func TestFailedLivePinPublicationRemovesTemporaryFiles(t *testing.T) {
	root, config, owners, _ := livePinFixture(t)
	store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	dir := strings.TrimPrefix(store.Environment(), LivePins+"=")
	destination := filepath.Join(dir, livePinFilename("source-a"))
	require.NoError(t, os.Mkdir(destination, 0o700))
	require.ErrorContains(t, store.Remember("source-a", strings.Repeat("a", 40)), "publishing live pin")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "failed replacement keeps the original entries and removes its temporary file")
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".pin-")
	}
	require.NoError(t, os.RemoveAll(dir))
	assert.ErrorContains(t, store.Remember("source-a", strings.Repeat("a", 40)), "creating live pin")
}
