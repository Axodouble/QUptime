// Package crypto handles the RSA key material every node uses for
// mutual TLS authentication and for the trust-store fingerprint pinning.
//
// Keys are RSA-3072 (NIST 112-bit security, well within the safe band
// through ~2030). They live PEM-encoded under <data_dir>/keys/.
package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"github.com/jasper/quptime/internal/config"
)

// KeySize is the RSA modulus size used by qu.
const KeySize = 3072

// GenerateKeyPair creates a fresh RSA keypair and writes the private,
// public, and self-signed certificate to the standard paths.
// It refuses to overwrite existing keys.
func GenerateKeyPair(commonName string) (*rsa.PrivateKey, error) {
	if _, err := os.Stat(config.PrivateKeyPath()); err == nil {
		return nil, errors.New("key material already exists; refusing to overwrite")
	}
	if err := config.EnsureDataDir(); err != nil {
		return nil, err
	}
	priv, err := rsa.GenerateKey(rand.Reader, KeySize)
	if err != nil {
		return nil, fmt.Errorf("generate rsa key: %w", err)
	}
	if err := writePEM(config.PrivateKeyPath(), "RSA PRIVATE KEY",
		x509.MarshalPKCS1PrivateKey(priv), 0o600); err != nil {
		return nil, err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	if err := writePEM(config.PublicKeyPath(), "PUBLIC KEY", pubDER, 0o644); err != nil {
		return nil, err
	}
	certDER, err := buildSelfSignedCert(priv, commonName)
	if err != nil {
		return nil, err
	}
	if err := writePEM(config.CertFilePath(), "CERTIFICATE", certDER, 0o644); err != nil {
		return nil, err
	}
	return priv, nil
}

// LoadPrivateKey reads the on-disk RSA private key.
func LoadPrivateKey() (*rsa.PrivateKey, error) {
	raw, err := os.ReadFile(config.PrivateKeyPath())
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("private key: no PEM block")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private key: not RSA")
		}
		return rk, nil
	default:
		return nil, fmt.Errorf("private key: unexpected PEM type %q", block.Type)
	}
}

// LoadCertPEM reads the self-signed cert file (used as the TLS leaf).
func LoadCertPEM() ([]byte, error) {
	return os.ReadFile(config.CertFilePath())
}

func writePEM(path, blockType string, der []byte, perm os.FileMode) error {
	encoded := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	return config.AtomicWrite(path, encoded, perm)
}
