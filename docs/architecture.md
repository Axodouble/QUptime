# Architecture

This page is the long-form companion to the diagram in the top-level
README. Read it if you need to reason about partitions, recovery,
upgrade ordering, or the consistency guarantees of `qu`.

## Components

A running `qu serve` is one process containing five long-lived
goroutines plus the listeners:

| Component       | Package                  | Role                                                                     |
| --------------- | ------------------------ | ------------------------------------------------------------------------ |
| Transport       | `internal/transport`     | mTLS listener + dialer, length-prefixed JSON-RPC framing.                |
| Quorum manager  | `internal/quorum`        | 1 Hz heartbeats, liveness tracking, deterministic master election.       |
| Replicator      | `internal/replicate`     | Master-routed mutations, version-gated broadcast and pull.               |
| Scheduler       | `internal/checks`        | One goroutine per check; runs HTTP/TCP/ICMP probes on each node.         |
| Aggregator      | `internal/checks`        | Master-only. Folds per-node probe results into a cluster-wide verdict.   |
| Alert dispatch  | `internal/alerts`        | Master-only. Renders templates and ships SMTP / Discord notifications.   |
| Control socket  | `internal/daemon`        | Local-only unix socket; the CLI and TUI talk to the daemon through it.   |

Every node runs every component. Whether the master-only ones actually
*do* anything depends on the result of master election.

## Trust and transport

Inter-node traffic is TLS 1.3 with mutual authentication. There is **no
central CA**. Each node generates a self-signed RSA cert at `qu init`
and the SPKI fingerprint of that cert is what other nodes pin against.

Two layers gate access:

1. **TLS layer** accepts any client cert. This avoids a chicken-and-egg
   during bootstrap — a brand-new node has no entry in anyone's trust
   store yet, so a strict TLS check would refuse the very first
   handshake.
2. **RPC dispatcher** rejects every method except `Join` for callers
   whose presented fingerprint is not in `trust.yaml`. So an untrusted
   peer can knock on the door but cannot ask questions.

`Join` itself is gated by the **cluster secret** — a pre-shared base64
string generated at `qu init` on the first node. Without it, an
attacker who can reach `:9901` cannot enrol themselves into the
cluster.

The local CLI talks to the daemon over a unix socket with `0600`
permissions; filesystem ACLs are the only authentication and no TLS is
used on that channel.

## The replicated state machine

`cluster.yaml` is the single replicated source of truth. It holds three
editable lists — `peers`, `checks`, `alerts` — plus three
server-controlled fields:

```yaml
version: 7                 # monotonically increasing
updated_at: 2026-05-15T...
updated_by: <node-id>      # master that committed this version
peers:  [...]
checks: [...]
alerts: [...]
```

### How mutations flow

1. The CLI (or the manual-edit watcher; see below) issues a mutation
   on the local daemon's control socket.
2. The daemon's replicator looks at the current quorum view:
   - If there is no quorum, the mutation fails loudly with
     `no quorum: refusing mutation`.
   - If this node is the master, apply locally and broadcast.
   - Otherwise, ship the mutation to the master via the
     `ProposeMutation` RPC and wait for the result.
3. The master holds the cluster lock, applies the mutation, bumps
   `version`, writes `cluster.yaml` atomically, and broadcasts the new
   snapshot to every peer via `ApplyClusterCfg`.
4. Each follower's `Replace` accepts the snapshot **only if**
   `incoming.Version > local.Version`. Older or equal versions are
   dropped silently.

The mutation kinds are enumerated in `internal/transport/messages.go`:
`add_check`, `remove_check`, `add_alert`, `remove_alert`, `add_peer`,
`remove_peer`, `replace_config`.

### Manual edits to `cluster.yaml`

Operators can `sudoedit /etc/quptime/cluster.yaml` on any node. Every
2 seconds the daemon hashes the file. When the on-disk hash diverges
from the last hash the daemon wrote, the new content is parsed and
forwarded to the master as a `replace_config` mutation. So a hand-edit
on a follower still ends up on the master, version-bumped, and
broadcast everywhere.

