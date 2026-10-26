package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

func TestAppPreparesOneDurableReturnIdentityBeforeListener(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("first-node durable journal refuses Windows before remote effects")
	}
	root := t.TempDir()
	fixtureGit(t, root, "init", "-q")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	endpoint := "https://" + listener.Addr().String()
	require.NoError(t, listener.Close())
	a := New(root, &config.File{Execution: &config.ExecutionConfig{
		Workers: []config.ExecutionWorkerConfig{{Name: "peer"}},
	}}, zerolog.Nop())
	a.startExecutionRun()
	require.NoError(t, a.prepareFirstRunIdentity(t.Context(), endpoint, ""))
	defer a.finishFirstRunIdentity(true)
	prepared, err := a.returnJournal.read()
	require.NoError(t, err)
	require.Equal(t, a.runID, prepared.Run)
	require.Equal(t, endpoint, prepared.Endpoint)
	channel, err := a.openRunReturnChannel(endpoint, "")
	require.NoError(t, err)
	require.Equal(t, prepared.CertPin, channel.certPin)
	require.NoError(t, channel.close())
	_, err = os.Stat(a.returnJournal.dir)
	require.NoError(t, err)
}

func TestAppPersistsReturnListenOverrideForSameGenerationRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("first-node durable journal refuses Windows before remote effects")
	}
	root := t.TempDir()
	fixtureGit(t, root, "init", "-q")
	a := New(root, &config.File{Execution: &config.ExecutionConfig{
		Workers: []config.ExecutionWorkerConfig{{Name: "peer"}},
	}}, zerolog.Nop())
	a.startExecutionRun()
	require.NoError(t, a.prepareFirstRunIdentity(t.Context(), "https://return.example:43881", "127.0.0.1:43881"))
	identity, err := a.returnJournal.read()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:43881", identity.Listen)
	identity.Listen = "other-host"
	require.ErrorContains(t, a.returnJournal.write(identity), "malformed first-node return listen address")
	require.NoError(t, a.finishFirstRunIdentity(true))
}

func TestExpiredRetainedReturnIdentityCanBeCleanedButCannotListen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("first-node durable journal refuses Windows before remote effects")
	}
	const endpoint = "https://127.0.0.1:43881"
	root := t.TempDir()
	fixtureGit(t, root, "init", "-q")
	settings := &config.File{Execution: &config.ExecutionConfig{
		Workers: []config.ExecutionWorkerConfig{{Name: "peer"}},
	}}
	a := New(root, settings, zerolog.Nop())
	a.startExecutionRun()
	certificate, _, err := newRunReturnCertificate(endpoint)
	require.NoError(t, err)
	parsed, err := x509.ParseCertificate(certificate.Certificate[0])
	require.NoError(t, err)
	expired := *parsed
	expired.NotBefore = time.Now().Add(-72 * time.Hour)
	expired.NotAfter = time.Now().Add(-48 * time.Hour)
	der, err := x509.CreateCertificate(rand.Reader, &expired, &expired,
		parsed.PublicKey, certificate.PrivateKey)
	require.NoError(t, err)
	certificate = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: certificate.PrivateKey}
	leaf, key, err := encodeFirstRunCertificate(certificate)
	require.NoError(t, err)
	gitDir, err := a.git.GitDir(t.Context())
	require.NoError(t, err)
	journal, err := openFirstRunJournal(gitDir, a.runID)
	require.NoError(t, err)
	sum := sha256.Sum256(der)
	require.NoError(t, journal.write(firstRunIdentity{Run: a.runID, Endpoint: endpoint,
		Listen: "127.0.0.1:43881", CertPin: hex.EncodeToString(sum[:]),
		CertificatePEM: leaf, PrivateKeyPEM: key}))
	a.returnJournal = journal
	_, err = a.openRunReturnChannel(endpoint, "")
	require.ErrorContains(t, err, "outside its validity period")
	require.NoError(t, journal.close())
	a.returnJournal = nil
	restarted := New(root, settings, zerolog.Nop())
	require.NoError(t, restarted.reconcilePriorFirstRun(t.Context()),
		"read-only cleanup must not require a live-valid certificate")
}

func TestFirstNodeCanReopenExactActiveIdentityWithoutRotatingTLS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("first-node durable journal refuses Windows before remote effects")
	}
	root := t.TempDir()
	fixtureGit(t, root, "init", "-q")
	a := New(root, &config.File{Execution: &config.ExecutionConfig{
		Workers: []config.ExecutionWorkerConfig{{Name: "peer"}},
	}}, zerolog.Nop())
	a.startExecutionRun()
	require.NoError(t, a.prepareFirstRunIdentity(t.Context(), "https://127.0.0.1:43881", ""))
	original, err := a.returnJournal.read()
	require.NoError(t, err)
	require.NoError(t, a.returnJournal.close())
	a.returnJournal = nil
	recovered, identity, err := reopenActiveFirstRunIdentity(t.Context(), a.git)
	require.NoError(t, err)
	require.NotNil(t, recovered)
	require.Equal(t, original.Run, identity.Run)
	require.Equal(t, original.CertPin, identity.CertPin)
	require.Equal(t, original.PrivateKeyPEM, identity.PrivateKeyPEM)
	require.NoError(t, recovered.markCompleted())
	require.NoError(t, recovered.close())
}

func TestFirstNodeRetainsActiveIdentityWhileItsPhysicalTaskLeaseIsHeld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("first-node durable journal refuses Windows before remote effects")
	}
	root := t.TempDir()
	fixtureGit(t, root, "init", "-q")
	a := New(root, &config.File{Execution: &config.ExecutionConfig{
		Workers: []config.ExecutionWorkerConfig{{Name: "peer"}},
	}}, zerolog.Nop())
	a.startExecutionRun()
	require.NoError(t, a.prepareFirstRunIdentity(t.Context(), "https://127.0.0.1:43881", ""))
	slotRoot := filepath.Join(t.TempDir(), "private-slots")
	require.NoError(t, os.Mkdir(slotRoot, 0o700))
	slots, err := execution.OpenNodeSlotAuthority(slotRoot, a.sender.Node, 1)
	require.NoError(t, err)
	a.localNodeSlots = slots
	_, ok, err := slots.TryReserve(t.Context(), execution.NodeSlotIntent{
		Run: a.runID, Task: "core:test", Attempt: 1, Weight: 1,
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.ErrorContains(t, a.finishFirstRunIdentity(true), "retains task core:test capacity")
	reopened, _, err := reopenActiveFirstRunIdentity(t.Context(), a.git)
	require.NoError(t, err)
	require.NotNil(t, reopened)
	require.NoError(t, reopened.close())
}
