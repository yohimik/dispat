package semver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	valid := map[string]Version{
		"1.2.3":    {1, 2, 3},
		"v1.2.3":   {1, 2, 3},
		"0.0.0":    {0, 0, 0},
		"10.20.30": {10, 20, 30},
	}
	for in, want := range valid {
		got, err := Parse(in)
		require.NoError(t, err, "Parse(%q)", in)
		assert.Equal(t, want, got, "Parse(%q)", in)
	}

	invalid := []string{"", "1", "1.2", "1.2.3.4", "1.a.3", "-1.2.3", "1.2.3-rc1", "1..3", "a.b.c", "1.2."}
	for _, in := range invalid {
		_, err := Parse(in)
		assert.Error(t, err, "Parse(%q)", in)
	}
}

func TestBumped(t *testing.T) {
	v := Version{1, 2, 3}
	cases := []struct {
		bump Bump
		want Version
	}{
		{BumpNone, Version{1, 2, 3}},
		{BumpPatch, Version{1, 2, 4}},
		{BumpMinor, Version{1, 3, 0}},
		{BumpMajor, Version{2, 0, 0}},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, v.Bumped(c.bump), "Bumped(%v)", c.bump)
	}
}

func TestMax(t *testing.T) {
	assert.Equal(t, BumpMajor, Max(BumpPatch, BumpMajor))
	assert.Equal(t, BumpMinor, Max(BumpMinor, BumpNone))
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b Version
		want int
	}{
		{Version{1, 0, 0}, Version{1, 0, 0}, 0},
		{Version{1, 0, 0}, Version{2, 0, 0}, -1},
		{Version{1, 10, 0}, Version{1, 2, 0}, 1},
		{Version{1, 2, 3}, Version{1, 2, 4}, -1},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, c.a.Compare(c.b), "%v.Compare(%v)", c.a, c.b)
	}
}

func TestString(t *testing.T) {
	assert.Equal(t, "1.2.3", Version{1, 2, 3}.String())
}
