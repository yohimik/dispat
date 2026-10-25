package release

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

func TestValidateExecutionGraphFindsPlanSpecificOutcomeCycle(t *testing.T) {
	p := mkPublishOrderPlan("none", "none")
	p.Providers["app"] = []string{"core"}
	p.Providers["core"] = []string{"app"}
	p.PublishProviders = map[string][]string{"app": {}, "core": {}}
	p.BlockingProviders = map[string][]string{"app": {}, "core": {}}
	for _, pair := range [][2]string{{"app", "core"}, {"core", "app"}} {
		rel := p.Releases[pair[0]]
		rel.OwnBump, rel.Units, rel.FreshUnits = ccme.BumpNone, nil, nil
		rel.DueTo = []string{pair[1]}
	}
	err := ValidateExecutionGraph(p)
	var cycle *ExecutionCycleError
	require.ErrorAs(t, err, &cycle)
	assert.Equal(t, "E233", cycle.DiagnosticCode())
	assert.Contains(t, cycle.Error(), "app:")
	assert.Contains(t, cycle.Error(), "core:")
}

func TestValidateExecutionGraphRejectsMutualVersionPickupCycle(t *testing.T) {
	p := mkPublishOrderPlan("none", "none")
	p.Providers["app"] = []string{"core"}
	p.Providers["core"] = []string{"app"}
	p.PublishProviders = map[string][]string{"app": {}, "core": {}}
	p.BlockingProviders = map[string][]string{"app": {}, "core": {}}
	for _, pair := range [][2]string{{"app", "core"}, {"core", "app"}} {
		rel := p.Releases[pair[0]]
		rel.Pkg.Space.VersionScript = []string{"sync"}
		rel.Updates = []plan.ProviderUpdate{{Name: pair[1]}}
	}
	err := ValidateExecutionGraph(p)
	var cycle *ExecutionCycleError
	require.ErrorAs(t, err, &cycle)
	assert.Contains(t, cycle.Error(), "app:version")
	assert.Contains(t, cycle.Error(), "core:version")
}
