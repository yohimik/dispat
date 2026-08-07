package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

func TestEnvKeySanitises(t *testing.T) {
	assert.Equal(t, "CORE", EnvKey("core"))
	assert.Equal(t, "_ACME_UI", EnvKey("@acme/ui"))
	assert.Equal(t, "CORE_UTILS", EnvKey("core-utils"))
}

func TestExportedCommit(t *testing.T) {
	rel := &Release{Pkg: &model.Package{Name: "core"}}
	assert.Empty(t, rel.ExportedCommit(), "no export, no pinned commit")

	rel.Outputs = []Output{{Name: PackageCommitExportPrefix + EnvKey("core"), Value: " abc123\n"}}
	assert.Equal(t, "abc123", rel.ExportedCommit(), "the exported hash, trimmed")

	other := &Release{Pkg: &model.Package{Name: "ui"},
		Outputs: []Output{{Name: "PACKAGE_CORE", Value: "abc123"}}}
	assert.Empty(t, other.ExportedCommit(), "only the package's own key counts")

	assert.Empty(t, (&Release{}).ExportedCommit(), "nil package is tolerated")
}
