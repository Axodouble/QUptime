#!/bin/bash
# QUptime installer.
#
# Downloads the latest released `qu` binary from the Gitea release
# page, verifies it against the published SHA256SUMS, installs it to
# /usr/local/bin, and (on systemd hosts) drops in a hardened
# quptime.service that matches the unit documented in
# docs/deployment/systemd.md. Idempotent — re-running upgrades the
# binary and refreshes the unit without touching the data directory.
set -euo pipefail

INSTALL_BIN="/usr/local/bin/qu"
SERVICE_FILE="/etc/systemd/system/quptime.service"
SERVICE_NAME="$(basename "$SERVICE_FILE")"
SERVICE_USER="quptime"
SERVICE_GROUP="quptime"
DATA_DIR="/etc/quptime"
REPO_API="https://git.cer.sh/api/v1/repos/axodouble/quptime/releases/latest"
RELEASE_BASE="https://git.cer.sh/axodouble/quptime/releases/download"

fail() {
    echo "Error: $*" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "$1 is not installed. Please install $1 and try again."
}

write_completion() {
    local shell=$1 path=$2
    [ -d "$(dirname "$path")" ] || return 1
    if "$INSTALL_BIN" completion "$shell" > "$path" 2>/dev/null; then
        echo "> installed $shell completion -> $path"
        return 0
    fi
    rm -f "$path"
    return 1
}

require_command curl
require_command jq
require_command sha256sum
require_command install
require_command mktemp

# --- target architecture ------------------------------------------------
case "$(uname -m)" in
    x86_64)         ARCH=amd64 ;;
    aarch64|arm64)  ARCH=arm64 ;;
    *)              fail "unsupported architecture: $(uname -m). Pre-built binaries are published for amd64 and arm64 only — build from source for other platforms." ;;
esac

if [ ! -w "$(dirname "$INSTALL_BIN")" ]; then
    fail "Cannot write to $(dirname "$INSTALL_BIN"). Run this script with sudo, or set INSTALL_BIN to a writable location."
fi

# --- latest release tag -------------------------------------------------
RELEASE=$(curl -fsSL "$REPO_API" | jq -r '.tag_name')
[ -n "$RELEASE" ] && [ "$RELEASE" != "null" ] \
    || fail "could not determine the latest release tag from $REPO_API"

BINARY_NAME="qu-${RELEASE}-linux-${ARCH}"
BINARY_URL="${RELEASE_BASE}/${RELEASE}/${BINARY_NAME}"
SUMS_URL="${RELEASE_BASE}/${RELEASE}/SHA256SUMS"

# --- download + verify --------------------------------------------------
# Stage in a temp dir so a failed verification never leaves a partial
# or unverified binary on disk.
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "> downloading $BINARY_NAME"
curl -fsSL --proto '=https' --tlsv1.2 -o "$TMPDIR/$BINARY_NAME" "$BINARY_URL"
echo "> downloading SHA256SUMS"
curl -fsSL --proto '=https' --tlsv1.2 -o "$TMPDIR/SHA256SUMS" "$SUMS_URL"

echo "> verifying checksum"
# Pull just our binary's entry so sha256sum -c doesn't fail on the
# arches we didn't download.
(
    cd "$TMPDIR"
    if ! grep -E "[[:space:]]\\*?${BINARY_NAME}\$" SHA256SUMS > expected.sum; then
        fail "no entry for $BINARY_NAME in published SHA256SUMS — refusing to install"
    fi
    if ! sha256sum -c expected.sum >/dev/null 2>&1; then
        echo "expected: $(awk '{print $1}' expected.sum)"
        echo "actual:   $(sha256sum "$BINARY_NAME" | awk '{print $1}')"
        fail "checksum mismatch for $BINARY_NAME — refusing to install"
    fi
)
echo "> checksum OK"

