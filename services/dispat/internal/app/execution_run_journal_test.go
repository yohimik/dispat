package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

func TestFirstRunJournalRetainsPinnedTLSAndExcludesAnotherOwner(t *testing.T) {
	if runtime.GOOS == "windows" {
		_, err := openFirstRunJournal(t.TempDir(), strings.Repeat("a", 32))
		require.ErrorContains(t, err, "not durable")
		return
	}
	certificate, pin, err := newRunReturnCertificate("https://127.0.0.1:43881")
	require.NoError(t, err)
	leaf, key, err := encodeFirstRunCertificate(certificate)
	require.NoError(t, err)
	run := strings.Repeat("a", 32)
	gitDir := t.TempDir()
	journal, err := openFirstRunJournal(gitDir, run)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, journal.close()) })
	identity := firstRunIdentity{Run: run, Endpoint: "https://127.0.0.1:43881", Listen: "127.0.0.1:43881",
		CertPin: pin, CertificatePEM: leaf, PrivateKeyPEM: key,
		PreparedLocks: map[string]release.PreparedLockIdentity{"control": {
			Run: run, Remote: "origin", LocalTag: "dispat-release-lock-attempt-a",
			Object: strings.Repeat("b", 40),
		}}}
	require.NoError(t, journal.write(identity))
	sourceLock := release.PreparedLockIdentity{Run: run, Remote: "source-remote",
		LocalTag: "dispat-release-lock-attempt-b", Object: strings.Repeat("c", 40)}
	require.NoError(t, journal.recordPreparedLock("source", sourceLock))
	require.ErrorContains(t, journal.recordPreparedLock("source", sourceLock), "already prepared")
	bootstrap := strings.Repeat("d", 40)
	ledger := strings.Repeat("e", 40)
	require.NoError(t, journal.recordBootstrap(bootstrap, "generation"))
	require.NoError(t, journal.recordLedgerRoot(ledger))
	require.NoError(t, journal.expectDeferredTag("core@1.0.0"))
	require.NoError(t, journal.expectDeferredTag("util@1.0.0"))
	require.ErrorContains(t, journal.prepareDeferredTags(map[string]firstRunDeferredTag{
		"core@1.0.0": {Tag: "core@1.0.0", Target: strings.Repeat("1", 40)},
	}), "differs from settled tasks")
	deferred, err := journal.read()
	require.NoError(t, err)
	require.Empty(t, deferred.DeferredTags["core@1.0.0"].Target,
		"an incomplete batch did not publish one native target")
	require.NoError(t, journal.prepareDeferredTags(map[string]firstRunDeferredTag{
		"core@1.0.0": {Tag: "core@1.0.0", Target: strings.Repeat("1", 40)},
		"util@1.0.0": {Tag: "util@1.0.0", Target: strings.Repeat("2", 40)},
	}))
	require.NoError(t, journal.recordDeferredTagObject("core@1.0.0", strings.Repeat("3", 40)))
	require.Error(t, journal.recordBootstrap(bootstrap, "generation"))
	require.Error(t, journal.recordLedgerRoot(ledger))
	_, err = openFirstRunJournal(gitDir, run)
	require.ErrorContains(t, err, "already has an owner")
	_, err = openFirstRunJournal(gitDir, strings.Repeat("c", 32))
	require.ErrorContains(t, err, "already has an owner")
	loaded, err := journal.read()
	require.NoError(t, err)
	require.Equal(t, identity.PreparedLocks["control"], loaded.PreparedLocks["control"])
	require.Equal(t, sourceLock, loaded.PreparedLocks["source"])
	require.Equal(t, bootstrap, loaded.BootstrapOID)
	require.Equal(t, ledger, loaded.LedgerRootOID)
	require.Equal(t, strings.Repeat("3", 40), loaded.DeferredTags["core@1.0.0"].Object)
	require.Equal(t, strings.Repeat("2", 40), loaded.DeferredTags["util@1.0.0"].Target)
	retained, err := loaded.certificate()
	require.NoError(t, err)
	require.Equal(t, certificate.Certificate, retained.Certificate)
	require.NoError(t, journal.close())
	journal, err = openFirstRunJournal(gitDir, run)
	require.NoError(t, err)
	loaded, err = journal.read()
	require.NoError(t, err)
	loaded.CertPin = strings.Repeat("0", 64)
	require.Error(t, journal.write(loaded), "a mismatched key cannot replace the retained identity")
	loaded.CertPin = identity.CertPin
	loaded.BootstrapOID = "short"
	require.ErrorContains(t, journal.write(loaded), "malformed first-node run identity")
	require.NoError(t, journal.close())
	_, err = openFirstRunJournal(gitDir, strings.Repeat("c", 32))
	require.ErrorContains(t, err, "needs reconciliation")
	journ, err := openFirstRunJournal(gitDir, run)
	require.NoError(t, err)
	require.NoError(t, journ.markCompleted())
	require.NoError(t, journ.close())
	next, err := openFirstRunJournal(gitDir, strings.Repeat("c", 32))
	require.NoError(t, err)
	require.NoError(t, next.close())
}

func TestFirstRunJournalRefusesReadableIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows journal creation refuses without directory durability")
	}
	certificate, pin, err := newRunReturnCertificate("https://127.0.0.1:43881")
	require.NoError(t, err)
	leaf, key, err := encodeFirstRunCertificate(certificate)
	require.NoError(t, err)
	journal, err := openFirstRunJournal(t.TempDir(), strings.Repeat("b", 32))
	require.NoError(t, err)
	defer journal.close()
	require.NoError(t, journal.write(firstRunIdentity{Run: journal.run,
		Endpoint: "https://127.0.0.1:43881", Listen: "127.0.0.1:43881", CertPin: pin,
		CertificatePEM: leaf, PrivateKeyPEM: key}))
	require.NoError(t, os.Chmod(filepath.Join(journal.dir, "identity.json"), 0o644))
	_, err = journal.read()
	require.ErrorContains(t, err, "unsafe first-node identity file")
}
