package transport

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"time"
)

// fingerprintOf computes the SHA-256 SPKI fingerprint of a parsed
// certificate using the same encoding as the crypto package
// (sha256:hex). Duplicated here to keep the transport package
// dependency-light at the call site.
func fingerprintOf(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PeerCertSample is the result of a TOFU probe: the operator inspects
// the fingerprint and decides whether to trust it.
type PeerCertSample struct {
	Cert        *x509.Certificate
	CertPEM     []byte
	Fingerprint string
}

// FetchPeerCert opens an mTLS connection to addr with no trust
// pinning, captures the peer's certificate, and closes the connection.
// The caller must show the fingerprint to the operator before adding
// it to the trust store.
//
// This is the *only* place the trust store is bypassed. After the
// TOFU exchange, the regular ClientConfig path applies for all future
// traffic to that peer.
func FetchPeerCert(ctx context.Context, assets *TLSAssets, addr string) (*PeerCertSample, error) {
	cfg, err := assets.InsecureBootstrapConfig()
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	d := tls.Dialer{Config: cfg, NetDialer: &net.Dialer{}}
	raw, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer raw.Close()

	tc, ok := raw.(*tls.Conn)
	if !ok {
		return nil, errors.New("dial returned non-tls conn")
	}
	state := tc.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, errors.New("peer presented no certificate")
	}
	leaf := state.PeerCertificates[0]
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	return &PeerCertSample{
		Cert:        leaf,
		CertPEM:     pemBytes,
		Fingerprint: fingerprintOf(leaf),
	}, nil
}
