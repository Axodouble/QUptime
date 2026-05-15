# Installation

`qu` ships as a single static Linux binary. Pick whichever method
matches how you manage software on the host.

> Choosing a deployment recipe instead? Jump to
> [systemd](deployment/systemd.md), [Docker](deployment/docker.md),
> [Tailscale](deployment/tailscale.md), or
> [public-internet](deployment/public-internet.md).

## Pre-built binary (recommended)

Releases are published to the [Gitea releases
page](https://git.cer.sh/axodouble/quptime/releases) with a
`SHA256SUMS` file. Two architectures are built: `linux-amd64` and
`linux-arm64`.

```sh
# Always pin to a tag — `latest` resolves on the server side.
TAG=v0.1.0
ARCH=amd64   # or arm64

curl -fSL -o qu \
  "https://git.cer.sh/axodouble/quptime/releases/download/${TAG}/qu-${TAG}-linux-${ARCH}"
curl -fSL -o SHA256SUMS \
  "https://git.cer.sh/axodouble/quptime/releases/download/${TAG}/SHA256SUMS"

# Verify before installing.
sha256sum --check --ignore-missing SHA256SUMS

install -m 0755 qu /usr/local/bin/qu
```

## One-line install script

The repo ships an `install.sh` that handles the download, checksum,
shell-completion installation, and a default systemd unit file. Run it
under `sudo` so it can write to `/usr/local/bin` and
`/etc/systemd/system`.

```sh
curl -fsSL https://git.cer.sh/Axodouble/QUptime/raw/branch/master/install.sh | sudo bash
```

What it does:

1. Looks up the latest release via the Gitea API.
2. Downloads the binary to `/usr/local/bin/qu`.
3. Installs bash / zsh / fish completion if a target directory exists.
4. Writes `/etc/systemd/system/qu-serve.service` and enables it (but
   does **not** start it — you need to run `qu init` first).

The unit it writes is minimal. For a production unit with hardening,
see the [systemd deployment guide](deployment/systemd.md).

## Build from source

Requires Go 1.24.2 or newer.

```sh
git clone https://git.cer.sh/axodouble/quptime.git
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

A multi-arch (`amd64` + `arm64`) image is published to the Gitea
registry on every tag and every push to `master`:

```
git.cer.sh/axodouble/quptime:master   # tip of main
git.cer.sh/axodouble/quptime:latest   # latest tagged release
git.cer.sh/axodouble/quptime:v0.0.1   # pinned release
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
