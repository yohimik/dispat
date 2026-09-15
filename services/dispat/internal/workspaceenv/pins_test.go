package workspaceenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPinsKeepExactOwnersAndReadLiveOutput(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "dispat.json")
	require.NoError(t, os.WriteFile(config, nil, 0o600))
	output := filepath.Join(root, "outputs")
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 64)
	env := []string{Root + "=" + root, Config + "=dispat.json", Imports + "=[]",
		Owners + `={"PACKAGE_LIB":"source-a","PACKAGE_TOOL":"source-a","PACKAGE_APP":"source-b","PACKAGE_CONTROL":"control"}`,
		"DISPAT_OUTPUT_PACKAGE_LIB=" + a, "DISPAT_OUTPUT_PACKAGE_APP=" + b,
		"DISPAT_OUTPUT_PACKAGE_UNKNOWN=" + c, "DISPAT_OUTPUT_PACKAGE_CONTROL=" + c,
		"DISPAT_OUTPUT=" + output}
	require.NoError(t, os.WriteFile(output, []byte("unrelated malformed export\nPACKAGE_LIB="+a+"\nDISPAT_OUTPUT_PACKAGE_TOOL="+c+"\nPACKAGE_APP=short\nPACKAGE_LIB="+strings.Repeat("d", 40)), 0o600))
	pins, err := Pins(root, config, env)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{a, c}, pins["source-a"])
	assert.Equal(t, []string{b}, pins["source-b"])
	assert.Len(t, pins, 2)
	// A second command sees a commit appended to the same live file even
	// though its inherited environment still contains the earlier value.
	f, err := os.OpenFile(output, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString("\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	pins, err = Pins(root, config, env)
	require.NoError(t, err)
	assert.Contains(t, pins["source-a"], strings.Repeat("d", 40))
	other, err := Pins(t.TempDir(), config, env)
	require.NoError(t, err)
	assert.Nil(t, other, "exported pins cannot authorize another workspace")
}

func TestPinsRequireCompleteContext(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "dispat.json")
	require.NoError(t, os.WriteFile(config, nil, 0o600))
	env := []string{Root + "=" + root, Config + "=dispat.json", Imports + "=[]", Owners + `={"PACKAGE_LIB":"source"}`,
		"DISPAT_OUTPUT_PACKAGE_LIB=" + strings.Repeat("a", 40)}
	for i := range 4 {
		incomplete := append(append([]string{}, env[:i]...), env[i+1:]...)
		pins, err := Pins(root, config, incomplete)
		require.NoError(t, err)
		assert.Nil(t, pins)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(root, alias))
	pins, err := Pins(alias, filepath.Join(alias, "dispat.json"), env)
	require.NoError(t, err)
	assert.Equal(t, []string{strings.Repeat("a", 40)}, pins["source"])
}

func TestPinsSkipOversizedExportsAndReportUnreadableOutput(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "dispat.json")
	require.NoError(t, os.WriteFile(config, nil, 0o600))
	output := filepath.Join(root, "outputs")
	sha := strings.Repeat("a", 40)
	env := []string{Root + "=" + root, Config + "=dispat.json", Imports + "=[]",
		Owners + `={"PACKAGE_LIB":"source"}`, "DISPAT_OUTPUT=" + output}
	// A large unrelated export must not truncate later pin evidence or be
	// retained by the bounded reader. A long line beginning with a known key
	// must not be accepted as a valid pin either.
	contents := "UNRELATED=" + strings.Repeat("x", 1<<20) + "\nPACKAGE_LIB=" + sha +
		strings.Repeat("x", 1<<16) + "\nPACKAGE_LIB=" + sha + "\n"
	require.NoError(t, os.WriteFile(output, []byte(contents), 0o600))
	pins, err := Pins(root, config, env)
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"source": {sha}}, pins)
	require.NoError(t, os.Remove(output))
	_, err = Pins(root, config, env)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
