package app

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"testing"
)

func TestRunReturnCertificatePinsFreshHTTPSIdentity(t *testing.T) {
	first, pin, err := newRunReturnCertificate("https://127.0.0.1:8443")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(first.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(first.Certificate[0])
	if pin != hex.EncodeToString(sum[:]) {
		t.Fatalf("signed pin %q does not name the generated certificate", pin)
	}
	_, nextPin, err := newRunReturnCertificate("https://127.0.0.1:8443")
	if err != nil {
		t.Fatal(err)
	}
	if nextPin == pin {
		t.Fatal("two runs shared a return-channel certificate")
	}
}
