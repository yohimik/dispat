package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnboundRunUnlocksOnlyExplicitlyInertAdmission(t *testing.T) {
	for _, state := range []string{firstRunAdmissionInertState, firstRunAdmissionArmedState, ""} {
		t.Run(state, func(t *testing.T) {
			journal := admissionJournalFixture(t, state)
			app := &App{runID: journal.run, returnJournal: journal, choreographyRetained: true}
			var unlocked int
			cleanup := &releaseCleanup{BeforeUnlock: app.requireChoreographyQuiescent,
				unlock: func() error { unlocked++; return nil }}
			err := cleanup.finish()
			if state == firstRunAdmissionInertState {
				require.NoError(t, err)
				require.Equal(t, 1, unlocked)
				return
			}
			require.ErrorContains(t, err, "no bound broker")
			require.Zero(t, unlocked, "missing or armed admission cannot prove no accepted effects")
		})
	}
}

func TestArmedAdmissionSurvivesReopenBeforeBrokerExposure(t *testing.T) {
	journal := admissionJournalFixture(t, firstRunAdmissionInertState)
	require.NoError(t, journal.armChoreographyAdmission())
	gitDir := filepath.Dir(filepath.Dir(filepath.Dir(journal.dir)))
	require.NoError(t, journal.close())
	reopened, err := openFirstRunJournal(gitDir, journal.run)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.close()) })
	identity, err := reopened.read()
	require.NoError(t, err)
	require.Equal(t, firstRunAdmissionArmedState, identity.AdmissionState)
	app := &App{runID: reopened.run, returnJournal: reopened, choreographyRetained: true}
	require.ErrorContains(t, app.requireChoreographyQuiescent(), "no bound broker")
	require.NoError(t, reopened.armChoreographyAdmission(), "repeating the durable fence does not clear it")
}

func admissionJournalFixture(t *testing.T, state string) *firstRunJournal {
	t.Helper()
	certificate, pin, err := newRunReturnCertificate("https://127.0.0.1:43881")
	require.NoError(t, err)
	leaf, key, err := encodeFirstRunCertificate(certificate)
	require.NoError(t, err)
	journal, err := openFirstRunJournal(t.TempDir(), strings.Repeat("a", 32))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, journal.close()) })
	require.NoError(t, journal.write(firstRunIdentity{Run: journal.run,
		Endpoint: "https://127.0.0.1:43881", Listen: "127.0.0.1:43881", CertPin: pin,
		CertificatePEM: leaf, PrivateKeyPEM: key, AdmissionState: state,
		BootstrapOID: strings.Repeat("b", 40), Generation: "generation",
		LedgerRootOID: strings.Repeat("c", 40)}))
	return journal
}
