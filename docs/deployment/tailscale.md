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
has no port 9901 open, and the cluster secret + mTLS handshake gate
the link inside the tunnel.

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
    image: git.cer.sh/axodouble/quptime:v0.1.0
    container_name: quptime
    volumes:
      - quptime:/etc/quptime
    network_mode: "service:tailscale"
    depends_on: [tailscale]
    cap_add: [NET_RAW]
    # No restart directive yet — needs `qu init` first.

volumes:
  tailscale:
  quptime:
```

### One-time bootstrap

Each host runs the same script with different `HOST` and `TAILSCALE_AUTHKEY`:

```sh
# .env
HOST=alpha
TAILSCALE_AUTHKEY=tskey-auth-xxxxxxxx
```

Start Tailscale alone first so it gets an IP:

```sh
docker compose up -d tailscale
sleep 5
TSIP=$(docker compose exec tailscale tailscale ip --4)
echo "this node's tailnet IP: $TSIP"
```

On the **first** host, init without `--secret`:

```sh
docker compose run --rm quptime init --advertise "$TSIP:9901"
# Grab the printed secret; pipe through your password manager.
```

On every **other** host, paste the secret:

```sh
docker compose run --rm quptime init \
  --advertise "$TSIP:9901" \
  --secret "$CLUSTER_SECRET"
```

Then bring up `qu` on every node and invite from the first:

```sh
# Each host
docker compose up -d quptime

# From alpha
docker compose exec quptime qu node add 100.64.1.2:9901
sleep 3
docker compose exec quptime qu node add 100.64.1.3:9901

docker compose exec quptime qu status
```

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
2. `qu init --advertise <overlay-ip>:9901`.
3. Set `bind_addr: <overlay-ip>` in `node.yaml` so the daemon does
   **not** also listen on the public interface.
4. Open `:9901` only on the overlay interface in your firewall — for
   nftables that's something like `iifname "wg0" tcp dport 9901
   accept`.

The cluster secret and mTLS fingerprints still apply; the overlay just
removes the open-internet attack surface.

## Why prefer overlay over public exposure

- Single failure domain at the network layer: an attacker who finds an
  exploit in your overlay client (rare; Tailscale and WireGuard are
  small surfaces) still hits the application-layer pinning before any
  cluster-level operation.
- The cluster secret can be lower-entropy when it's already
  unreachable from outside. (You should still treat it as a real
  secret; "defence in depth" only works if every layer is real.)
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
