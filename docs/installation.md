# Installation

`qu` ships as a single static Linux binary. Pick whichever method
matches how you manage software on the host.

> Choosing a deployment recipe instead? Jump to
> [systemd](deployment/systemd.md), [Docker](deployment/docker.md),
> [Tailscale](deployment/tailscale.md), or
> [public-internet](deployment/public-internet.md).

## Pre-built binary (recommended)

Every tag triggers identical builds on both sources, so either one
serves the same artefact set. Gitea is the canonical home; GitHub is a
push-mirror.

Primary — Gitea releases:
<https://git.cer.sh/axodouble/quptime/releases>

Fallback — GitHub releases (mirrored from the same tag):
<https://github.com/Axodouble/QUptime/releases>

Each release ships `qu-${TAG}-linux-amd64`, `qu-${TAG}-linux-arm64`,
and a `SHA256SUMS` file.

```sh
# Always pin to a tag — `latest` resolves on the server side.
TAG=v0.0.1
ARCH=amd64   # or arm64

# Primary: Gitea
curl -fSL -o qu \
  "https://git.cer.sh/axodouble/quptime/releases/download/${TAG}/qu-${TAG}-linux-${ARCH}"
curl -fSL -o SHA256SUMS \
  "https://git.cer.sh/axodouble/quptime/releases/download/${TAG}/SHA256SUMS"

# (or the GitHub mirror — substitute the host below if Gitea is unreachable)
#   https://github.com/Axodouble/QUptime/releases/download/${TAG}/qu-${TAG}-linux-${ARCH}
#   https://github.com/Axodouble/QUptime/releases/download/${TAG}/SHA256SUMS

# Verify before installing. Use the SHA256SUMS from the SAME source
# as the binary — never mix.
sha256sum --check --ignore-missing SHA256SUMS

install -m 0755 qu /usr/local/bin/qu
```

## One-line install script

The repo ships an `install.sh` that handles the download, checksum,
shell-completion installation, and a hardened systemd unit. Run it
under `sudo` so it can write to `/usr/local/bin` and
`/etc/systemd/system`.

```sh
curl -fsSL https://git.cer.sh/Axodouble/QUptime/raw/branch/master/install.sh | sudo bash
# or, via the GitHub mirror:
# curl -fsSL https://raw.githubusercontent.com/Axodouble/QUptime/master/install.sh | sudo bash
```

What it does:

1. Looks up the latest release via the Gitea API; falls back to the
   GitHub API if Gitea is unreachable.
2. Downloads the per-arch binary and the matching `SHA256SUMS` from
   the same source, then verifies the checksum. Refuses to install on
   a mismatch.
3. Installs bash / zsh / fish completion if a target directory exists.
4. Creates a dedicated `quptime` system user and writes
   `/etc/systemd/system/quptime.service` (hardened — matches the unit
   in [systemd.md](deployment/systemd.md)). Enables but does not start
   the service, so you can configure identity before first boot.
5. Repairs ownership and modes under `/etc/quptime/` to the canonical
   layout (`0750` on the dir, `0700` on `keys/`, `0600` on
   `node.yaml` / `cluster.yaml` / `trust.yaml` / `keys/private.pem`,
   `0644` on `keys/public.pem` / `keys/cert.pem`). This makes the
   installer idempotent for permission damage — if something
   tightened or loosened modes (a stray `chmod -R`, a misguided
   backup restore, an accidental `sudo qu init`), re-running
   `install.sh` puts everything back without touching the contents
   of those files.

## Build from source

Requires Go 1.24.2 or newer.

```sh
# Either remote — Gitea is canonical, GitHub is a push-mirror.
git clone https://git.cer.sh/axodouble/quptime.git
# git clone https://github.com/Axodouble/QUptime.git
cd quptime
go build -ldflags "-X main.version=$(git describe --tags --always)" -o qu ./cmd/qu

./qu --version
```

Static binary, no cgo. `CGO_ENABLED=0` is the default on a clean Go
install; if you've enabled cgo globally, set it explicitly:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o qu ./cmd/qu
```

## Docker image

The same multi-arch (`amd64` + `arm64`) image is published to two
registries on every tag. The Gitea registry is the canonical source
and also gets canary `:master` builds; GHCR is a tag-only mirror.

Primary — Gitea registry:

```
git.cer.sh/axodouble/quptime:master   # tip of main (canary)
git.cer.sh/axodouble/quptime:latest   # latest tagged release
git.cer.sh/axodouble/quptime:v0.0.1   # pinned release
```

Fallback — GitHub Container Registry:

```
ghcr.io/axodouble/quptime:latest      # latest tagged release
ghcr.io/axodouble/quptime:v0.0.1      # pinned release
ghcr.io/axodouble/quptime:0.0         # latest 0.0.x
```

See the [Docker deployment guide](deployment/docker.md) for compose
files and volume layout.

## Verifying the install

```sh
qu --version
qu --help
```

If completions installed, `qu <tab>` will list subcommands. After
`qu init` you can run `qu status` to confirm the daemon is reachable
over its control socket.

## Next steps

- [Configure the node and the cluster](configuration.md).
- Pick a deployment recipe under [docs/deployment/](deployment/).
- Walk through the [architecture](architecture.md) so the operational
  guarantees are clear before you commit to a topology.
