package release

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

type failingSourceRecorder struct{}

func (failingSourceRecorder) Record(_ context.Context, rel *plan.Release) error {
	if rel.Pkg.Name == "provider" {
		return errors.New("control checkpoint failed after source tag")
	}
	return nil
}

func TestRequiredRecordFailureBlocksConsumerClosure(t *testing.T) {
	for _, required := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "required records"}[required], func(t *testing.T) {
			pl := mkPlan(planSpec{
				Names:   []string{"provider", "consumer", "leaf", "independent"},
				Deps:    map[string][]string{"consumer": {"provider"}, "leaf": {"consumer"}},
				OwnBump: map[string]ccme.Bump{"provider": ccme.BumpMinor, "consumer": ccme.BumpPatch, "leaf": ccme.BumpPatch, "independent": ccme.BumpPatch},
			})
			runner := &fakeRunner{}
			reverter := &fakeReverter{}
			ex := &Executor{Runner: runner, Recorders: []ReleaseRecorderx{failingSourceRecorder{}},
				BlockOnRecordFailure: required, Reverter: reverter, Log: zerolog.Nop()}
			results := ex.Run(context.Background(), pl)
			require.Equal(t, StatusPublished, results["provider"].Status)
			require.Len(t, results["provider"].Critical, 1)
			assert.Equal(t, required, results["provider"].RecordBlocked)
			assert.Equal(t, StatusPublished, results["independent"].Status)
			for _, consumer := range []string{"consumer", "leaf"} {
				if required {
					assert.Equal(t, StatusSkipped, results[consumer].Status)
					assert.True(t, results[consumer].RecordBlocked)
				} else {
					assert.Equal(t, StatusPublished, results[consumer].Status)
				}
			}
			assert.NotContains(t, reverter.dirs, pl.Releases["provider"].Pkg.Dir, "published source is never reverted")
		})
	}
}
