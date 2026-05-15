package quorum

import (
	"testing"
	"time"

	"git.cer.sh/axodouble/quptime/internal/config"
	"git.cer.sh/axodouble/quptime/internal/transport"
)

func threeNode(self string) (*config.ClusterConfig, *Manager) {
	cluster := &config.ClusterConfig{Peers: []config.PeerInfo{
		{NodeID: "a"}, {NodeID: "b"}, {NodeID: "c"},
	}}
	return cluster, New(self, cluster, nil)
}

func TestSoloNodeElectsItself(t *testing.T) {
	cluster := &config.ClusterConfig{}
	m := New("only", cluster, nil)
	m.markLive("only")
	m.recomputeMaster()
	if m.Master() != "only" {
		t.Errorf("Master=%q want %q", m.Master(), "only")
	}
	if !m.HasQuorum() {
		t.Error("solo node should have quorum")
	}
	if m.Term() != 1 {
		t.Errorf("Term=%d want 1 after first election", m.Term())
	}
}

func TestThreeNodeElectsLowestNodeID(t *testing.T) {
	_, m := threeNode("b")
	m.markLive("a")
	m.markLive("b")
	m.markLive("c")
	m.recomputeMaster()
	if got := m.Master(); got != "a" {
		t.Errorf("Master=%q want a", got)
	}
	if !m.HasQuorum() {
		t.Error("expected quorum with 3 live of 3")
	}
}

func TestNoQuorumClearsMaster(t *testing.T) {
	_, m := threeNode("b")
	m.markLive("b")
	m.recomputeMaster()
	if m.Master() != "" {
		t.Errorf("Master=%q want empty (no quorum)", m.Master())
	}
	if m.HasQuorum() {
		t.Error("1 of 3 live should not be quorum")
	}
}

func TestTermBumpsOnMasterChange(t *testing.T) {
	_, m := threeNode("b")
	m.markLive("a")
	m.markLive("b")
	m.recomputeMaster()
	termBefore := m.Term()
	masterBefore := m.Master()
	if masterBefore != "a" {
		t.Fatalf("expected initial master a, got %q", masterBefore)
	}

	// "a" goes dead — we and "c" join up.
	m.mu.Lock()
	delete(m.lastSeen, "a")
	m.mu.Unlock()
	m.markLive("c")
	m.recomputeMaster()
	if m.Master() != "b" {
		t.Errorf("after a-fail Master=%q want b", m.Master())
	}
	if m.Term() <= termBefore {
		t.Errorf("Term did not bump: before=%d after=%d", termBefore, m.Term())
	}
}

func TestHandleHeartbeatMarksSenderLive(t *testing.T) {
	cluster, m := threeNode("a")
	_ = cluster
	resp := m.HandleHeartbeat(transport.HeartbeatRequest{
		FromNodeID: "b",
		Term:       7,
		MasterID:   "a",
		Version:    3,
	})
	if resp.NodeID != "a" {
		t.Errorf("response NodeID=%q want a", resp.NodeID)
	}
	if _, ok := m.Liveness()["b"]; !ok {
		t.Error("sender was not recorded live")
	}
}

func TestDeadAfterEvictsStaleLiveness(t *testing.T) {
	_, m := threeNode("a")
	m.deadAfter = 50 * time.Millisecond
	m.markLive("a")
	m.markLive("b")
	m.markLive("c")
	m.recomputeMaster()
	if m.Master() != "a" {
		t.Fatal("expected initial master a")
	}

	// Wait past the dead-after window — only self remains live.
	time.Sleep(120 * time.Millisecond)
	m.markLive("a")
	m.recomputeMaster()
	if m.Master() != "" {
		t.Errorf("expected no master after peers timed out, got %q", m.Master())
	}
}

// heartbeatLoop simulates the production heartbeat cadence — calling
// markLive for the given peers more frequently than deadAfter, so a
// peer that's "live throughout" never has its liveSince reset by the
// dead-after gap heuristic. It returns when the context's deadline
// hits.
func heartbeatLoop(t *testing.T, m *Manager, dur time.Duration, peers ...string) {
	t.Helper()
	deadline := time.Now().Add(dur)
	interval := m.deadAfter / 4
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	for time.Now().Before(deadline) {
		for _, p := range peers {
			m.markLive(p)
		}
		m.recomputeMaster()
		time.Sleep(interval)
	}
}

