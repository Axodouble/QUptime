package config

import (
	"fmt"
	"testing"
)

func TestQuorumSize(t *testing.T) {
	cases := []struct {
		peers int
		want  int
	}{
		{0, 1},
		{1, 1},
		{2, 2},
		{3, 2},
		{4, 3},
		{5, 3},
		{7, 4},
	}
	for _, tc := range cases {
		c := &ClusterConfig{}
		for i := 0; i < tc.peers; i++ {
			c.Peers = append(c.Peers, PeerInfo{NodeID: fmt.Sprintf("n%d", i)})
		}
		if got := c.QuorumSize(); got != tc.want {
			t.Errorf("peers=%d: QuorumSize=%d want %d", tc.peers, got, tc.want)
		}
	}
}

func TestClusterMutateBumpsVersion(t *testing.T) {
	t.Setenv("QUPTIME_DIR", t.TempDir())
	c := &ClusterConfig{}

	err := c.Mutate("nodeA", func(cc *ClusterConfig) error {
		cc.Checks = append(cc.Checks, Check{ID: "1", Name: "x"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 {
		t.Errorf("Version=%d want 1", c.Version)
	}
	if c.UpdatedBy != "nodeA" {
		t.Errorf("UpdatedBy=%q want nodeA", c.UpdatedBy)
	}

	err = c.Mutate("nodeB", func(cc *ClusterConfig) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 2 {
		t.Errorf("Version=%d want 2 after second mutate", c.Version)
	}
}

func TestClusterReplaceGatesOnVersion(t *testing.T) {
	t.Setenv("QUPTIME_DIR", t.TempDir())
	cur := &ClusterConfig{Version: 5, Checks: []Check{{ID: "old"}}}

	if applied, _ := cur.Replace(&ClusterConfig{Version: 4}); applied {
		t.Error("older version was applied")
	}
	if applied, _ := cur.Replace(&ClusterConfig{Version: 5}); applied {
		t.Error("equal version was applied")
	}
	applied, err := cur.Replace(&ClusterConfig{
		Version: 6,
		Checks:  []Check{{ID: "new"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Error("newer version was not applied")
	}
	if cur.Version != 6 || len(cur.Checks) != 1 || cur.Checks[0].ID != "new" {
		t.Errorf("after replace: %+v", cur)
	}
}

func TestClusterSnapshotIsCopy(t *testing.T) {
	c := &ClusterConfig{Checks: []Check{{ID: "a"}}}
	snap := c.Snapshot()
	snap.Checks[0].ID = "b"
	if c.Checks[0].ID != "a" {
		t.Error("snapshot mutation leaked back to original")
	}
}

func TestFindAlert(t *testing.T) {
	c := &ClusterConfig{Alerts: []Alert{
		{ID: "id-1", Name: "primary", Type: AlertSMTP},
		{ID: "id-2", Name: "secondary", Type: AlertDiscord},
	}}
	if a := c.FindAlert("primary"); a == nil || a.Type != AlertSMTP {
		t.Errorf("by name: %+v", a)
	}
	if a := c.FindAlert("id-2"); a == nil || a.Type != AlertDiscord {
		t.Errorf("by id: %+v", a)
	}
	if a := c.FindAlert("ghost"); a != nil {
		t.Errorf("expected nil for missing, got %+v", a)
	}
}
