// Package replicate keeps cluster.yaml consistent across every node.
//
// Every mutation goes through the elected master. Followers either
//
//   - forward a CLI-originated proposal to the master via the
//     ProposeMutation RPC, then trust the master's broadcast to
//     update their local copy; or
//   - observe via heartbeat that the master holds a higher version
//     and pull the new snapshot on their own.
//
// The master applies, bumps the monotonic Version counter, and
// pushes the new snapshot to every peer. Peers accept the snapshot
// only when its Version is strictly greater than their local Version.
//
// The package owns no transport or quorum logic — it consumes those
// through small interfaces so it stays easy to test.
package replicate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jasper/quptime/internal/config"
	"github.com/jasper/quptime/internal/transport"
)

// MasterView is the minimum the replicator needs from the quorum
// manager: who the current master is and what address to reach them
// at. Implemented by *quorum.Manager.
type MasterView interface {
	Master() string
	IsMaster() bool
	HasQuorum() bool
}

// Replicator drives mutation routing and broadcast.
type Replicator struct {
	selfID  string
	cluster *config.ClusterConfig
	client  *transport.Client
	master  MasterView
}

// New constructs a replicator. selfID is this node's NodeID.
func New(selfID string, cluster *config.ClusterConfig, client *transport.Client, master MasterView) *Replicator {
	return &Replicator{
		selfID:  selfID,
		cluster: cluster,
		client:  client,
		master:  master,
	}
}

// LocalMutate is called by the CLI/control plane on this node to
// effect a config change. It routes the mutation to the master and
// returns the new version on success.
func (r *Replicator) LocalMutate(ctx context.Context, kind transport.MutationKind, payload any) (uint64, error) {
	if !r.master.HasQuorum() {
		return 0, errors.New("no quorum: refusing mutation")
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal payload: %w", err)
	}

	if r.master.IsMaster() {
		// apply directly, then broadcast.
		newVer, err := r.applyLocally(kind, raw)
		if err != nil {
			return 0, err
		}
		r.broadcast(ctx)
		return newVer, nil
	}

	// follower: ship to master.
	masterID := r.master.Master()
	if masterID == "" {
		return 0, errors.New("master unknown")
	}
	addr := r.addressOf(masterID)
	if addr == "" {
		return 0, fmt.Errorf("master %s has no advertise address", masterID)
	}
	req := transport.ProposeMutationRequest{
		FromNodeID: r.selfID,
		Kind:       kind,
		Payload:    raw,
	}
	var resp transport.ProposeMutationResponse
	if err := r.client.Call(ctx, masterID, addr, transport.MethodProposeMutation, req, &resp); err != nil {
		return 0, fmt.Errorf("propose to master: %w", err)
	}
	if resp.Error != "" {
		return 0, errors.New(resp.Error)
	}
	return resp.NewVersion, nil
}

// HandleProposeMutation is the inbound RPC handler. Only meaningful
// when this node is the master; followers reject the proposal so the
// caller knows to re-route.
func (r *Replicator) HandleProposeMutation(ctx context.Context, req transport.ProposeMutationRequest) transport.ProposeMutationResponse {
	if !r.master.IsMaster() {
		return transport.ProposeMutationResponse{Error: "not master"}
	}
	if !r.master.HasQuorum() {
		return transport.ProposeMutationResponse{Error: "no quorum"}
	}
	newVer, err := r.applyLocally(req.Kind, req.Payload)
	if err != nil {
		return transport.ProposeMutationResponse{Error: err.Error()}
	}
	go r.broadcast(context.Background())
	return transport.ProposeMutationResponse{NewVersion: newVer}
}

// HandleApplyClusterCfg is the inbound RPC handler for a master
// broadcast. Applies if the version is strictly newer than ours.
func (r *Replicator) HandleApplyClusterCfg(req transport.ApplyClusterCfgRequest) transport.ApplyClusterCfgResponse {
	applied := false
	if req.Config != nil {
		ok, err := r.cluster.Replace(req.Config)
		if err == nil && ok {
			applied = true
		}
	}
	return transport.ApplyClusterCfgResponse{
		Applied: applied,
		Version: r.cluster.Snapshot().Version,
	}
}

// HandleGetClusterCfg returns a snapshot of our cluster.yaml. Used by
// followers performing catch-up against the master.
func (r *Replicator) HandleGetClusterCfg() transport.GetClusterCfgResponse {
	return transport.GetClusterCfgResponse{Config: r.cluster.Snapshot()}
}