If the parse fails (invalid YAML), the daemon logs and pins the bad
hash so it doesn't loop. The operator's next valid save unblocks it.

## Quorum and master election

Every node sends a heartbeat to every peer once per second. A peer is
**live** if a heartbeat (sent or received) was observed within the
last 4 seconds — comfortably more than three missed beats so a one-tick
blip does not unseat the master.

**Quorum** is met when `len(live_peers) >= floor(N/2) + 1` where `N`
is the total peer count in `cluster.yaml`. Below quorum, the cluster
refuses every mutation; existing checks continue probing locally but no
state transitions are committed (the master is the only one who
aggregates, and there is no master).

**Master election** is deterministic with no negotiation step: among
the live members, the master is the one with the lexicographically
smallest `NodeID`. Every node that observes the same live set picks the
same master — so there is no split-brain window even during a partial
partition.

The `term` integer in `qu status` is bumped every time the elected
master changes (including transitions to and from "no master"). Use it
to spot flappy clusters.

## Catch-up when a node reconnects

This is the scenario most people ask about: node C is offline, the
master commits config version 7, node C comes back online. What
happens?

1. Node C's tick loop fires heartbeats every second regardless of its
   previous state. There is no backoff, no give-up.
2. Each heartbeat carries the sender's `Version`. Each response carries
   the responder's `Version`.
3. The first time C sees a peer reporting a higher version than its
   own, the version-observer fires and calls
   `replicator.PullFrom(peerID, addr)`.
4. `PullFrom` does a `GetClusterCfg` RPC against that peer and feeds
   the snapshot through `Replace`, which writes `cluster.yaml`
   atomically and refreshes the on-disk hash so the manual-edit
   watcher doesn't re-fire.
5. Within ~1 heartbeat C is byte-for-byte identical to the master.

The same path catches a stale node up when the partition heals on the
minority side: the minority side cannot mutate, so when it rejoins it
strictly has the older version, and the pull fires.

There is one corner case worth knowing about: the pull only fires when
`peer_version > local_version`. Two nodes at the same version with
different content would silently diverge — but the design forbids
that (only the master mutates, and the master is the only one bumping
the version) unless somebody hand-edits `cluster.yaml` and also
manually sets `version:`. Don't do that.

## Why a check flips state

The aggregator runs on the master only. Followers' probe results are
shipped to the master via the `ReportResult` RPC; the master's own
probe results are submitted directly.

For each check, the aggregator keeps the latest result per node within
a freshness window (3× the check interval, minimum 30s). On each
incoming submission it counts OK vs not-OK across the fresh results:

- 0 fresh reports → `unknown`
- more OK than not-OK → `up`
- more not-OK than OK → `down`
- tie → `up` (a tie at one report means one node says yes and one says
  no; biasing toward `up` avoids false alerts when nodes disagree
  transiently).

A state flip is **not** committed immediately. Hysteresis requires the
candidate state to hold for **two consecutive aggregate evaluations**
before the state transition fires and the alert dispatcher is called.
Set in `internal/checks/aggregator.go` as the `HysteresisCount`
constant — change it there if you want a hair-trigger or a slower
alert.

If the master changes, the new master starts the per-check state from
`unknown` and rebuilds it as fresh results arrive. The first few
seconds after a re-election can therefore show `unknown` even for
checks that were `up` a moment ago.

## What `qu` does *not* do

These omissions are intentional in v1 and useful to know up front:

- **No persistent history.** Only the current aggregate state lives in
  memory. There are no graphs, no SLA reports. Add a sidecar (Prometheus
  exporter, SQLite logger) if you need them.
- **No automatic key rotation.** Re-init a node and re-trust if you
  need to roll its identity. See [security.md](security.md).
- **No multi-tenant isolation.** One cluster = one set of checks =
  one alert tree.
- **No web UI.** Operator surface is `qu` (CLI), `qu tui`, and direct
  edits to `cluster.yaml`.
- **No automatic peer eviction on prolonged downtime.** A dead peer
  stays in `cluster.yaml` until an operator runs `qu node remove`,
  because that decision affects the quorum size and shouldn't happen
  silently.
