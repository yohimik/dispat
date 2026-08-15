package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The condition grammar `dispat if` branches on. Parsing and evaluation are
// tested apart because they are separable: a spec that parses is not a spec
// that matches, and the failure a user sees ("that is not a variable name")
// comes from the first alone.

func TestParseConditionGrammar(t *testing.T) {
	// Each spelling parses into the variable, the question and the comparison
	// value, so a later reader can see the whole grammar in one table.
	for spec, want := range map[string]Condition{
		"CI":                {Name: "CI", op: opSet},
		"!CI":               {Name: "CI", op: opUnset},
		"ENV=prod":          {Name: "ENV", op: opEq, Value: "prod"},
		"ENV!=prod":         {Name: "ENV", op: opNe, Value: "prod"},
		"BRANCH~release/*":  {Name: "BRANCH", op: opGlob, Value: "release/*"},
		"BRANCH!~release/*": {Name: "BRANCH", op: opNotGlob, Value: "release/*"},
		// A value may be empty, contain the other operators, or be a lone "!":
		// only the first operator found ends the name, and everything after it
		// is taken verbatim.
		"ENV=":       {Name: "ENV", op: opEq, Value: ""},
		"ENV=a=b":    {Name: "ENV", op: opEq, Value: "a=b"},
		"ENV=!x":     {Name: "ENV", op: opEq, Value: "!x"},
		"ENV!=":      {Name: "ENV", op: opNe, Value: ""},
		"URL=a~b":    {Name: "URL", op: opEq, Value: "a~b"},
		"_PRIVATE":   {Name: "_PRIVATE", op: opSet},
		"A1_B2=x":    {Name: "A1_B2", op: opEq, Value: "x"},
		"TAG~v1.*.0": {Name: "TAG", op: opGlob, Value: "v1.*.0"},
	} {
		t.Run(spec, func(t *testing.T) {
			// Spec is always the text parsed, so the table states the parts
			// that vary and this fills in the one that cannot.
			want.Spec = spec
			got, err := ParseCondition(spec)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

func TestParseConditionPicksTheLeftmostOperator(t *testing.T) {
	// The operator that ends the name is the leftmost one in the spec, and the
	// longest at that position. Two separate rules, and both have a way of
	// going wrong that produces a parse rather than an error, which is why they
	// are pinned here rather than left to the grammar table.

	// Leftmost: an operator appearing later inside the *value* must not claim
	// the split. Scanning by operator length instead of position parsed this as
	// name "URL=a" globbing "b", which is not even a valid name.
	v, err := ParseCondition("URL=a~b")
	require.NoError(t, err)
	assert.Equal(t, Condition{Name: "URL", op: opEq, Value: "a~b", Spec: "URL=a~b"}, v,
		"an operator inside the value must not end the name")

	// Longest at that position: "!=" and "!~" begin one byte before the "=" or
	// "~" they contain, so the shorter token must not win the tie.
	ne, err := ParseCondition("ENV!=prod")
	require.NoError(t, err)
	assert.Equal(t, opNe, ne.op, "!= must not parse as = with a ! ending the name")
	assert.Equal(t, "prod", ne.Value)

	ng, err := ParseCondition("ENV!~pro*")
	require.NoError(t, err)
	assert.Equal(t, opNotGlob, ng.op, "!~ must not parse as ~ with a ! ending the name")
	assert.Equal(t, "pro*", ng.Value)
}

func TestParseConditionRejectsUnusableSpecs(t *testing.T) {
	// A malformed condition is a usage mistake, never a silent false: the two
	// are indistinguishable at a glance, and only one of them is worth an
	// error. Every message quotes the spec as typed.
	for name, spec := range map[string]string{
		"empty":              "",
		"bare bang":          "!",
		"no name before =":   "=value",
		"no name before !=":  "!=value",
		"no name before ~":   "~glob",
		"no name before !~":  "!~glob",
		"space in name":      "A B=c",
		"dash in name":       "MY-VAR=c",
		"leading digit":      "1VAR=c",
		"dot in name":        "MY.VAR",
		"negated bad name":   "!MY-VAR",
		"bare invalid name":  "my var",
		"dollar in the name": "$HOME",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseCondition(spec)
			require.Error(t, err, "an unusable condition must not parse")
			if spec != "" {
				assert.Contains(t, err.Error(), spec, "the message must quote the spec as typed")
			}
		})
	}
}

func TestConditionMatch(t *testing.T) {
	// Evaluation against a fixed environment. "set" means set and non-empty
	// throughout, so EMPTY behaves as though it were absent everywhere.
	env := map[string]string{
		"CI":     "1",
		"EMPTY":  "",
		"ENV":    "prod",
		"BRANCH": "release/1.2",
	}
	lookup := func(name string) string { return env[name] }

	for spec, want := range map[string]bool{
		"CI":      true,
		"MISSING": false,
		"EMPTY":   false, // set but empty is not "set"

		"!CI":      false,
		"!MISSING": true,
		"!EMPTY":   true,

		"ENV=prod":    true,
		"ENV=dev":     false,
		"EMPTY=":      true, // the only spelling that asks for "empty"
		"MISSING=":    true, // unset expands to nothing, as in the shell
		"MISSING=x":   false,
		"ENV!=dev":    true,
		"ENV!=prod":   false,
		"MISSING!=x":  true,
		"MISSING!=''": true,

		"BRANCH~release/*":   true,
		"BRANCH~main":        false,
		"BRANCH~*":           true,
		"BRANCH~*1.2":        true,
		"BRANCH~release/1.2": true,
		"EMPTY~*":            true, // "*" matches the empty string
		"MISSING~*":          true,
		"MISSING~x*":         false,
		"BRANCH!~main":       true,
		"BRANCH!~release/*":  false,
	} {
		t.Run(spec, func(t *testing.T) {
			c, err := ParseCondition(spec)
			require.NoError(t, err)
			assert.Equal(t, want, c.Match(lookup))
		})
	}
}

func TestResolvedConditionAnswersWithoutTheEnvironment(t *testing.T) {
	// A resolved condition carries an answer computed before the chain ran, so
	// Match reports that answer and reads nothing: the lookup must not even be
	// consulted, or a condition about the repository would silently become a
	// condition about a variable.
	looked := false
	lookup := func(string) string { looked = true; return "" }

	held := ResolvedCondition("--changed", true)
	assert.True(t, held.Match(lookup), "a condition resolved true matches")
	assert.Equal(t, "--changed", held.Spec, "the spec quotes what the user typed")

	missed := ResolvedCondition("-f report.json", false)
	assert.False(t, missed.Match(lookup), "a condition resolved false does not match")
	assert.Equal(t, "-f report.json", missed.Spec)

	assert.False(t, looked, "the environment must not be consulted")
}

func TestFileConditionReadsTheFilesystem(t *testing.T) {
	// The -f and -d file tests, against a real folder: -f asks for a regular
	// file, -d for a directory, and a path that is absent or the wrong kind is
	// false rather than an error, matching the shell's [ -f ] and [ -d ].
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "report.json"), []byte("{}"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "build"), 0o755))

	for name, tc := range map[string]struct {
		path    string
		wantDir bool
		held    bool
	}{
		"-f on a file":         {path: "report.json", held: true},
		"-f on a folder":       {path: "build", held: false},
		"-f on a missing path": {path: "ghost.json", held: false},
		"-d on a folder":       {path: "build", wantDir: true, held: true},
		"-d on a file":         {path: "report.json", wantDir: true, held: false},
		"-d on a missing path": {path: "ghost", wantDir: true, held: false},
	} {
		t.Run(name, func(t *testing.T) {
			c := FileCondition(dir, tc.path, tc.wantDir)
			assert.Equal(t, tc.held, c.Match(func(string) string { return "" }))
		})
	}

	// An absolute path ignores the folder it would otherwise be joined onto,
	// and a relative one is read from that folder alone.
	abs := FileCondition(t.TempDir(), filepath.Join(dir, "report.json"), false)
	assert.True(t, abs.Match(func(string) string { return "" }),
		"an absolute path is used as it is")
	rel := FileCondition(t.TempDir(), "report.json", false)
	assert.False(t, rel.Match(func(string) string { return "" }),
		"a relative path resolves against the given folder, not the process cwd")

	// The spec keeps the flag spelling, so logs quote the test as typed.
	assert.Equal(t, "-f report.json", FileCondition(dir, "report.json", false).Spec)
	assert.Equal(t, "-d build", FileCondition(dir, "build", true).Spec)
}
