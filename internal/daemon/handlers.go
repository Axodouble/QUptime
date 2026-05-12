package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jasper/quptime/internal/checks"
	"github.com/jasper/quptime/internal/crypto"
	"github.com/jasper/quptime/internal/transport"
	"github.com/jasper/quptime/internal/trust"
)

// registerHandlers wires every inter-node RPC method that the daemon
// understands onto the transport server. Each method delegates to the
// owning subsystem (quorum, replicator, etc.) so this file stays a
// thin dispatch table.
func (d *Daemon) registerHandlers() {
	d.server.Handle(transport.MethodPing, func(_ context.Context, _ string, _ json.RawMessage) (any, error) {
		return transport.PingResponse{NodeID: d.node.NodeID, Now: time.Now().UTC()}, nil
	})

	d.server.Handle(transport.MethodWhoAmI, func(_ context.Context, _ string, _ json.RawMessage) (any, error) {
		fp, err := crypto.FingerprintFromCertPEM(d.assets.Cert)
		if err != nil {
			return nil, err
		}
		return transport.WhoAmIResponse{
			NodeID:      d.node.NodeID,
			Advertise:   d.node.AdvertiseAddr(),
			Fingerprint: fp,
			CertPEM:     string(d.assets.Cert),
		}, nil
	})

	d.server.Handle(transport.MethodJoin, func(_ context.Context, _ string, raw json.RawMessage) (any, error) {
		var req transport.JoinRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return transport.JoinResponse{Error: err.Error()}, nil
		}
		fp, err := crypto.FingerprintFromCertPEM([]byte(req.CertPEM))
		if err != nil {
			return transport.JoinResponse{Error: "parse cert: " + err.Error()}, nil
		}
		if fp != req.Fingerprint {
			return transport.JoinResponse{Error: "fingerprint mismatch"}, nil
		}
		// Outbound join (the proposing node already accepted our cert
		// out of band). Symmetric trust is required for mTLS to work,
		// so we accept the join automatically. Operators who need
		// stricter onboarding can disable the listener and use the
		// CLI flow exclusively.
		if err := d.trust.Add(trust.Entry{
			NodeID:      req.NodeID,
			Address:     req.Advertise,
			Fingerprint: req.Fingerprint,
			CertPEM:     req.CertPEM,
		}); err != nil {
			return transport.JoinResponse{Error: err.Error()}, nil
		}
		return transport.JoinResponse{Accepted: true}, nil
	})

	d.server.Handle(transport.MethodHeartbeat, func(_ context.Context, _ string, raw json.RawMessage) (any, error) {
		var req transport.HeartbeatRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		return d.quorum.HandleHeartbeat(req), nil
	})

	d.server.Handle(transport.MethodGetClusterCfg, func(_ context.Context, _ string, _ json.RawMessage) (any, error) {
		return d.replicator.HandleGetClusterCfg(), nil
	})

	d.server.Handle(transport.MethodApplyClusterCfg, func(_ context.Context, _ string, raw json.RawMessage) (any, error) {
		var req transport.ApplyClusterCfgRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		return d.replicator.HandleApplyClusterCfg(req), nil
	})

	d.server.Handle(transport.MethodProposeMutation, func(ctx context.Context, _ string, raw json.RawMessage) (any, error) {
		var req transport.ProposeMutationRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		return d.replicator.HandleProposeMutation(ctx, req), nil
	})

	d.server.Handle(transport.MethodReportResult, func(_ context.Context, _ string, raw json.RawMessage) (any, error) {
		var req transport.ReportResultRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		res := checks.Result{
			CheckID:   req.CheckID,
			OK:        req.OK,
			Detail:    req.Detail,
			Latency:   time.Duration(req.LatencyMS) * time.Millisecond,
			Timestamp: req.At,
		}
		d.aggregator.Submit(req.FromNodeID, res)
		return transport.ReportResultResponse{}, nil
	})

	d.server.Handle(transport.MethodStatus, func(_ context.Context, _ string, _ json.RawMessage) (any, error) {
		return d.buildStatus(), nil
	})
}

// buildStatus is shared by both the inter-node Status RPC handler and
// the local control plane's "status" command.
func (d *Daemon) buildStatus() transport.StatusResponse {
	snap := d.cluster.Snapshot()
	liveness := d.quorum.Liveness()
	live := map[string]bool{}
	for _, id := range d.quorum.LiveSet() {
		live[id] = true
	}

	out := transport.StatusResponse{
		NodeID:     d.node.NodeID,
		Term:       d.quorum.Term(),
		MasterID:   d.quorum.Master(),
		Version:    snap.Version,
		HasQuorum:  d.quorum.HasQuorum(),
		QuorumSize: snap.QuorumSize(),
	}
	for _, p := range snap.Peers {
		out.Peers = append(out.Peers, transport.PeerLiveness{
			NodeID:    p.NodeID,
			Advertise: p.Advertise,
			Live:      live[p.NodeID],
			LastSeen:  liveness[p.NodeID],
		})
	}
	for _, c := range snap.Checks {
		cs := transport.CheckSnapshot{CheckID: c.ID, Name: c.Name, State: "unknown"}
		if agg, ok := d.aggregator.SnapshotFor(c.ID); ok {
			cs.State = string(agg.State)
			cs.OKCount = agg.OKCount
			cs.Total = agg.Reports
			cs.Detail = agg.Detail
		}
		out.Checks = append(out.Checks, cs)
	}
	return out
}
