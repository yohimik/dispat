package app

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	public "github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/config"
)

func TestExplicitLocalConcurrencyUsesPersistentSharedSlotsWithoutReturnIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	plain := New(home, &config.File{}, zerolog.Nop())
	require.NoError(t, plain.prepareExplicitLocalSlots())
	require.Nil(t, plain.localNodeSlots)
	require.Empty(t, plain.localSlotRunID)

	settings := &config.File{Execution: &public.ExecutionConfig{
		Name: "same-local-node", Concurrency: public.Int(2),
	}}
	first := New(home, settings, zerolog.Nop())
	err := first.prepareExplicitLocalSlots()
	if runtime.GOOS == "windows" {
		require.Error(t, err, "explicit durable node capacity must refuse on Windows")
		return
	}
	require.NoError(t, err)
	require.NotNil(t, first.localNodeSlots)
	require.Len(t, first.localSlotRunID, 32)
	require.Nil(t, first.returnJournal)
	root, err := sharedNodeSlotRoot()
	require.NoError(t, err)
	require.DirExists(t, root)
	second := New(home, settings, zerolog.Nop())
	require.NoError(t, second.prepareExplicitLocalSlots())
	require.Equal(t, first.localNodeSlots.AuthorityID(), second.localNodeSlots.AuthorityID())
	require.NotEqual(t, first.localSlotRunID, second.localSlotRunID)
	require.Nil(t, second.returnJournal)
}
