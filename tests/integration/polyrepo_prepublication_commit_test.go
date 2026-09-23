package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// A native preparatory commit exports its exact source revision to the
// enclosing release; that admitted revision must survive the publication fence.
func TestPolyrepoAdmitsANativePreparatoryCommitBeforePublication(t *testing.T) {
	f := finalPolyrepo(t)
	r := f.control
	cfg := polyrepoFile()
	cfg["logLevel"] = "debug"
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["flow"].(map[string]any)["beforePublish"] = []string{"prepare"}
	cfg["scripts"].(map[string]any)["prepare"] = []string{
		"printf 'prepared\\n' > generated.txt",
		r.DispatCommand("commit", "--package", "core"),
	}
	cfg["scripts"].(map[string]any)["publish"] = []string{"printf 'published\\n' >> ../../../../publish-count"}
	writePolyrepoJSON(t, r, "dispat.json", cfg)
	r.Commit("chore: prepare a native source commit before publication")
	before := r.Git("-C", "sources/lib", "rev-parse", "HEAD")
	res := r.ReleaseOK("--log-level", "debug")
	after := r.Git("-C", "sources/lib", "rev-parse", "HEAD")
	assert.NotEqual(t, before, after)
	assert.Contains(t, res.Stdout, "admitted nested source revision into fleet snapshot")
	assert.Equal(t, after, r.Git("-C", "sources/lib", "rev-parse", "core@0.1.0^{commit}"))
	assert.Equal(t, "prepared", r.Git("-C", "sources/lib", "show", "core@0.1.0:packages/core/generated.txt"))
	assert.Equal(t, "published\n", covReadFile(t, r.Path("publish-count")))
	assert.False(t, harness.IsCodePresent(res.Events, "E330"), "the source revision was explicitly admitted")
}
