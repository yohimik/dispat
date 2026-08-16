package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// TestAppendIncludeDirsWarnsOnAMissingPath: a commit.include path that names
// nothing on disk is skipped with W227 — staged silence would cost the
// release commit its artifact on every release until a human noticed.
func TestAppendIncludeDirsWarnsOnAMissingPath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "present.txt"), []byte("x"), 0o644))
	var logs bytes.Buffer
	a := New(root, &config.File{}, zerolog.New(&logs))

	dirs := a.appendIncludeDirs(nil, []string{"present.txt", "ghost.lock"})

	require.Len(t, dirs, 1, "the present path is staged")
	assert.Contains(t, dirs[0], "present.txt")
	assert.Contains(t, logs.String(), plan.CodeCommitIncludeMissing)
	assert.Contains(t, logs.String(), "ghost.lock", "the warning names the path")
}