// PullFrom fetches the cluster config from a peer and applies it.
// Wired to the quorum manager's VersionObserver.
func (r *Replicator) PullFrom(ctx context.Context, peerID, addr string) error {
	if peerID == "" || addr == "" {
		return errors.New("pull: empty peer")
	}
	var resp transport.GetClusterCfgResponse
	if err := r.client.Call(ctx, peerID, addr, transport.MethodGetClusterCfg, transport.GetClusterCfgRequest{}, &resp); err != nil {
		return fmt.Errorf("pull from %s: %w", peerID, err)
	}
	if resp.Config == nil {
		return errors.New("pull: empty config")
	}
	_, err := r.cluster.Replace(resp.Config)
	return err
}

// broadcast pushes the current cluster config to all peers. Called by
// master after a successful mutation.
func (r *Replicator) broadcast(ctx context.Context) {
	snap := r.cluster.Snapshot()
	for _, p := range snap.Peers {
		if p.NodeID == r.selfID || p.NodeID == "" || p.Advertise == "" {
			continue
		}
		peerID, addr := p.NodeID, p.Advertise
		go func(peerID, addr string) {
			callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			req := transport.ApplyClusterCfgRequest{Config: snap}
			var resp transport.ApplyClusterCfgResponse
			_ = r.client.Call(callCtx, peerID, addr, transport.MethodApplyClusterCfg, req, &resp)
		}(peerID, addr)
	}
}

func (r *Replicator) addressOf(nodeID string) string {
	for _, p := range r.cluster.Snapshot().Peers {
		if p.NodeID == nodeID {
			return p.Advertise
		}
	}
	return ""
}

// applyLocally decodes the mutation payload and applies it. Only the
// master should call this directly. Returns the new version.
func (r *Replicator) applyLocally(kind transport.MutationKind, payload json.RawMessage) (uint64, error) {
	apply := func(c *config.ClusterConfig) error {
		switch kind {
		case transport.MutationAddCheck:
			var ch config.Check
			if err := json.Unmarshal(payload, &ch); err != nil {
				return fmt.Errorf("decode check: %w", err)
			}
			if ch.ID == "" || ch.Name == "" {
				return errors.New("check needs id and name")
			}
			for i, existing := range c.Checks {
				if existing.ID == ch.ID || existing.Name == ch.Name {
					c.Checks[i] = ch
					return nil
				}
			}
			c.Checks = append(c.Checks, ch)
			return nil

		case transport.MutationRemoveCheck:
			var target string
			if err := json.Unmarshal(payload, &target); err != nil {
				return fmt.Errorf("decode target: %w", err)
			}
			for i, existing := range c.Checks {
				if existing.ID == target || existing.Name == target {
					c.Checks = append(c.Checks[:i], c.Checks[i+1:]...)
					return nil
				}
			}
			return fmt.Errorf("no such check %q", target)

		case transport.MutationAddAlert:
			var a config.Alert
			if err := json.Unmarshal(payload, &a); err != nil {
				return fmt.Errorf("decode alert: %w", err)
			}
			if a.ID == "" || a.Name == "" {
				return errors.New("alert needs id and name")
			}
			for i, existing := range c.Alerts {
				if existing.ID == a.ID || existing.Name == a.Name {
					c.Alerts[i] = a
					return nil
				}
			}
			c.Alerts = append(c.Alerts, a)
			return nil

		case transport.MutationRemoveAlert:
			var target string
			if err := json.Unmarshal(payload, &target); err != nil {
				return fmt.Errorf("decode target: %w", err)
			}
			for i, existing := range c.Alerts {
				if existing.ID == target || existing.Name == target {
					c.Alerts = append(c.Alerts[:i], c.Alerts[i+1:]...)
					return nil
				}
			}
			return fmt.Errorf("no such alert %q", target)

		case transport.MutationAddPeer:
			var p config.PeerInfo
			if err := json.Unmarshal(payload, &p); err != nil {
				return fmt.Errorf("decode peer: %w", err)
			}
			if p.NodeID == "" {
				return errors.New("peer needs node_id")
			}
			for i, existing := range c.Peers {
				if existing.NodeID == p.NodeID {
					c.Peers[i] = p
					return nil
				}
			}
			c.Peers = append(c.Peers, p)
			return nil

		case transport.MutationRemovePeer:
			var target string
			if err := json.Unmarshal(payload, &target); err != nil {
				return fmt.Errorf("decode target: %w", err)
			}
			for i, existing := range c.Peers {
				if existing.NodeID == target {
					c.Peers = append(c.Peers[:i], c.Peers[i+1:]...)
					return nil
				}
			}
			return fmt.Errorf("no such peer %q", target)

		default:
			return fmt.Errorf("unknown mutation kind %q", kind)
		}
	}

	if err := r.cluster.Mutate(r.selfID, apply); err != nil {
		return 0, err
	}
	return r.cluster.Snapshot().Version, nil
}
