package workspaceenv

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func livePinFixture(t *testing.T) (root, config string, owners map[string]string, env []string) {
	t.Helper()
	root = t.TempDir()
	config = filepath.Join(root, "dispat.json")
	require.NoError(t, os.WriteFile(config, []byte("{}"), 0o600))
	owners = map[string]string{"PACKAGE_LIB": "source-a", "PACKAGE_APP": "source-b"}
	env = []string{
		Root + "=" + root, Config + "=dispat.json", Imports + "=[]",
		Owners + `={"PACKAGE_LIB":"source-a","PACKAGE_APP":"source-b"}`,
		Repositories + `=["source-a","source-b"]`,
	}
	return
}

func TestLivePinsArePrivateAtomicAndReadFresh(t *testing.T) {
	root, config, owners, env := livePinFixture(t)
	store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	dir := strings.TrimPrefix(store.Environment(), LivePins+"=")
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	env = append(env, store.Environment())

	first, second := strings.Repeat("a", 40), strings.Repeat("b", 40)
	require.NoError(t, store.Remember("source-a", first))
	pins, err := Pins(root, config, env)
	require.NoError(t, err)
	assert.Equal(t, []string{first}, pins["source-a"])

	require.NoError(t, store.Remember("source-a", second))
	pins, err = Pins(root, config, env)
	require.NoError(t, err)
	assert.Equal(t, []string{second}, pins["source-a"], "each invocation reads the latest atomic replacement")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "one context file and one hashed owner file")
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), "source-a")
		fileInfo, err := entry.Info()
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
	}
}

func TestNestedCommandPublishesLivePinForAlreadyRunningSiblings(t *testing.T) {
	root, config, owners, env := livePinFixture(t)
	store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	env = append(env, store.Environment())
	pin := strings.Repeat("c", 64)

	require.NoError(t, RememberLivePin(root, config, env, "source-b", pin))
	pins, err := Pins(root, config, env)
	require.NoError(t, err)
	assert.Equal(t, []string{pin}, pins["source-b"])
}

func TestLivePinsRejectMismatchedOrUnownedWrites(t *testing.T) {
	root, config, owners, env := livePinFixture(t)
	store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	env = append(env, store.Environment())
	pin := strings.Repeat("d", 40)

	assert.Error(t, store.Remember("control", pin))
	assert.Error(t, store.Remember("foreign", pin))
	assert.Error(t, store.Remember("source-a", "HEAD"))
	mismatched := append([]string(nil), env...)
	mismatched[3] = Owners + `={"PACKAGE_LIB":"source-b"}`
	_, err = Pins(root, config, mismatched)
	assert.ErrorContains(t, err, "does not match this workspace")
	assert.Error(t, RememberLivePin(root, config, mismatched, "source-a", pin))
}

func TestLivePinsCleanupRemovesTransientDirectory(t *testing.T) {
	root, config, owners, _ := livePinFixture(t)
	store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
	require.NoError(t, err)
	dir := strings.TrimPrefix(store.Environment(), LivePins+"=")
	require.NoError(t, store.Close())
	require.NoError(t, store.Close())
	_, err = os.Stat(dir)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Empty(t, store.Environment(), "closed coordination cannot be inherited")
	assert.ErrorContains(t, store.Remember("source-a", strings.Repeat("a", 40)), "closed",
		"writing after Close must not resolve the owner filename against the working directory")
}

func TestLivePinsAuthorizeRepositoriesWhosePackageKeysCollide(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "dispat.json")
	require.NoError(t, os.WriteFile(config, []byte("{}"), 0o600))
	store, err := NewLivePins(root, config, map[string]string{}, []string{"a-source", "b-source"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	env := []string{
		Root + "=" + root, Config + "=dispat.json", Imports + "=[]", Owners + "={}",
		Repositories + `=["a-source","b-source"]`, store.Environment(),
	}
	pin := strings.Repeat("e", 40)
	require.NoError(t, store.Remember("a-source", pin))
	reader, err := OpenLivePins(root, config, env)
	require.NoError(t, err)
	require.NotNil(t, reader)
	got, err := reader.Pins("a-source")
	require.NoError(t, err)
	assert.Equal(t, []string{pin}, got)
}

func TestLivePinsRejectMalformedMetadataFiles(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{"trailing JSON", func(t *testing.T, path string) {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			require.NoError(t, err)
			_, err = f.WriteString("{}")
			require.NoError(t, err)
			require.NoError(t, f.Close())
		}},
		{"unknown field", func(t *testing.T, path string) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			data = bytes.TrimSpace(data)
			require.NoError(t, os.WriteFile(path,
				append(append([]byte(nil), data[:len(data)-1]...), []byte(`,"unknown":true}`)...), 0o600))
		}},
		{"symlink", func(t *testing.T, path string) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			target := filepath.Join(t.TempDir(), "metadata.json")
			require.NoError(t, os.WriteFile(target, data, 0o600))
			require.NoError(t, os.Remove(path))
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
		{"non regular", func(t *testing.T, path string) {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Mkdir(path, 0o700))
		}},
		{"oversize", func(t *testing.T, path string) {
			require.NoError(t, os.Truncate(path, (8<<20)+1))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, config, owners, env := livePinFixture(t)
			store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
			require.NoError(t, err)
			t.Cleanup(func() { _ = store.Close() })
			dir := strings.TrimPrefix(store.Environment(), LivePins+"=")
			env = append(env, store.Environment())
			tc.mutate(t, filepath.Join(dir, liveMetadataFile))

			_, err = OpenLivePins(root, config, env)
			require.Error(t, err)
		})
	}
}

