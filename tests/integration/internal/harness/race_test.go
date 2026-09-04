package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckRaceReports(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, CheckRaceReports(""), "an unconfigured normal test run has no race-report contract")
	require.NoError(t, CheckRaceReports(dir))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "unrelated"), []byte("ignored"), 0o600))
	require.NoError(t, CheckRaceReports(dir))

	report := filepath.Join(dir, "race.42")
	require.NoError(t, os.WriteFile(report, []byte("WARNING: DATA RACE\nchild stack"), 0o600))
	err := CheckRaceReports(dir)
	require.Error(t, err)
	require.ErrorContains(t, err, "race detector reported in a black-box subprocess")
	require.ErrorContains(t, err, report)
	require.ErrorContains(t, err, "WARNING: DATA RACE")
}
