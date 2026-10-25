package integration

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestSemverCLIParsesAndBuildsWithoutWorkspace(t *testing.T) {
	repo := harness.New(t)
	repo.WorkFrom()
	require.NoError(t, os.RemoveAll(repo.Path(".git")))
	repo.WriteConfig("not valid json")
	big := strings.Repeat("9", 80)
	raw := big + ".0.3-alpha." + big + "+001"

	parsed := repo.CommandEnv([]string{"PATH=" + t.TempDir()}, "semver", "parse", raw)
	require.Equal(t, 0, parsed.Code, "%s%s", parsed.Stdout, parsed.Stderr)
	assert.Empty(t, parsed.Stderr)
	var value map[string]any
	require.NoError(t, json.Unmarshal([]byte(parsed.Stdout), &value))
	assert.Equal(t, raw, value["version"])
	assert.Equal(t, big, value["major"])
	assert.Equal(t, "0", value["minor"])
	assert.Equal(t, "3", value["patch"])
	assert.Equal(t, []any{"alpha", big}, value["prerelease"])
	assert.Equal(t, []any{"001"}, value["build"])

	built := repo.CommandEnv([]string{"PATH=" + t.TempDir()}, "semver", "build", big, "0", "3",
		"--pre-release", "alpha."+big, "--build-metadata", "001")
	require.Equal(t, 0, built.Code, "%s%s", built.Stdout, built.Stderr)
	assert.JSONEq(t, parsed.Stdout, built.Stdout)

	invalid := repo.Command("semver", "build", "01", "0", "3")
	assert.Equal(t, 2, invalid.Code, "%s%s", invalid.Stdout, invalid.Stderr)
	assert.Empty(t, invalid.Stdout)
	assert.Contains(t, invalid.Stderr, "leading zero")
}
