# Deployment: Docker / docker-compose

The published image is a 14 MB distroless static container with the
`qu` binary as the entrypoint. It runs as root by default so the
daemon can bind privileged ports and open ICMP sockets; override with
`--user` if your host doesn't need that.

## Image references

```
git.cer.sh/axodouble/quptime:master          # tip of main, multi-arch
git.cer.sh/axodouble/quptime:v0.1.0          # tagged release
git.cer.sh/axodouble/quptime:v0.1.0-amd64    # single-arch (if you must pin)
```

The image embeds `QUPTIME_DIR=/etc/quptime` and declares it a volume —
treat it as the only piece of state worth persisting.

## Single-node, single-container compose

For a development cluster or a single-node smoke test:

```yaml
# compose.yaml
services:
  quptime:
    image: git.cer.sh/axodouble/quptime:v0.1.0
    container_name: quptime
    restart: unless-stopped
    ports:
      - "9901:9901"
    volumes:
      - quptime-data:/etc/quptime
    # ICMP UDP-mode pings need a permissive sysctl on the host:
    #   sysctl net.ipv4.ping_group_range="0 2147483647"
    # Or grant CAP_NET_RAW (more accurate, raw ICMP).
    cap_add:
      - NET_RAW

volumes:
  quptime-data:
```

You must **`qu init` before the daemon will start**. With this compose
file:

```sh
docker compose run --rm quptime init --advertise <host-ip>:9901
docker compose up -d
docker compose exec quptime qu status
```

`<host-ip>` must be reachable from every other node — the loopback
address inside the container is useless to peers.

## Three-node compose on a single host

For local testing of the full quorum machinery without three machines:

```yaml
# compose.yaml
x-quptime: &quptime
  image: git.cer.sh/axodouble/quptime:v0.1.0
  restart: unless-stopped
  cap_add:
    - NET_RAW

services:
  alpha:
    <<: *quptime
    container_name: alpha
    ports: ["9901:9901"]
    volumes: ["alpha-data:/etc/quptime"]

  bravo:
    <<: *quptime
    container_name: bravo
    ports: ["9902:9901"]
    volumes: ["bravo-data:/etc/quptime"]

  charlie:
    <<: *quptime
    container_name: charlie
    ports: ["9903:9901"]
    volumes: ["charlie-data:/etc/quptime"]

volumes:
  alpha-data:
  bravo-data:
  charlie-data:
```

Bootstrap:

```sh
# First node: prints the secret to stdout.
docker compose run --rm alpha init --advertise alpha:9901
# Capture the secret (or read it back from alpha-data).
SECRET=$(docker compose exec alpha cat /etc/quptime/node.yaml | grep cluster_secret | awk '{print $2}')

docker compose run --rm bravo   init --advertise bravo:9901   --secret "$SECRET"
docker compose run --rm charlie init --advertise charlie:9901 --secret "$SECRET"

docker compose up -d

# Invite from alpha. The hostnames resolve over the compose network.
docker compose exec alpha qu node add bravo:9901
sleep 3   # wait for heartbeats before the next add
docker compose exec alpha qu node add charlie:9901

docker compose exec alpha qu status
```

For a cluster on three separate hosts, replicate the compose file on
each box with different `advertise` addresses (the public hostname or
the overlay IP) and bootstrap the same way.

## Multi-host compose

The natural unit is one compose file per host, each running one
`qu` container. The minimum-viable file per host:

```yaml
# /etc/qu-stack/compose.yaml
services:
  quptime:
    image: git.cer.sh/axodouble/quptime:v0.1.0
    container_name: quptime
    restart: unless-stopped
    ports:
      - "9901:9901"
    volumes:
      - /srv/quptime/data:/etc/quptime
    cap_add:
      - NET_RAW
```

Persistence is a bind-mount under `/srv/quptime/data` so backups and
upgrades hit a known path. See [operations.md](../operations.md) for
the backup recipe.

Inter-host traffic on TCP/9901 must be reachable. If the boxes don't
share a private network, prefer the
[Tailscale recipe](tailscale.md) over exposing 9901 directly — see
[public-internet.md](public-internet.md) for the threat model if you
must expose it.

## Behind a reverse proxy

**Don't.** `qu` is mTLS-pinned at the application layer, so a TLS-
terminating proxy would force the daemon to trust whatever cert the
proxy presents — defeating fingerprint pinning. If you need a single
public address per node, use a Layer 4 TCP proxy (`nginx stream`,
HAProxy `mode tcp`, or a plain firewall NAT) that forwards bytes
without touching them.

## Image internals

Build locally if you want to inspect what you're running:

```sh
docker buildx build \
  --build-arg VERSION=$(git describe --tags --always) \
  --platform linux/amd64,linux/arm64 \
  --file docker/Dockerfile \
  --tag quptime:dev \
  --load \
  .
```

The Dockerfile (see `docker/Dockerfile`) is two stages: a `golang:1.24-alpine`
builder that cross-compiles with `-trimpath -ldflags "-s -w"`, and a
`gcr.io/distroless/static-debian12` runtime. No shell, no package
manager, no SSH; you cannot `docker exec -it sh` into it. Use
`docker exec quptime qu ...` for everything.

## Healthcheck

The container exits non-zero if the daemon crashes, so the default
`restart: unless-stopped` policy is enough for liveness. A more
useful readiness check requires the binary to be in your healthchecker:

```yaml
healthcheck:
  test: ["CMD", "/usr/local/bin/qu", "status"]
  interval: 30s
  timeout: 5s
  retries: 3
  start_period: 10s
```

`qu status` exits 0 when the daemon socket is reachable and the
control RPC succeeds — it does **not** fail on quorum loss. That's
intentional: restarting a quorum-less node won't bring quorum back,
and a healthcheck that flaps a follower in and out of `unhealthy`
state every time the master is briefly unreachable is worse than no
check. If you want a stricter readiness signal, pipe `qu status`
through `grep -q 'quorum     true'`.
