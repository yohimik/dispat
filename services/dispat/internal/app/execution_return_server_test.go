package app

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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
	server, err := startRunReturnServer("127.0.0.1:0", cert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || !r.TLS.HandshakeComplete {
			http.Error(w, "missing server TLS proof", http.StatusUpgradeRequired)
			return
		}
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

func TestReturnTLSProofRejectsPlainAndUnfinishedConnections(t *testing.T) {
	handler, connContext := runReturnTLSProofAdapter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || !r.TLS.HandshakeComplete {
			w.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "https://example.invalid/dispat/choreography/claim", nil)
	request.TLS = nil // TinyGo omits this field even when the URL says HTTPS.
	request.Header.Set("Forwarded", "proto=https")
	request.Header.Set("X-Forwarded-Proto", "https")
	plainClient, plainServer := net.Pipe()
	defer plainClient.Close()
	defer plainServer.Close()
	request = request.WithContext(connContext(request.Context(), plainServer))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUpgradeRequired {
		t.Fatalf("plain connection with HTTPS headers returned %d", response.Code)
	}

	unfinishedClient, unfinishedServer := net.Pipe()
	defer unfinishedClient.Close()
	defer unfinishedServer.Close()
	serverTLS := tls.Server(unfinishedServer, &tls.Config{MinVersion: tls.VersionTLS13})
	defer serverTLS.Close()
	request = request.WithContext(connContext(request.Context(), serverTLS))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUpgradeRequired {
		t.Fatalf("unfinished TLS handshake returned %d", response.Code)
	}
}

func TestReturnTLSProofRestoresOmittedRequestStateFromAcceptedConnection(t *testing.T) {
	verified, connContext := runReturnTLSProofAdapter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || !r.TLS.HandshakeComplete {
			w.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.TLS = nil // Model the pinned TinyGo server's omitted request metadata.
		verified.ServeHTTP(w, r)
	}))
	server.Config.ConnContext = connContext
	server.StartTLS()
	defer server.Close()
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("omitted TLS request state returned %d", response.StatusCode)
	}
}