func TestLivePinsRejectMalformedOwnerRecords(t *testing.T) {
	valid := strings.Repeat("a", 40)
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{"trailing JSON", func(t *testing.T, path string) {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			require.NoError(t, err)
			_, err = f.WriteString("{}")
			require.NoError(t, err)
			require.NoError(t, f.Close())
		}},
		{"unknown field", func(t *testing.T, path string) {
			require.NoError(t, os.WriteFile(path,
				[]byte(`{"owner":"source-a","revision":"`+valid+`","unknown":true}`), 0o600))
		}},
		{"wrong owner", func(t *testing.T, path string) {
			require.NoError(t, os.WriteFile(path,
				[]byte(`{"owner":"source-b","revision":"`+valid+`"}`), 0o600))
		}},
		{"short revision", func(t *testing.T, path string) {
			require.NoError(t, os.WriteFile(path,
				[]byte(`{"owner":"source-a","revision":"`+valid[:12]+`"}`), 0o600))
		}},
		{"symlink", func(t *testing.T, path string) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			target := filepath.Join(t.TempDir(), "pin.json")
			require.NoError(t, os.WriteFile(target, data, 0o600))
			require.NoError(t, os.Remove(path))
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
		{"non regular", func(t *testing.T, path string) {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Mkdir(path, 0o700))
		}},
		{"oversize", func(t *testing.T, path string) {
			require.NoError(t, os.Truncate(path, 4097))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, config, owners, env := livePinFixture(t)
			store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
			require.NoError(t, err)
			t.Cleanup(func() { _ = store.Close() })
			require.NoError(t, store.Remember("source-a", valid))
			env = append(env, store.Environment())
			reader, err := OpenLivePins(root, config, env)
			require.NoError(t, err)
			dir := strings.TrimPrefix(store.Environment(), LivePins+"=")
			tc.mutate(t, filepath.Join(dir, livePinFilename("source-a")))

			_, err = reader.Pins("source-a")
			require.Error(t, err)
		})
	}
}

func TestLivePinsRejectStaleCoordinator(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{"deleted", func(t *testing.T, dir string) {
			require.NoError(t, os.RemoveAll(dir))
		}},
		{"replaced by file", func(t *testing.T, dir string) {
			require.NoError(t, os.RemoveAll(dir))
			require.NoError(t, os.WriteFile(dir, []byte("stale"), 0o600))
		}},
		{"replaced by symlink", func(t *testing.T, dir string) {
			require.NoError(t, os.RemoveAll(dir))
			target := t.TempDir()
			if err := os.Symlink(target, dir); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, config, owners, env := livePinFixture(t)
			store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
			require.NoError(t, err)
			t.Cleanup(func() { _ = store.Close() })
			env = append(env, store.Environment())
			require.NoError(t, store.Remember("source-a", strings.Repeat("a", 40)))
			reader, err := OpenLivePins(root, config, env)
			require.NoError(t, err)
			missing, err := reader.Pins("source-b")
			require.NoError(t, err)
			assert.Nil(t, missing, "an intact coordinator may have no record for an owner")
			dir := strings.TrimPrefix(store.Environment(), LivePins+"=")
			tc.mutate(t, dir)

			_, openErr := OpenLivePins(root, config, env)
			require.Error(t, openErr)
			_, readErr := reader.Pins("source-a")
			require.Error(t, readErr)
			if tc.name == "deleted" {
				assert.ErrorIs(t, openErr, os.ErrNotExist)
				assert.ErrorIs(t, readErr, os.ErrNotExist)
			}
		})
	}
}
