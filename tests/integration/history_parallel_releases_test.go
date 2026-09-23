package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// Independent releases on two branches have incomparable baseline commits.
// After their merge, each package must exclude only its own shipped history.
func TestPlanKeepsIndependentReleaseWindowsAfterMerge(t *testing.T) {
	r := correctionsRepo(t)
	r.Commit("chore: seed packages")
	base := r.Git("rev-parse", "HEAD")
	r.Git("checkout", "-q", "-b", "core-release")
	r.WriteFile("packages/core/shipped.txt", "core release\n")
	r.Commit("feat(core): shipped core feature")
	r.Git("tag", "-a", "core@1.1.0", "-m", "core release")
	r.WriteFile("packages/core/pending.txt", "core fix\n")
	r.Commit("fix(core): pending core fix")
	r.Git("checkout", "-q", "-b", "utils-release", base)
	r.WriteFile("packages/utils/shipped.txt", "utils release\n")
	r.Commit("feat(utils): shipped utils feature")
	r.Git("tag", "-a", "utils@2.1.0", "-m", "utils release")
	r.WriteFile("packages/utils/pending.txt", "utils fix\n")
	r.Commit("fix(utils): pending utils fix")
	r.Git("merge", "-q", "--no-ff", "core-release", "-m", "chore: merge independent releases")

	plan := r.StatusOK()
	assert.Equal(t, "1.1.0 -> 1.1.1", harness.GraphLine(plan.Events, "core").Str("version"))
	assert.Equal(t, "2.1.0 -> 2.1.1", harness.GraphLine(plan.Events, "utils").Str("version"))
	r.ReleaseOK()
	for _, pkg := range []string{"core", "utils"} {
		entry := changelogOf(t, r, pkg)
		assert.Contains(t, entry, "pending "+pkg+" fix")
		assert.NotContains(t, entry, "shipped "+pkg+" feature")
	}
	assert.ElementsMatch(t, []string{"core@1.1.0", "core@1.1.1", "utils@2.1.0", "utils@2.1.1"}, r.TagList())
	settled := r.StatusOK()
	for _, pkg := range []string{"core", "utils"} {
		assert.Equal(t, "unchanged", harness.GraphLine(settled.Events, pkg).Str("message"), "the merged release has consumed both windows")
	}
}
