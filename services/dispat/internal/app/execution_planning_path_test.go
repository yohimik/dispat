package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRebasePlanningPathAcceptsRelativeRepositoryRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repository")
	private := filepath.Join(base, "private")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(private, 0o755))
	t.Chdir(root)

	mapped, err := rebasePlanningPath(root, ".", private)
	require.NoError(t, err)
	require.Equal(t, private, mapped)

	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(outside, 0o755))
	_, err = rebasePlanningPath(outside, ".", private)
	require.ErrorContains(t, err, "escapes its repository")
}
