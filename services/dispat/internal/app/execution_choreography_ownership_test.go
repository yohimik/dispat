package app

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

func TestChoreographyFirstCheckBindsExactProtectedRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("distributed first-node journal refuses Windows until durable directory replacement is available")
	}
	endpoint := "https://127.0.0.1:43881"
	certificate, pin, err := newRunReturnCertificate(endpoint)
	require.NoError(t, err)
	leaf, key, err := encodeFirstRunCertificate(certificate)
	require.NoError(t, err)
	run := strings.Repeat("a", 32)
	bootstrap, ledger := strings.Repeat("b", 40), strings.Repeat("c", 40)
	journal, err := openFirstRunJournal(t.TempDir(), run)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, journal.close()) })
	require.NoError(t, journal.write(firstRunIdentity{Run: run, Endpoint: endpoint, Listen: "127.0.0.1:43881",
		CertPin: pin, CertificatePEM: leaf, PrivateKeyPEM: key}))
	require.NoError(t, journal.recordBootstrap(bootstrap, "generation"))
	require.NoError(t, journal.recordLedgerRoot(ledger))
	coordinator := &execution.Coordinator{Run: run, Generation: "generation",
		BootstrapOID: bootstrap, ReturnEndpoint: endpoint, ReturnCertSHA256: pin}
	a := &App{returnJournal: journal}
	verify, err := a.choreographyFirstCheck(coordinator, ledger)
	require.NoError(t, err)
	require.NoError(t, verify(t.Context()))
	coordinator.ReturnCertSHA256 = strings.Repeat("0", 64)
	require.ErrorContains(t, verify(t.Context()), "differs")
	coordinator.ReturnCertSHA256 = pin
	require.NoError(t, journal.close())
	require.ErrorContains(t, verify(t.Context()), "no current owner")
}
