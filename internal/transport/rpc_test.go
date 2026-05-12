package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jasper/quptime/internal/crypto"
	"github.com/jasper/quptime/internal/trust"
)

// testNode bundles everything one side of the handshake needs.
type testNode struct {
	id     string
	dir    string
	assets *TLSAssets
	fp     string
}

// makeNode builds keys + cert + an empty trust store rooted at dir.
// After every disk-touching trust operation the caller must ensure
// QUPTIME_DIR points back at this node's dir.
func makeNode(t *testing.T, dir, id string) *testNode {
	t.Helper()
	t.Setenv("QUPTIME_DIR", dir)
	priv, err := crypto.GenerateKeyPair(id)
	if err != nil {
		t.Fatal(err)
	}
	certPEM, err := crypto.LoadCertPEM()
	if err != nil {
		t.Fatal(err)
	}
	fp, err := crypto.FingerprintFromCertPEM(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	store, err := trust.Load()
	if err != nil {
		t.Fatal(err)
	}
	return &testNode{
		id:     id,
		dir:    dir,
		assets: &TLSAssets{Cert: certPEM, Key: priv, Trust: store},
		fp:     fp,
	}
}

func (n *testNode) trust(t *testing.T, other *testNode, addr string) {
	t.Helper()
	t.Setenv("QUPTIME_DIR", n.dir)
	if err := n.assets.Trust.Add(trust.Entry{
		NodeID: other.id, Address: addr, Fingerprint: other.fp,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRPCRoundtrip(t *testing.T) {
	a := makeNode(t, t.TempDir(), "node-a")
	b := makeNode(t, t.TempDir(), "node-b")

	// pre-pick a free port; brief race window is acceptable for tests
	tmpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := tmpLn.Addr().String()
	tmpLn.Close()

	a.trust(t, b, addr)
	b.trust(t, a, addr)

	srv := NewServer(a.assets)
	srv.Handle("Echo", func(_ context.Context, peer string, payload json.RawMessage) (any, error) {
		var s string
		if err := json.Unmarshal(payload, &s); err != nil {
			return nil, err
		}
		if peer != b.id {
			return nil, errors.New("unexpected peer id: " + peer)
		}
		return s + " ack", nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, addr) }()
	defer srv.Stop()

	if !waitForDial(addr, 2*time.Second) {
		t.Fatal("server did not start listening in time")
	}

	cli := NewClient(b.assets)
	defer cli.Close()

	callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
	defer callCancel()
	var got string
	if err := cli.Call(callCtx, a.id, addr, "Echo", "hello", &got); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != "hello ack" {
		t.Errorf("got %q want %q", got, "hello ack")
	}
}

func TestRPCUnknownMethod(t *testing.T) {
	a := makeNode(t, t.TempDir(), "node-a")
	b := makeNode(t, t.TempDir(), "node-b")

	tmpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := tmpLn.Addr().String()
	tmpLn.Close()

	a.trust(t, b, addr)
	b.trust(t, a, addr)

	srv := NewServer(a.assets)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, addr)
	defer srv.Stop()
	if !waitForDial(addr, 2*time.Second) {
		t.Fatal("server not up")
	}

	cli := NewClient(b.assets)
	defer cli.Close()
	err := cli.Call(ctx, a.id, addr, "DoesNotExist", nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
}

func TestRPCRejectsUntrustedPeer(t *testing.T) {
	a := makeNode(t, t.TempDir(), "node-a")
	b := makeNode(t, t.TempDir(), "node-b")

	tmpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := tmpLn.Addr().String()
	tmpLn.Close()

	// Deliberately omit b.trust(...) on the server side: b is unknown to a.
	t.Setenv("QUPTIME_DIR", b.dir)
	_ = b.assets.Trust.Add(trust.Entry{NodeID: a.id, Address: addr, Fingerprint: a.fp})

	srv := NewServer(a.assets)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx, addr)
	defer srv.Stop()
	if !waitForDial(addr, 2*time.Second) {
		t.Fatal("server not up")
	}

	cli := NewClient(b.assets)
	defer cli.Close()

	callCtx, callCancel := context.WithTimeout(ctx, 2*time.Second)
	defer callCancel()
	if err := cli.Call(callCtx, a.id, addr, "Ping", nil, nil); err == nil {
		t.Error("untrusted client was admitted")
	}
}

// waitForDial polls a TCP listener until it accepts a plain TCP
// connection, signalling that Serve has begun listening.
func waitForDial(addr string, max time.Duration) bool {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
