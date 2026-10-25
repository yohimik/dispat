package app

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// rescanKept is the reading keepDeclared replaced: one pass over every
// declaration per touched list.
func rescanKept(declared []config.DeclaredDependency, key string, edit *listEdit) []config.DependencyConfig {
	var kept []config.DependencyConfig
	for _, d := range declared {
		if listKey(d.Source) != key || edit.remove[d.Source.Index] {
			continue
		}
		entry := d.DependencyConfig
		if kind, ok := edit.kind[d.Source.Index]; ok {
			entry.Kind = kind
		}
		kept = append(kept, entry)
	}
	return kept
}

// TestKeepDeclaredMatchesAPerListRescan: the one-pass bucketing gives every
// touched list the entries, order and kinds a rescan per list gives, with
// the sources' entries interleaved, a list emptied by its removals, and a
// list nobody touched.
func TestKeepDeclaredMatchesAPerListRescan(t *testing.T) {
	sources := []config.DepSource{
		{KeyPath: []string{"dependencies"}},
		{File: "packages/web/dispat.json", KeyPath: []string{"dependencies"}},
		{Space: "libs", KeyPath: []string{"spaces", "libs", "packages", "ui", "dependencies"}},
		{Repository: "sdk", KeyPath: []string{"dependencies"}},
		{File: "packages/untouched/dispat.json", KeyPath: []string{"dependencies"}},
	}
	var declared []config.DeclaredDependency
	for i := range 6 {
		for s, src := range sources {
			src.Index = i
			declared = append(declared, config.DeclaredDependency{
				DependencyConfig: config.DependencyConfig{Consumer: fmt.Sprintf("c%d", s), Provider: fmt.Sprintf("p%d-%d", s, i)},
				Source:           src,
			})
		}
	}
	lists := newListEdits()
	lists.at(sources[0]).remove[1] = true
	lists.at(sources[0]).kind[4] = "dev"
	lists.at(sources[1]).kind[0] = "build"
	lists.at(sources[2]).remove[5] = true
	for i := range 6 {
		lists.at(sources[3]).remove[i] = true
	}

	lists.keepDeclared(declared)

	assert.Len(t, lists.byKey, 4, "an untouched list is never bucketed")
	for _, k := range lists.keys() {
		edit := lists.byKey[k]
		assert.Equal(t, rescanKept(declared, k, edit), edit.kept, "%v", edit.src)
	}
	assert.Nil(t, lists.byKey[listKey(sources[3])].kept, "every entry removed leaves the list as the rescan did")
}
