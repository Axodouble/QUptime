package checks

import (
	"sync/atomic"
	"testing"
	"time"

	"git.cer.sh/axodouble/quptime/internal/config"
)

func TestAggregatorHysteresisRequiresConsecutiveEvals(t *testing.T) {
	cluster := &config.ClusterConfig{Checks: []config.Check{
		{ID: "c1", Name: "x", Interval: 10 * time.Second},
	}}

	var transitions atomic.Int32
	agg := NewAggregator(cluster, func(_ *config.Check, _, _ State, _ Snapshot) {
		transitions.Add(1)
	})

	// First OK submission — candidate=Up, committed still Unknown.
	agg.Submit("nodeA", Result{CheckID: "c1", OK: true, Timestamp: time.Now()})
	snap, _ := agg.SnapshotFor("c1")
	if snap.State != StateUnknown {
		t.Errorf("after one tick state=%s want unknown", snap.State)
	}
	if transitions.Load() != 0 {
		t.Errorf("transitions=%d after one tick, want 0", transitions.Load())
	}

	// Second OK — hysteresis satisfied, commit Up.
	agg.Submit("nodeA", Result{CheckID: "c1", OK: true, Timestamp: time.Now()})
	snap, _ = agg.SnapshotFor("c1")
	if snap.State != StateUp {
		t.Errorf("after two ticks state=%s want up", snap.State)
	}
	if transitions.Load() != 1 {
		t.Errorf("transitions=%d after commit, want 1", transitions.Load())
	}

	// Single failure — candidate flips to Down, committed stays Up.
	agg.Submit("nodeA", Result{CheckID: "c1", OK: false, Detail: "boom", Timestamp: time.Now()})
	snap, _ = agg.SnapshotFor("c1")
	if snap.State != StateUp {
		t.Errorf("single fail flipped state prematurely: %s", snap.State)
	}

	// Second failure — commit Down.
	agg.Submit("nodeA", Result{CheckID: "c1", OK: false, Detail: "boom", Timestamp: time.Now()})
	snap, _ = agg.SnapshotFor("c1")
	if snap.State != StateDown {
		t.Errorf("after two fails state=%s want down", snap.State)
	}
	if transitions.Load() != 2 {
		t.Errorf("transitions=%d after second commit, want 2", transitions.Load())
	}
}

func TestAggregatorMajorityRule(t *testing.T) {
	cluster := &config.ClusterConfig{Checks: []config.Check{
		{ID: "c1", Name: "x", Interval: 10 * time.Second},
	}}
	agg := NewAggregator(cluster, nil)

	// 2 OK + 1 fail → candidate Up.
	now := time.Now()
	agg.Submit("a", Result{CheckID: "c1", OK: true, Timestamp: now})
	agg.Submit("b", Result{CheckID: "c1", OK: true, Timestamp: now})
	agg.Submit("c", Result{CheckID: "c1", OK: false, Timestamp: now})

	snap, _ := agg.SnapshotFor("c1")
	if snap.OKCount != 2 || snap.NotOK != 1 {
		t.Errorf("counts wrong: %+v", snap)
	}

	// flip the majority
	for i := 0; i < 2; i++ {
		agg.Submit("a", Result{CheckID: "c1", OK: false, Timestamp: time.Now()})
		agg.Submit("b", Result{CheckID: "c1", OK: false, Timestamp: time.Now()})
		agg.Submit("c", Result{CheckID: "c1", OK: false, Timestamp: time.Now()})
	}
	snap, _ = agg.SnapshotFor("c1")
	if snap.State != StateDown {
		t.Errorf("majority-fail did not transition to down: %s", snap.State)
	}
}

func TestAggregatorDropsUnknownChecks(t *testing.T) {
	cluster := &config.ClusterConfig{}
	agg := NewAggregator(cluster, nil)

	agg.Submit("a", Result{CheckID: "ghost", OK: true, Timestamp: time.Now()})
	if _, ok := agg.SnapshotFor("ghost"); ok {
		t.Error("aggregator kept state for unconfigured check")
	}
}

func TestAggregatorIgnoresStaleResults(t *testing.T) {
	cluster := &config.ClusterConfig{Checks: []config.Check{
		{ID: "c1", Name: "x", Interval: 10 * time.Second},
	}}
	agg := NewAggregator(cluster, nil)

	old := time.Now().Add(-10 * time.Minute)
	agg.Submit("a", Result{CheckID: "c1", OK: true, Timestamp: old})

	snap, _ := agg.SnapshotFor("c1")
	if snap.Reports != 0 {
		t.Errorf("stale report counted: %+v", snap)
	}
}
