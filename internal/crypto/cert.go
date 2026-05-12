package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// CertValidity is how long self-signed certs are valid for. We use a
// long horizon because cert rotation is operator-driven, not automatic.
const CertValidity = 10 * 365 * 24 * time.Hour

// buildSelfSignedCert produces an X.509 certificate signed by `priv`
// itself, using the given common name. Returns the DER bytes.
func buildSelfSignedCert(priv *rsa.PrivateKey, commonName string) ([]byte, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"quptime"}},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(CertValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	return x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
}

// Fingerprint computes the SHA-256 fingerprint of an X.509 certificate's
// SubjectPublicKeyInfo (the same hash used by `openssl x509 -pubkey -noout
// | openssl dgst -sha256`). Returns the lowercase hex digest with a
// "sha256:" prefix to match SSH conventions.
func Fingerprint(cert *x509.Certificate) string {
	return FingerprintFromSPKI(cert.RawSubjectPublicKeyInfo)
}

// FingerprintFromSPKI is the underlying helper.
func FingerprintFromSPKI(spki []byte) string {
	sum := sha256.Sum256(spki)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FingerprintFromCertPEM parses a PEM-encoded certificate and returns
// its fingerprint.
func FingerprintFromCertPEM(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", errors.New("cert: no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse cert: %w", err)
	}
	return Fingerprint(cert), nil
}

// FingerprintFromPubKeyPEM parses a public-key PEM and returns its
// fingerprint over the same SPKI bytes.
func FingerprintFromPubKeyPEM(pubPEM []byte) (string, error) {
	block, _ := pem.Decode(pubPEM)
	if block == nil {
		return "", errors.New("pubkey: no PEM block")
	}
	return FingerprintFromSPKI(block.Bytes), nil
}
