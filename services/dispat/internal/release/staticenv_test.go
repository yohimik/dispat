package release

// StaticEnv: the ordering rule that makes the computed DISPAT_* namespace
// dependable, and the expansion that makes a configured value mean what it
// reads.

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticEnvPlacesStaticFirstSoComputedWins(t *testing.T) {
	// exec resolves duplicates last-wins, so putting the computed set last is
	// what stops a configured key from shadowing a computed one. Both entries
	// are present; the computed one is the effective value.
	out := StaticEnv(
		[]string{"DISPAT_VERSION=hijacked", "OWN=mine"},
		[]string{"DISPAT_VERSION=1.2.3", "DISPAT_PACKAGE=core"})

	require.Equal(t, []string{
		"DISPAT_VERSION=hijacked", "OWN=mine",
		"DISPAT_VERSION=1.2.3", "DISPAT_PACKAGE=core",
	}, out)
	assert.Greater(t, slices.Index(out, "DISPAT_VERSION=1.2.3"),
		slices.Index(out, "DISPAT_VERSION=hijacked"),
		"the computed value must come last, so exec keeps it")
}

func TestStaticEnvWithoutStaticIsThePlainComputedSlice(t *testing.T) {
	// A repository that configures no env pays nothing: the computed slice is
	// handed back as it arrived, not copied.
	computed := []string{"DISPAT_PACKAGE=core"}
	out := StaticEnv(nil, computed)
	require.Equal(t, computed, out)
	assert.Equal(t, &computed[0], &out[0], "no allocation for the unconfigured case")

	assert.Nil(t, StaticEnv(nil, nil))
}

func TestStaticEnvExpandsValues(t *testing.T) {
	// cmd.Env values are never shell-expanded, so dispat expands them itself:
	// a configured "custom_$DISPAT_VERSION" has to arrive as the version.
	t.Setenv("FROM_PROCESS", "process")
	computed := []string{"DISPAT_VERSION=1.4.0", "DISPAT_PACKAGE=core"}

	cases := map[string]struct{ in, want string }{
		"bare name":        {"custom_$DISPAT_VERSION", "custom_1.4.0"},
		"braced name":      {"${DISPAT_PACKAGE}-suffix", "core-suffix"},
		"two references":   {"$DISPAT_PACKAGE@$DISPAT_VERSION", "core@1.4.0"},
		"process fallback": {"$FROM_PROCESS", "process"},
		"unknown is empty": {"a${NOPE}b", "ab"},
		"escaped dollar":   {"cost: $$5", "cost: $5"},
		"no reference":     {"plain value", "plain value"},
		"empty value":      {"", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out := StaticEnv([]string{"K=" + c.in}, computed)
			assert.Equal(t, "K="+c.want, out[0])
		})
	}
}

// TestStaticEnvComputedWinsTheLookupToo: a name is resolved against the
// computed set before the process environment, so a value referring to
// DISPAT_PACKAGE gets this run's package even when the ambient environment
// happens to carry one (a nested dispat invocation always does).
func TestStaticEnvComputedWinsTheLookupToo(t *testing.T) {
	t.Setenv("DISPAT_PACKAGE", "outer")
	out := StaticEnv([]string{"K=$DISPAT_PACKAGE"}, []string{"DISPAT_PACKAGE=inner"})
	assert.Equal(t, "K=inner", out[0])
}

// TestStaticEnvDoesNotAliasItsInputs: the static slice belongs to the space
// and is shared by every package in it, so building one package's environment
// must not write through to the next one's.
func TestStaticEnvDoesNotAliasItsInputs(t *testing.T) {
	static := []string{"K=$DISPAT_VERSION"}
	computed := []string{"DISPAT_VERSION=1.0.0"}
	first := StaticEnv(static, computed)
	require.Equal(t, "K=1.0.0", first[0])

	// The space's own slice is untouched, so the next package expands it
	// against its own version rather than inheriting the first one's.
	assert.Equal(t, []string{"K=$DISPAT_VERSION"}, static)
	second := StaticEnv(static, []string{"DISPAT_VERSION=2.0.0"})
	assert.Equal(t, "K=2.0.0", second[0])
	assert.Equal(t, "K=1.0.0", first[0], "the earlier result is not rewritten")
}

// TestStaticEnvKeepsValuesWithEqualsSigns: only the first "=" separates a pair,
// so a value that itself contains one survives.
func TestStaticEnvKeepsValuesWithEqualsSigns(t *testing.T) {
	out := StaticEnv([]string{"GOFLAGS=-mod=mod"}, []string{"DISPAT_PACKAGE=core"})
	assert.Equal(t, "GOFLAGS=-mod=mod", out[0])
}
