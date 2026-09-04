package integration

// Regression for test-plan goals 18 and 21: deep revision windows keep their
// exact lower bound, `all` removes it, and consumer expansion remains
// transitive after the ninth commit rather than stopping at the first edge.

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestSelectionWindowsFromHEAD1ThroughHEAD9(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	names := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "web"}
	for _, name := range names {
		r.SeedPackage("packages", name)
	}
	for i := 1; i < len(names); i++ {
		cfg.Dependencies = append(cfg.Dependencies, models.DependencyConfig{Consumer: names[i], Provider: names[i-1]})
	}
	r.WriteConfigModel(cfg)
	r.Commit("chore: seed deep selection graph")
	for i := 1; i <= 9; i++ {
		name := fmt.Sprintf("p%d", i)
		r.WriteFile("packages/"+name+"/change.txt", fmt.Sprintf("%d\n", i))
		r.Commit("fix(" + name + "): revision window marker")
	}
	for depth := 1; depth <= 9; depth++ {
		want := append([]string(nil), names[9-depth:9]...)
		assert.Equal(t, want, forWindow(t, r, "--since", fmt.Sprintf("HEAD~%d", depth)), "depth %d", depth)
	}
	assert.Equal(t, names, forWindow(t, r, "--since", "all"))
	assert.Equal(t, []string{"web"}, forWindow(t, r, "--since", "HEAD~9", "--package", "web", "--consumers"),
		"consumer expansion reaches the transitive web leaf before the filter narrows to it")
}
