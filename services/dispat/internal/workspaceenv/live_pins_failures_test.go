package workspaceenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPinsRejectsCorruptLiveRecordDespiteInheritedCandidate proves that an
// older valid package export cannot hide corrupt coordination from a sibling
// command that was already running. Once live context is present, failing to
// read its latest owner record is fatal rather than a fallback to stale state.
func TestPinsRejectsCorruptLiveRecordDespiteInheritedCandidate(t *testing.T) {
	root, config, owners, env := livePinFixture(t)
	store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	oldPin := strings.Repeat("a", 40)
	newPin := strings.Repeat("b", 40)
	require.NoError(t, store.Remember("source-a", newPin))
	env = append(env, "DISPAT_OUTPUT_PACKAGE_LIB="+oldPin, store.Environment())
	dir := strings.TrimPrefix(store.Environment(), LivePins+"=")
	require.NoError(t, os.WriteFile(filepath.Join(dir, livePinFilename("source-a")),
		[]byte(`{"owner":"source-b","revision":"`+newPin+`"}`), 0o600))

	pins, err := Pins(root, config, env)
	require.Nil(t, pins)
	require.ErrorContains(t, err, "live pin for repository source-a is malformed")
}

// TestRememberLivePinRejectsStaleInheritedContext verifies that a nested
// recorder cannot silently lose a successful source revision after the outer
// run's coordinator has gone away, even though all inherited identities still
// look valid.
func TestRememberLivePinRejectsStaleInheritedContext(t *testing.T) {
	root, config, owners, env := livePinFixture(t)
	store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
	require.NoError(t, err)
	liveEnvironment := store.Environment()
	require.NoError(t, store.Close())
	env = append(env, liveEnvironment)

	err = RememberLivePin(root, config, env, "source-a", strings.Repeat("c", 40))
	require.ErrorContains(t, err, "opening live pin context")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// TestPinsStayBoundToExactConfiguration keeps transient authorization scoped
// to the one config selected by the outer release. Another valid config in
// the same control repository receives no pins, and removal of the selected
// config invalidates its live context.
func TestPinsStayBoundToExactConfiguration(t *testing.T) {
	root, config, owners, env := livePinFixture(t)
	otherConfig := filepath.Join(root, "other.json")
	require.NoError(t, os.WriteFile(otherConfig, []byte("{}"), 0o600))
	store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	pin := strings.Repeat("d", 40)
	require.NoError(t, store.Remember("source-a", pin))
	env = append(env, "DISPAT_OUTPUT_PACKAGE_LIB="+pin, store.Environment())

	pins, err := Pins(root, otherConfig, env)
	require.NoError(t, err)
	assert.Nil(t, pins, "a sibling config must not inherit this run's source authorization")
	require.NoError(t, os.Remove(config))
	pins, err = Pins(root, config, env)
	require.NoError(t, err)
	assert.Nil(t, pins, "a missing selected config cannot match inherited context")
}

// TestPinsRejectsMalformedFullLengthExports checks the lexical half of the
// full-object-ID contract. Length alone is insufficient: uppercase and
// non-hex values from either inherited exports or the live output file cannot
// authorize a source checkout.
func TestPinsRejectsMalformedFullLengthExports(t *testing.T) {
	root, config, _, env := livePinFixture(t)
	output := filepath.Join(root, "outputs")
	require.NoError(t, os.WriteFile(output, []byte(
		"DISPAT_OUTPUT_PACKAGE_LIB="+strings.Repeat("A", 40)+"\n"+
			"DISPAT_OUTPUT_PACKAGE_APP="+strings.Repeat("g", 64)+"\n"), 0o600))
	env = append(env,
		"DISPAT_OUTPUT_PACKAGE_LIB="+strings.Repeat("Z", 40),
		"DISPAT_OUTPUT="+output,
	)

	pins, err := Pins(root, config, env)
	require.NoError(t, err)
	assert.Empty(t, pins)
}

// TestNewLivePinsReportsUnavailableTemporaryRoot exercises the operational
// failure before any inheritable path exists. The caller receives an error and
// no partial store when its configured temporary directory cannot be entered.
func TestNewLivePinsReportsUnavailableTemporaryRoot(t *testing.T) {
	root, config, owners, _ := livePinFixture(t)
	missing := filepath.Join(t.TempDir(), "missing")
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, missing)
	}

	store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
	require.Nil(t, store)
	require.ErrorContains(t, err, "creating live pin directory")
}
