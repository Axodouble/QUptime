package crypto

import (
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

func TestGenerateAndLoadKeyPair(t *testing.T) {
	t.Setenv("QUPTIME_DIR", t.TempDir())

	priv, err := GenerateKeyPair("node-1")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	if priv.N.BitLen() < KeySize-8 {
		t.Errorf("key too small: %d bits", priv.N.BitLen())
	}

	// Refusing to overwrite existing material is part of the contract.
	if _, err := GenerateKeyPair("node-1"); err == nil {
		t.Error("expected error on re-generate")
	}

	loaded, err := LoadPrivateKey()
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	if loaded.N.Cmp(priv.N) != 0 {
		t.Error("loaded key modulus differs from generated")
	}
}

func TestFingerprintDeterminismAndUniqueness(t *testing.T) {
	t.Setenv("QUPTIME_DIR", t.TempDir())
	priv, err := GenerateKeyPair("node-x")
	if err != nil {
		t.Fatal(err)
	}

	certPEM, err := LoadCertPEM()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("no PEM block in cert")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	fp1 := Fingerprint(cert)
	fp2 := Fingerprint(cert)
	if fp1 != fp2 {
		t.Errorf("non-deterministic: %s vs %s", fp1, fp2)
	}
	if !strings.HasPrefix(fp1, "sha256:") {
		t.Errorf("missing sha256: prefix: %s", fp1)
	}

	pemFP, err := FingerprintFromCertPEM(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if pemFP != fp1 {
		t.Errorf("PEM-derived fingerprint differs: %s vs %s", pemFP, fp1)
	}

	// Now generate a fresh cert from the same key — fingerprint must
	// match (SPKI is identical).
	derSame, err := buildSelfSignedCert(priv, "node-x")
	if err != nil {
		t.Fatal(err)
	}
	certSame, _ := x509.ParseCertificate(derSame)
	if Fingerprint(certSame) != fp1 {
		t.Error("fingerprint changed across cert regen with same key")
	}
}

func TestFingerprintDiffersAcrossKeys(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	t.Setenv("QUPTIME_DIR", dirA)
	if _, err := GenerateKeyPair("a"); err != nil {
		t.Fatal(err)
	}
	pemA, _ := LoadCertPEM()
	fpA, _ := FingerprintFromCertPEM(pemA)

	t.Setenv("QUPTIME_DIR", dirB)
	if _, err := GenerateKeyPair("b"); err != nil {
		t.Fatal(err)
	}
	pemB, _ := LoadCertPEM()
	fpB, _ := FingerprintFromCertPEM(pemB)

	if fpA == fpB {
		t.Error("two independent keys produced the same fingerprint")
	}
}

func TestFingerprintFromCertPEMRejectsGarbage(t *testing.T) {
	if _, err := FingerprintFromCertPEM([]byte("not a pem")); err == nil {
		t.Error("expected error on non-PEM input")
	}
}
