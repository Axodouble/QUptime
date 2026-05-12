// Package trust is the local fingerprint trust store. Every inbound
// or outbound mTLS connection must present a peer cert whose
// fingerprint is recorded here, otherwise it is refused.
//
// The trust store is NOT replicated automatically by the cluster
// config: each node's operator confirms each new peer's fingerprint
// out of band (the "trust on first use" prompt). After confirmation,
// the cluster replication layer is free to propagate the peer's
// public material to other nodes — but it cannot grant trust on
// behalf of a node that has not yet trusted it.
package trust

import (
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jasper/quptime/internal/config"
	"github.com/jasper/quptime/internal/crypto"
)

// Entry is one trusted peer.
type Entry struct {
	NodeID       string    `yaml:"node_id"`
	Address      string    `yaml:"address"`
	Fingerprint  string    `yaml:"fingerprint"`
	PublicKeyPEM string    `yaml:"public_key_pem"`
	CertPEM      string    `yaml:"cert_pem,omitempty"`
	AddedAt      time.Time `yaml:"added_at"`
}

// Store is the persistent trust list with an in-memory cache.
type Store struct {
	mu      sync.RWMutex
	entries map[string]Entry // keyed by NodeID
}

// Load reads trust.yaml. A missing file yields an empty store.
func Load() (*Store, error) {
	s := &Store{entries: map[string]Entry{}}
	raw, err := os.ReadFile(config.TrustFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	wrap := struct {
		Entries []Entry `yaml:"entries"`
	}{}
	if err := yaml.Unmarshal(raw, &wrap); err != nil {
		return nil, fmt.Errorf("parse trust.yaml: %w", err)
	}
	for _, e := range wrap.Entries {
		s.entries[e.NodeID] = e
	}
	return s, nil
}

// Save writes trust.yaml atomically.
func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		list = append(list, e)
	}
	out, err := yaml.Marshal(struct {
		Entries []Entry `yaml:"entries"`
	}{Entries: list})
	if err != nil {
		return err
	}
	return config.AtomicWrite(config.TrustFilePath(), out, 0o600)
}

// Add inserts or replaces a trust entry by NodeID.
func (s *Store) Add(e Entry) error {
	if e.NodeID == "" || e.Fingerprint == "" {
		return errors.New("trust entry needs node_id and fingerprint")
	}
	if e.AddedAt.IsZero() {
		e.AddedAt = time.Now().UTC()
	}
	s.mu.Lock()
	s.entries[e.NodeID] = e
	s.mu.Unlock()
	return s.Save()
}

// Remove drops the entry for nodeID. Returns true if anything was
// actually removed.
func (s *Store) Remove(nodeID string) (bool, error) {
	s.mu.Lock()
	_, ok := s.entries[nodeID]
	if ok {
		delete(s.entries, nodeID)
	}
	s.mu.Unlock()
	if !ok {
		return false, nil
	}
	return true, s.Save()
}

// List returns a copy of all entries.
func (s *Store) List() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	return out
}

// Get returns the entry for the given NodeID and whether it was found.
func (s *Store) Get(nodeID string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[nodeID]
	return e, ok
}

// LookupByFingerprint finds the entry matching the given fingerprint
// regardless of NodeID. Useful when validating incoming TLS handshakes
// where the cert's CommonName carries the NodeID claim.
func (s *Store) LookupByFingerprint(fp string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.entries {
		if e.Fingerprint == fp {
			return e, true
		}
	}
	return Entry{}, false
}

// VerifyPeerCert is the tls.Config VerifyPeerCertificate callback. It
// rejects any cert whose fingerprint isn't in the trust store.
func (s *Store) VerifyPeerCert(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	if len(rawCerts) == 0 {
		return errors.New("peer presented no certificate")
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("parse peer cert: %w", err)
	}
	fp := crypto.Fingerprint(cert)
	if _, ok := s.LookupByFingerprint(fp); !ok {
		return fmt.Errorf("peer cert fingerprint %s not in trust store", fp)
	}
	return nil
}