install -m 0755 "$TMPDIR/$BINARY_NAME" "$INSTALL_BIN"
echo "> qu ${RELEASE} installed to $INSTALL_BIN"

# --- shell completions --------------------------------------------------
if "$INSTALL_BIN" --help 2>/dev/null | grep -q "completion"; then
    write_completion bash /usr/share/bash-completion/completions/qu \
        || write_completion bash /etc/bash_completion.d/qu \
        || true
    write_completion zsh  /usr/share/zsh/site-functions/_qu                 || true
    write_completion fish /usr/share/fish/vendor_completions.d/qu.fish      || true
else
    echo "> qu does not expose completion support; skipping shell completion installation."
fi

# --- systemd unit -------------------------------------------------------
if ! command -v systemctl >/dev/null 2>&1; then
    echo
    echo "> systemd is not available on this system. Installation stops here."
    echo "> Run \`qu serve\` manually (or wire it into the supervisor of your choice)."
    exit 0
fi

# Dedicated service user. Hardened unit drops all capabilities and
# locks the daemon down with ProtectSystem=strict, so it must run as
# its own unprivileged account rather than the invoking sudo user.
if ! id "$SERVICE_USER" >/dev/null 2>&1; then
    echo "> creating system user $SERVICE_USER"
    useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
fi

install -d -o "$SERVICE_USER" -g "$SERVICE_GROUP" -m 0750 "$DATA_DIR"

echo "> writing $SERVICE_FILE"
cat > "$SERVICE_FILE" <<'EOF'
[Unit]
Description=QUptime distributed uptime monitor
Documentation=https://git.cer.sh/axodouble/quptime
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/qu serve
Restart=always
RestartSec=5s

User=quptime
Group=quptime

# Where state lives. RuntimeDirectory creates /var/run/quptime/ each
# boot owned by User:Group with mode 0750.
Environment=QUPTIME_DIR=/etc/quptime
RuntimeDirectory=quptime
RuntimeDirectoryMode=0750
ReadWritePaths=/etc/quptime /var/run/quptime

# Hardening. Comment out individual directives if a probe needs
# something we've revoked.
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
ProtectClock=true
ProtectHostname=true
RestrictNamespaces=true
RestrictRealtime=true
RestrictSUIDSGID=true
LockPersonality=true
MemoryDenyWriteExecute=true

# Network access is required (we're a network monitor). Keep address
# families minimal — AF_NETLINK is needed for some libc lookups.
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK

# If you need raw ICMP, *also* uncomment:
# AmbientCapabilities=CAP_NET_RAW
# CapabilityBoundingSet=CAP_NET_RAW
# Otherwise drop all capabilities:
CapabilityBoundingSet=

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE_NAME" >/dev/null
echo "> ${SERVICE_NAME} installed and enabled (not yet started)"

cat <<EOF

Installation complete.

Next steps:

  1. Initialise the node identity. Either:

       a) Let \`qu serve\` auto-init from environment variables.
          Drop a systemd override like:

            sudo systemctl edit ${SERVICE_NAME}
              [Service]
              Environment=QUPTIME_ADVERTISE=<this-host>:9901
              # On follower nodes, also set the shared join secret:
              # Environment=QUPTIME_CLUSTER_SECRET=<paste from first node>

       b) Or run \`qu init\` once explicitly:

            sudo -u ${SERVICE_USER} QUPTIME_DIR=${DATA_DIR} \\
              qu init --advertise <this-host>:9901

  2. Start the service:

       sudo systemctl start ${SERVICE_NAME}
       sudo -u ${SERVICE_USER} qu status

  3. For ICMP checks, the daemon defaults to unprivileged UDP-mode
     pings — those need the ping_group_range sysctl widened to include
     the ${SERVICE_USER} GID, or grant CAP_NET_RAW in the unit. See
     docs/deployment/systemd.md for the recipes.

Full documentation: https://git.cer.sh/axodouble/quptime/src/branch/master/docs
EOF
