package app

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The planner and guard can enumerate the same repositories in different
// orders. A compact closure must keep repository identities across both the
// reorder and a machine-word boundary, including an optional checkpoint.
func TestSnapshotRemapsRepositoryInputsAcrossWordBoundaries(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		t.Run(fmt.Sprintf("checkpoint=%t", checkpoint), func(t *testing.T) {
			names := []string{config.ControlRepository}
			for i := range 65 {
				names = append(names, fmt.Sprintf("source-%02d", i))
			}
			w := snapshotClosureFixture(names...)
			w.byName[config.ControlRepository].repo.Commit.Enabled = &checkpoint
			reversed := slices.Clone(names)
			slices.Reverse(reversed)
			// In reversed order, source-64 is bit 0 and source-00 is bit 64.
			input := []uint64{1, 1}
			rel := closureRelease("lib", "source-64")
			sibling := closureRelease("tool", "source-64")
			pl := &plan.Plan{
				Order:                []string{"lib", "tool"},
				Releases:             map[string]*plan.Release{"lib": rel, "tool": sibling},
				RepositoryInputOrder: reversed,
				RepositoryInputs:     map[string][]uint64{"lib": input, "tool": input},
			}

			w.setSnapshotPlan(pl)

			set := w.snapshot.byRelease[rel]
			require.NotNil(t, set)
			for index, name := range names {
				want := name == "source-00" || name == "source-64" || (checkpoint && name == config.ControlRepository)
				assert.Equal(t, want, set.contains(index), "repository %s", name)
			}
			assert.Equal(t, []uint64{1, 1}, input, "remapping must not rewrite shared planner input")
			assert.Same(t, set, w.snapshot.byRelease[sibling], "equal remapped closures remain interned")
		})
	}
}
