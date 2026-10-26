package app

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestReturnListenerServesOnlyThePinnedRunCertificate(t *testing.T) {
	cert, pin, err := newRunReturnCertificate("https://127.0.0.1:8443")
	if err != nil {
		t.Fatal(err)
	}
	_, cancel := context.WithCancel(t.Context())
	defer cancel()
	server, err := startRunReturnServer("127.0.0.1:0", cert, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if err := server.close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // The signed exact leaf pin is checked below.
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) != 1 {
				return fmt.Errorf("unexpected certificate chain")
			}
			sum := sha256.Sum256(state.PeerCertificates[0].Raw)
			if hex.EncodeToString(sum[:]) != pin {
				return fmt.Errorf("return certificate does not match the signed pin")
			}
			return nil
		},
	}}}
	response, err := client.Get("https://" + server.address)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d, want %d", response.StatusCode, http.StatusNoContent)
	}
	// The command context may end while signed Cancel/Result/Ack delivery is
	// still pending. Only the run owner closes this listener after settlement.
	cancel()
	client.CloseIdleConnections()
	response, err = client.Get("https://" + server.address)
	if err != nil {
		t.Fatalf("return listener closed before detached settlement: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status after command cancellation %d, want %d", response.StatusCode, http.StatusNoContent)
	}
}
