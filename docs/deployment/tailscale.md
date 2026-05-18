# Deployment: Tailscale / WireGuard overlay

When your nodes live in different networks — different VPS providers,
different physical sites, a mix of home and cloud — exposing TCP/9901
to the open internet is a poor idea. An overlay network gives every
node a stable private IP regardless of NAT, and `qu` only needs to
listen on that overlay address.

This page focuses on Tailscale because the repo ships an example
compose for it, but everything generalises to WireGuard, Nebula, or a
self-hosted Headscale.

## The big idea

```
+--- host A (VPS, no public ICMP) ----+
| tailscale ←→ overlay ip 100.64.1.1  |
| qu listening on 100.64.1.1:9901     |
+-------------------------------------+
              │   mTLS over overlay
              ▼
+--- host B (homelab behind NAT) -----+
| tailscale ←→ overlay ip 100.64.1.2  |
| qu listening on 100.64.1.2:9901     |
+-------------------------------------+
```

`bind_addr` is set to the tailscale IP, the host's public interface
has no port 9901 open, and the mTLS handshake plus pre-deployment
enrollment tokens gate the link inside the tunnel.

## Compose recipe

The repo ships [`docker/docker-compose-tailscale.yml`](../../docker/docker-compose-tailscale.yml).
The relevant trick is `network_mode: "service:tailscale"` — the
`quptime` container shares the network namespace of the `tailscale`
sidecar so it sees the tailnet as its own interface.

```yaml
services:
  tailscale:
    image: tailscale/tailscale:latest
    container_name: tailscale
    cap_add: [NET_ADMIN]
    environment:
      - TS_AUTHKEY=${TAILSCALE_AUTHKEY}   # provision via .env
      - TS_HOSTNAME=quptime-${HOST}       # name visible in admin
    volumes:
      - /dev/net/tun:/dev/net/tun
      - tailscale:/var/lib/tailscale
    restart: unless-stopped

  quptime:
    image: git.cer.sh/axodouble/quptime:latest
    container_name: quptime
    environment:
      # host:port other QUptime nodes use to reach this one. Should be
      # this node's tailnet IP / MagicDNS name. Auto-init reads this on
      # first start.
      - QUPTIME_ADVERTISE=${QUPTIME_ADVERTISE}
    volumes:
      - quptime:/etc/quptime
    network_mode: "service:tailscale"
    depends_on: [tailscale]
    cap_add: [NET_RAW]
    restart: unless-stopped

volumes:
  tailscale:
  quptime:
```

### One-time bootstrap

Each host runs the same compose file with a per-host `.env`:

```sh
# .env (alpha — the first node)
HOST=alpha
TAILSCALE_AUTHKEY=tskey-auth-xxxxxxxx
QUPTIME_ADVERTISE=100.64.1.1:9901          # this node's tailnet IP
```

Start the stack on the first host. `qu serve` auto-initialises the
volume using the env vars above, so a single `docker compose up`
brings everything up as a one-node cluster:

```sh
docker compose up -d
```

For each follower, mint an enrollment token from the running cluster
(here from alpha) and copy the printed `qu enroll join …` line out
of band:

```sh
# On alpha:
docker compose exec quptime qu enroll create --name bravo --auto-approve --ttl 1h
```

`--auto-approve` skips the manual `qu enroll approve` step on the
cluster side; drop it if you want a second-operator audit checkpoint.
Tokens are single-use and time-bound — see
[../security.md](../security.md) for the threat model.

On every **other** host, write the same `.env` (with that host's own
tailnet IP) and run the join *before* starting the daemon. The join
populates the data volume with this node's identity, the bootstrap
peer's pinned fingerprint, and a seeded `cluster.yaml`; `qu serve`
then comes up already trusting the cluster.

```sh
# .env (bravo)
HOST=bravo
TAILSCALE_AUTHKEY=tskey-auth-xxxxxxxx
QUPTIME_ADVERTISE=100.64.1.2:9901
```

```sh
# On bravo:
docker compose up -d tailscale          # bring up the tailnet first
docker compose run --rm quptime \
  qu enroll join <token> --advertise 100.64.1.2:9901 --yes
docker compose up -d
```

The `--rm` run shares the same `quptime` named volume as the long-
running service, so the freshly written `node.yaml`, keys, and
`cluster.yaml` are picked up the moment `qu serve` starts. From
alpha:

```sh
docker compose exec quptime qu status
```

should now show two peers (three once charlie is enrolled the same
way).

## Tailscale ACLs

Belt and braces — even though mTLS pins identities, lock down the
tailnet itself so only the `qu` nodes can reach each other's :9901.
In the Tailscale admin console:

```jsonc
{
  "tagOwners": { "tag:qu-node": ["group:ops"] },
  "acls": [
    {
      "action": "accept",
      "src": ["tag:qu-node"],
      "dst": ["tag:qu-node:9901"]
    }
    // ...your other rules
  ]
}
```

Then tag every `qu` node in its auth key:

```yaml
environment:
  - TS_AUTHKEY=${TAILSCALE_AUTHKEY}?ephemeral=false&tags=tag:qu-node
```

## WireGuard / Nebula / Headscale equivalents

The recipe generalises:

1. Provision the overlay interface on each host with a stable
   private IP (the tunnel's own address).
2. On the first node: `qu init --advertise <overlay-ip>:9901`. On
   every subsequent node: `qu enroll join <token> --advertise
   <overlay-ip>:9901` with a token minted on the first node.
3. Set `bind_addr: <overlay-ip>` in `node.yaml` so the daemon does
   **not** also listen on the public interface.
4. Open `:9901` only on the overlay interface in your firewall — for
   nftables that's something like `iifname "wg0" tcp dport 9901
   accept`.

mTLS fingerprint pinning still applies; the overlay just removes the
open-internet attack surface for new-peer enrollment.

## Why prefer overlay over public exposure

- Single failure domain at the network layer: an attacker who finds an
  exploit in your overlay client (rare; Tailscale and WireGuard are
  small surfaces) still hits the application-layer pinning before any
  cluster-level operation.
- Enrollment tokens are short-lived and single-use, but a brute-force
  attempt against an outstanding token is still cheap to attempt over
  the open internet. An overlay makes that surface unreachable in the
  first place.
- ICMP probes from a homelab to a target on the public internet are
  trivial through NAT, but ICMP *into* a homelab usually isn't.
  Running `qu` on a tailnet means peers can heartbeat each other
  regardless of NAT direction.

## Trade-offs

- One more thing to monitor. If your tailnet is down, your monitor is
  down. Counter-measure: run *another* tiny `qu` cluster (or a single
  node) on the public internet that watches the overlay's coordinator
  health.
- Probe latency includes the overlay's hop. Tailscale's wireguard is
  fast (<1 ms LAN, single-digit ms WAN) so this rarely matters, but
  if you're alerting on tight latency thresholds, account for it.
