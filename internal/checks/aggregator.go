package checks

import (
	"sync"
	"time"

	"github.com/jasper/quptime/internal/config"
)

// State is the aggregate verdict on one check.
type State string

const (
	StateUnknown State = "unknown"
	StateUp      State = "up"
	StateDown    State = "down"
)

// HysteresisCount is how many consecutive evaluations a candidate
// state must hold before becoming the new committed state. 2 matches
// the design doc ("down for ≥2 consecutive aggregate evaluations").
const HysteresisCount = 2

// TransitionFn is fired when a check's committed state flips. The
// alert dispatcher is the production consumer.
type TransitionFn func(check *config.Check, from, to State, snap Snapshot)

// Snapshot summarises one check's current aggregate state.
type Snapshot struct {
	CheckID  string
	State    State
	Reports  int   // number of fresh per-node results
	OKCount  int   // number reporting OK
	NotOK    int   // number reporting not-OK
	Detail   string
	UpdateAt time.Time
}

// Aggregator runs on the master only. Other nodes ship their probe
// results to it and it decides the cluster-wide truth.
type Aggregator struct {
	cluster    *config.ClusterConfig
	transition TransitionFn

	mu       sync.Mutex
	perCheck map[string]*checkState
}

type checkState struct {
	// nodeID → most recent result we've received from that node.
	latest map[string]nodeResult

	committed   State // last announced state (held through hysteresis)
	candidate   State // state being considered for promotion
	consecutive int   // ticks the candidate has persisted
}

type nodeResult struct {
	OK     bool
	Detail string
	At     time.Time
}

// NewAggregator returns an empty aggregator. fn may be nil during
// startup; SetTransition can wire it later.
func NewAggregator(cluster *config.ClusterConfig, fn TransitionFn) *Aggregator {
	return &Aggregator{
		cluster:    cluster,
		transition: fn,
		perCheck:   map[string]*checkState{},
	}
}

// SetTransition wires (or rewires) the transition callback.
func (a *Aggregator) SetTransition(fn TransitionFn) {
	a.mu.Lock()
	a.transition = fn
	a.mu.Unlock()
}

// Submit records one node's result for one check and immediately
// re-evaluates that check's aggregate state.
func (a *Aggregator) Submit(nodeID string, r Result) {
	if r.CheckID == "" {
		return
	}
	a.mu.Lock()
	st, ok := a.perCheck[r.CheckID]
	if !ok {
		st = &checkState{
			latest:    map[string]nodeResult{},
			committed: StateUnknown,
			candidate: StateUnknown,
		}
		a.perCheck[r.CheckID] = st
	}
	st.latest[nodeID] = nodeResult{OK: r.OK, Detail: r.Detail, At: r.Timestamp}
	a.mu.Unlock()
	a.evaluate(r.CheckID)
}

// SnapshotAll returns the current aggregate view of every known check.
func (a *Aggregator) SnapshotAll() map[string]Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]Snapshot, len(a.perCheck))
	for id, st := range a.perCheck {
		out[id] = a.snapshotLocked(id, st)
	}
	return out
}

// SnapshotFor returns the aggregate for a single check.
func (a *Aggregator) SnapshotFor(checkID string) (Snapshot, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.perCheck[checkID]
	if !ok {
		return Snapshot{}, false
	}
	return a.snapshotLocked(checkID, st), true
}

func (a *Aggregator) evaluate(checkID string) {
	check := a.lookupCheck(checkID)
	if check == nil {
		// Check was removed from cluster.yaml — drop its state so we
		// don't keep alerting on something the operator deleted.
		a.mu.Lock()
		delete(a.perCheck, checkID)
		a.mu.Unlock()
		return
	}

	a.mu.Lock()
	st, ok := a.perCheck[checkID]
	if !ok {
		a.mu.Unlock()
		return
	}

	freshWindow := freshWindowFor(check.Interval)
	cutoff := time.Now().Add(-freshWindow)

	var ok_, notOK int
	var lastDetail string
	for _, nr := range st.latest {
		if nr.At.Before(cutoff) {
			continue
		}
		if nr.OK {
			ok_++
		} else {
			notOK++
			if lastDetail == "" {
				lastDetail = nr.Detail
			}
		}
	}

	var candidate State
	switch {
	case ok_+notOK == 0:
		candidate = StateUnknown
	case notOK > ok_:
		candidate = StateDown
	default:
		candidate = StateUp
	}

	if candidate == st.candidate {
		st.consecutive++
	} else {
		st.candidate = candidate
		st.consecutive = 1
	}

	var fireFrom, fireTo State
	var fired bool
	if candidate != st.committed && st.consecutive >= HysteresisCount {
		fireFrom = st.committed
		fireTo = candidate
		st.committed = candidate
		fired = true
	}

	snap := a.snapshotLocked(checkID, st)
	fn := a.transition
	a.mu.Unlock()

	if fired && fn != nil {
		fn(check, fireFrom, fireTo, snap)
	}
}

func (a *Aggregator) snapshotLocked(checkID string, st *checkState) Snapshot {
	check := a.lookupCheck(checkID)
	freshWindow := 60 * time.Second
	if check != nil {
		freshWindow = freshWindowFor(check.Interval)
	}
	cutoff := time.Now().Add(-freshWindow)

	var ok_, notOK int
	var detail string
	for _, nr := range st.latest {
		if nr.At.Before(cutoff) {
			continue
		}
		if nr.OK {
			ok_++
		} else {
			notOK++
			if detail == "" {
				detail = nr.Detail
			}
		}
	}
	return Snapshot{
		CheckID:  checkID,
		State:    st.committed,
		Reports:  ok_ + notOK,
		OKCount:  ok_,
		NotOK:    notOK,
		Detail:   detail,
		UpdateAt: time.Now().UTC(),
	}
}

func (a *Aggregator) lookupCheck(id string) *config.Check {
	snap := a.cluster.Snapshot()
	for i := range snap.Checks {
		if snap.Checks[i].ID == id {
			c := snap.Checks[i]
			return &c
		}
	}
	return nil
}

// freshWindowFor returns the staleness threshold for a check given
// its configured interval. Anything older than this is considered too
// stale to count.
func freshWindowFor(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 60 * time.Second
	}
	w := interval * 3
	if w < 30*time.Second {
		w = 30 * time.Second
	}
	return w
}
