package execution

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWorkerAuthorityIsTheExactMarker: the marker decides whether a process
// may start a release, so it is read exactly. A value dispat did not write is
// not worker authority, and neither is another variable that happens to hold
// the word.
func TestWorkerAuthorityIsTheExactMarker(t *testing.T) {
	for name, tc := range map[string]struct {
		env    []string
		isHeld bool
	}{
		"an empty environment":            {env: nil},
		"unrelated variables":             {env: []string{"PATH=/usr/bin", "HOME=/root"}},
		"the marker":                      {env: []string{"PATH=/usr/bin", AuthorityEnv + "=worker"}, isHeld: true},
		"the marker and the node":         {env: FormatWorkerAuthorityEnv("build-a"), isHeld: true},
		"an empty marker":                 {env: []string{AuthorityEnv + "="}},
		"the marker with no value at all": {env: []string{AuthorityEnv}},
		"another authority":               {env: []string{AuthorityEnv + "=orchestrator"}},
		"the word in the wrong case":      {env: []string{AuthorityEnv + "=Worker"}},
		"the word padded":                 {env: []string{AuthorityEnv + "= worker"}},
		"the word in another variable":    {env: []string{NodeEnv + "=worker"}},
		"a variable the name is a prefix of": {
			env: []string{AuthorityEnv + "_SCOPE=worker"},
		},
		"the first statement of the name wins": {
			env:    []string{AuthorityEnv + "=worker", AuthorityEnv + "=orchestrator"},
			isHeld: true,
		},
		"and wins when it is the one that says nothing": {
			env: []string{AuthorityEnv + "=", AuthorityEnv + "=worker"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.isHeld, IsWorkerAuthority(tc.env))
		})
	}
}

// TestWorkerAuthorityEnvCarriesTheMarkerAndTheNode: what a worker's task
// runner appends is read back as worker authority by the processes it starts,
// which is the only contract the two halves have with each other.
func TestWorkerAuthorityEnvCarriesTheMarkerAndTheNode(t *testing.T) {
	env := FormatWorkerAuthorityEnv("build-a")

	assert.Equal(t, []string{AuthorityEnv + "=worker", NodeEnv + "=build-a"}, env)
	assert.True(t, IsWorkerAuthority(env))
}
