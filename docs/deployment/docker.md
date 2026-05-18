# Deployment: Docker / docker-compose

The published image is a 14 MB distroless static container with the
`qu` binary as the entrypoint. It runs as root by default so the
daemon can bind privileged ports and open ICMP sockets; override with
`--user` if your host doesn't need that.

New nodes join the cluster via single-use pre-deployment enrollment
tokens — there is no longer a shared cluster secret. The pattern in
every recipe below is:

1. Bring up the first node with `docker compose up -d`. `qu serve`
   auto-initialises a one-node cluster from the `QUPTIME_*` env vars.
2. On that node: `docker compose exec quptime qu enroll create --auto-approve`.
   Copy the printed `qu enroll join <token>` line out of band.
3. On each new host, run the join *before* the daemon starts so it
   sees a pre-populated data dir:
   ```sh
   docker compose run --rm quptime qu enroll join <token> \
     --advertise <this-host:9901> --yes
   docker compose up -d
   ```

See [../security.md](../security.md) for the threat model and
`qu enroll --help` for the rest of the subcommands (`list`, `approve`,
`revoke`).

## Image references

The same multi-arch (amd64 + arm64) image is published to two
registries. **The Gitea registry is the canonical source** — it also
publishes canary `:master` builds on every branch push. GHCR is a
tag-only push-mirror for users who can't reach `git.cer.sh`.

Primary — Gitea registry:

```
git.cer.sh/axodouble/quptime:master          # tip of main, multi-arch
git.cer.sh/axodouble/quptime:latest          # latest tagged release
git.cer.sh/axodouble/quptime:v0.0.1          # specific tagged release
git.cer.sh/axodouble/quptime:latest-amd64    # single-arch (if you must pin)
```

Fallback — GitHub Container Registry:

```
ghcr.io/axodouble/quptime:latest             # latest tagged release
ghcr.io/axodouble/quptime:v0.0.1             # specific tagged release
ghcr.io/axodouble/quptime:0.0                # latest patch in the 0.0 minor line
```

The image embeds `QUPTIME_DIR=/etc/quptime` and declares it a volume —
treat it as the only piece of state worth persisting.

## Single-node, single-container compose

For a development cluster or a single-node smoke test:

```yaml
# compose.yaml
services:
  quptime:
    image: git.cer.sh/axodouble/quptime:latest
    container_name: quptime
    restart: unless-stopped
    environment:
      # host:port other nodes use to reach this one. Must be reachable
      # from every peer — the loopback inside the container is useless.
      - QUPTIME_ADVERTISE=<host-ip>:9901
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

`qu serve` auto-initialises the data volume on first start using the
`QUPTIME_*` env vars (see [configuration.md](../configuration.md) for
the full list). One command brings everything up:

```sh
docker compose up -d
docker compose exec quptime qu status
```

That is a self-contained one-node cluster. The full list of accepted
env vars lives in
[configuration.md](../configuration.md#nodeyaml-field-overrides).

## Three-node compose on a single host

For local testing of the full quorum machinery without three machines:

```yaml
# compose.yaml
x-quptime: &quptime
  image: git.cer.sh/axodouble/quptime:latest
  restart: unless-stopped
  cap_add:
    - NET_RAW

services:
  alpha:
    <<: *quptime
    container_name: alpha
    environment:
      - QUPTIME_ADVERTISE=alpha:9901
    ports: ["9901:9901"]
    volumes: ["alpha-data:/etc/quptime"]

  bravo:
    <<: *quptime
    container_name: bravo
    environment:
      - QUPTIME_ADVERTISE=bravo:9901
    ports: ["9902:9901"]
    volumes: ["bravo-data:/etc/quptime"]

  charlie:
    <<: *quptime
    container_name: charlie
    environment:
      - QUPTIME_ADVERTISE=charlie:9901
    ports: ["9903:9901"]
    volumes: ["charlie-data:/etc/quptime"]

volumes:
  alpha-data:
  bravo-data:
  charlie-data:
```

Bootstrap:

```sh
# 1. Start alpha first — it auto-initialises as a one-node cluster.
docker compose up -d alpha

# 2. Mint one enrollment token per follower from alpha. --auto-approve
#    skips the manual `qu enroll approve` step; drop it if you want
#    a two-operator audit checkpoint.
TOKEN_BRAVO=$(docker compose exec -T alpha qu enroll create \
  --name bravo --auto-approve | awk '/qu enroll join/{print $NF}')
TOKEN_CHARLIE=$(docker compose exec -T alpha qu enroll create \
  --name charlie --auto-approve | awk '/qu enroll join/{print $NF}')

# 3. Run the join inside each follower's volume BEFORE the daemon
#    starts. `docker compose run --rm` brings up the container,
#    runs the one-shot command, and removes it — the named volume
#    keeps the freshly written node.yaml/keys/cluster.yaml.
docker compose run --rm bravo qu enroll join "$TOKEN_BRAVO" \
  --advertise bravo:9901 --yes
docker compose run --rm charlie qu enroll join "$TOKEN_CHARLIE" \
  --advertise charlie:9901 --yes

# 4. Now start the followers normally.
docker compose up -d bravo charlie

docker compose exec alpha qu status
```

The hostnames resolve over the compose network. For a cluster on
three separate hosts, replicate the compose file on each box with
different `advertise` addresses (the public hostname or the overlay
IP) and follow the same enroll pattern.

## Multi-host compose

The natural unit is one compose file per host, each running one
`qu` container. The minimum-viable file per host:

```yaml
# /etc/qu-stack/compose.yaml
services:
  quptime:
    image: git.cer.sh/axodouble/quptime:latest
    container_name: quptime
    restart: unless-stopped
    environment:
      - QUPTIME_ADVERTISE=${QUPTIME_ADVERTISE}        # host:9901 reachable from peers
    ports:
      - "9901:9901"
    volumes:
      - /srv/quptime/data:/etc/quptime
    cap_add:
      - NET_RAW
```

Put the per-host value (`QUPTIME_ADVERTISE`) in a sibling `.env` file
so the compose file itself is identical across hosts.

Bootstrap the first host with `docker compose up -d`. For every
subsequent host, mint a token on the live cluster and join before
starting the daemon:

```sh
# On the first host (already running):
docker compose exec quptime qu enroll create --name bravo --auto-approve
# → copy the `qu enroll join <token>` line out of band.

# On the new host (data dir empty):
docker compose run --rm quptime qu enroll join <token> \
  --advertise bravo.example.com:9901 --yes
docker compose up -d
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
