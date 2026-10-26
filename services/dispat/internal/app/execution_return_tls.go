package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"net"
	"net/url"
	"time"
)

// newRunReturnCertificate creates a fresh TLS identity. The
// signed probe pins its leaf hash, so peers need no shared CA or server key.
func newRunReturnCertificate(endpoint string) (tls.Certificate, string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return tls.Certificate{}, runReturnAbsentCertificatePinText, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, runReturnAbsentCertificatePinText, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(runReturnSerialBaseValue), runReturnSerialSizeBits))
	if err != nil {
		return tls.Certificate{}, runReturnAbsentCertificatePinText, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: runReturnCertificateCommonName},
		NotBefore:             now.Add(-runReturnCertificateClockSkewMinutes * time.Minute),
		NotAfter:              now.AddDate(runReturnCertificateLifetimeYears, runReturnCertificateExtraMonths, runReturnCertificateExtraDays),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: runReturnCertificateValidConstraintsState,
	}
	if host := u.Hostname(); net.ParseIP(host) != nil {
		template.IPAddresses = append(template.IPAddresses, net.ParseIP(host))
	} else {
		template.DNSNames = append(template.DNSNames, host)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, runReturnAbsentCertificatePinText, err
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	sum := sha256.Sum256(der)
	return cert, hex.EncodeToString(sum[:]), nil
}
