// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package changelog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// TestNoteEntryReplacesTheChangelogAtomically: the note rewrites the whole
// file, and it runs in the merge recovery, after the packages are already
// published. Record goes through the atomic replace for exactly that reason
// and the note has the same reason, so it takes the same path.
//
// The refusal to follow a symlink is what makes the difference observable: the
// atomic replace renames a new file over the name and will not write through a
// link out of the package folder, where a plain write would follow it and
// overwrite whatever it points at.
func TestNoteEntryReplacesTheChangelogAtomically(t *testing.T) {
	dir := t.TempDir()
	rel := testRelease(dir, ccme.Version{Major: 1, Minor: 2})
	rel.Pkg.Changelog = model.ChangelogSpec{Enabled: true}
	w := &FileWriter{Now: func() time.Time { return testDate }}
	require.NoError(t, w.Record(context.Background(), rel))

	path := filepath.Join(dir, "CHANGELOG.md")
	recorded, err := os.ReadFile(path)
	require.NoError(t, err)

	// The same entry, reached through a link that leaves the package folder.
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	require.NoError(t, os.WriteFile(outside, recorded, 0o644))
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Symlink(outside, path))

	_, noted, err := NoteEntry(rel, "a note the recovery wanted to leave")
	require.Error(t, err, "a changelog that is a link out of the package is not rewritten")
	assert.False(t, noted)

	after, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, string(recorded), string(after), "the file the link pointed at is untouched")
}