func TestReturningLowerIDWaitsForCooldown(t *testing.T) {
	_, m := threeNode("b")
	m.deadAfter = 80 * time.Millisecond
	m.masterCooldown = 200 * time.Millisecond

	// Bootstrap: all three live, "a" elected.
	m.markLive("a")
	m.markLive("b")
	m.markLive("c")
	m.recomputeMaster()
	if m.Master() != "a" {
		t.Fatalf("initial master=%q want a", m.Master())
	}

	// "a" drops — only b/c heartbeat. Long enough to age a out and let
	// b take over.
	heartbeatLoop(t, m, 120*time.Millisecond, "b", "c")
	if m.Master() != "b" {
		t.Fatalf("after a-drop master=%q want b", m.Master())
	}

	// "a" returns. Verify b stays master for less than the cooldown.
	heartbeatLoop(t, m, 120*time.Millisecond, "a", "b", "c")
	if m.Master() != "b" {
		t.Errorf("mid-cooldown master=%q want b", m.Master())
	}

	// Past the cooldown, a reclaims master.
	heartbeatLoop(t, m, 120*time.Millisecond, "a", "b", "c")
	if m.Master() != "a" {
		t.Errorf("after cooldown master=%q want a", m.Master())
	}
}

func TestCooldownResetsOnFlap(t *testing.T) {
	_, m := threeNode("b")
	m.deadAfter = 80 * time.Millisecond
	m.masterCooldown = 200 * time.Millisecond

	m.markLive("a")
	m.markLive("b")
	m.markLive("c")
	m.recomputeMaster()

	// a drops, b becomes master.
	heartbeatLoop(t, m, 120*time.Millisecond, "b", "c")
	if m.Master() != "b" {
		t.Fatalf("master=%q want b", m.Master())
	}

	// a returns briefly, then drops again before cooldown elapses.
	heartbeatLoop(t, m, 100*time.Millisecond, "a", "b", "c")
	if m.Master() != "b" {
		t.Fatalf("during first cooldown master=%q want b", m.Master())
	}
	heartbeatLoop(t, m, 120*time.Millisecond, "b", "c") // a ages out again
	if m.Master() != "b" {
		t.Fatalf("after a-reflap master=%q want b", m.Master())
	}

	// a returns for the second time — cooldown restarts here.
	// Wait less than a full cooldown — b should still be master.
	heartbeatLoop(t, m, 100*time.Millisecond, "a", "b", "c")
	if m.Master() != "b" {
		t.Errorf("partway through fresh cooldown master=%q want b", m.Master())
	}

	// Past the full fresh cooldown, a takes over.
	heartbeatLoop(t, m, 150*time.Millisecond, "a", "b", "c")
	if m.Master() != "a" {
		t.Errorf("after fresh cooldown master=%q want a", m.Master())
	}
}

func TestNewMasterAfterQuorumLossIgnoresCooldown(t *testing.T) {
	_, m := threeNode("b")
	m.deadAfter = 50 * time.Millisecond
	m.masterCooldown = 1 * time.Hour // would block election if applied

	// Bootstrap into no-master state by letting all peers age out.
	m.markLive("a")
	m.markLive("b")
	m.markLive("c")
	m.recomputeMaster()
	time.Sleep(80 * time.Millisecond)
	m.markLive("b")
	m.recomputeMaster()
	if m.Master() != "" {
		t.Fatalf("master=%q want empty (quorum lost)", m.Master())
	}

	// Quorum regained — incumbent is empty, election must be immediate.
	m.markLive("a")
	m.markLive("b")
	m.recomputeMaster()
	if m.Master() != "a" {
		t.Errorf("post-recovery master=%q want a (no cooldown when empty)", m.Master())
	}
}

func TestVersionObserverFiresOnHigherVersion(t *testing.T) {
	cluster := &config.ClusterConfig{Version: 2}
	m := New("a", cluster, nil)

	var notified struct {
		peerID  string
		peerVer uint64
		count   int
	}
	m.SetVersionObserver(func(peerID, _ string, peerVer uint64) {
		notified.peerID = peerID
		notified.peerVer = peerVer
		notified.count++
	})

	// Seed the address for "b" via an incoming heartbeat — the
	// observer no-ops without one to avoid log spam.
	m.HandleHeartbeat(transport.HeartbeatRequest{
		FromNodeID: "b", Advertise: "10.0.0.2:9901", Version: 2,
	})

	m.maybeNotifyVersion("b", 5)
	if notified.count != 1 || notified.peerID != "b" || notified.peerVer != 5 {
		t.Errorf("expected observer fired with b=5, got %+v", notified)
	}

	m.maybeNotifyVersion("b", 1)
	if notified.count != 1 {
		t.Errorf("observer fired for stale version, count=%d", notified.count)
	}
}

func TestVersionObserverSkippedWithoutAddress(t *testing.T) {
	cluster := &config.ClusterConfig{Version: 0}
	m := New("a", cluster, nil)

	var fired int
	m.SetVersionObserver(func(_, _ string, _ uint64) { fired++ })

	// Peer "c" has never sent a heartbeat — no recorded address.
	m.maybeNotifyVersion("c", 99)
	if fired != 0 {
		t.Errorf("observer fired without a known address: %d", fired)
	}
}
