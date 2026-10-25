package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/semver"
)

var benchmarkBuiltParts semver.VersionParts

func TestSemverParseKeepsFullVersionAndUnboundedCore(t *testing.T) {
	big := strings.Repeat("9", 80)
	raw := big + ".0.3-rc.1+sha.001"
	var stdout, stderr bytes.Buffer
	code := Run([]string{"semver", "parse", raw, "--root", t.TempDir()}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	assert.Empty(t, stderr.String())
	var got semverOutput
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	assert.Equal(t, raw, got.Version)
	assert.Equal(t, big, got.Major)
	assert.Equal(t, "0", got.Minor)
	assert.Equal(t, "3", got.Patch)
	assert.Equal(t, []string{"rc", "1"}, got.Prerelease)
	assert.Equal(t, []string{"sha", "001"}, got.Build)
	assert.Equal(t, 1, bytes.Count(stdout.Bytes(), []byte("\n")), "one JSON record")
}

func TestSemverBuildRoundTripsThroughParse(t *testing.T) {
	var built, stderr bytes.Buffer
	code := Run([]string{"semver", "build", "1", "2", "3", "--pre-release", "rc.1",
		"--build-metadata", "sha.001", "--root", t.TempDir()}, &built, &stderr)
	require.Equal(t, 0, code, stderr.String())
	var object semverOutput
	require.NoError(t, json.Unmarshal(built.Bytes(), &object))
	assert.Equal(t, "1.2.3-rc.1+sha.001", object.Version)
	assert.Equal(t, []string{"rc", "1"}, object.Prerelease)
	assert.Equal(t, []string{"sha", "001"}, object.Build)

	var parsed bytes.Buffer
	stderr.Reset()
	code = Run([]string{"semver", "parse", object.Version, "--root", t.TempDir()}, &parsed, &stderr)
	require.Equal(t, 0, code, stderr.String())
	assert.JSONEq(t, built.String(), parsed.String())
}

func TestSemverRefusesInvalidInputWithoutJSON(t *testing.T) {
	for _, args := range [][]string{
		{"semver"}, {"semver", "parse"}, {"semver", "build", "1", "2"},
		{"semver", "parse", "01.2.3"}, {"semver", "parse", "1.2.3-01"},
		{"semver", "parse", "1.2.3+"}, {"semver", "parse", "1.2.3-α"},
		{"semver", "parse", "1.2.3", "--pre-release", "rc"},
		{"semver", "build", "01", "2", "3"},
		{"semver", "build", "1", "2", "3", "--pre-release", "a.00"},
		{"semver", "build", "1", "2", "3", "--build-metadata", ""},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(append(append([]string{}, args...), "--root", t.TempDir()), &stdout, &stderr)
		assert.Equal(t, 2, code, "%v: %s", args, stderr.String())
		assert.Empty(t, stdout.String(), "%v", args)
		assert.NotEmpty(t, stderr.String(), "%v", args)
	}
}

func TestSemverHelpIsCommandScoped(t *testing.T) {
	var stdout, stderr bytes.Buffer
	assert.Zero(t, Run([]string{"semver", "--help"}, &stdout, &stderr))
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "usage: dispat semver parse <version> | build <major> <minor> <patch>")
	assert.Contains(t, stderr.String(), "--pre-release")
	assert.Contains(t, stderr.String(), "--build-metadata")
}

func BenchmarkBuildSemverScanner(b *testing.B) {
	input := semverSpelling{core: []string{"12", "34", "56"}, preSet: true, pre: "rc.7",
		buildSet: true, build: "sha.0123456789abcdef"}
	for i := 0; i < b.N; i++ {
		parts, err := semver.ParseVersionParts(buildSemverSpelling(input))
		if err != nil {
			b.Fatal(err)
		}
		benchmarkBuiltParts = parts
	}
}

func BenchmarkBuildSemverFmt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		parts, err := semver.ParseVersionParts(fmt.Sprintf("%s.%s.%s-%s+%s", "12", "34", "56", "rc.7", "sha.0123456789abcdef"))
		if err != nil {
			b.Fatal(err)
		}
		benchmarkBuiltParts = parts
	}
}
