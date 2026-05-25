#!/usr/bin/env bash
# Bootstrap helper for the Tailscale docker-compose recipe.
#
# Usage:
#   ./deploy-tailscale.sh init                  # first node — auto-init a one-node cluster
#   ./deploy-tailscale.sh enroll <name>         # mint an enrollment token from a running cluster
#   ./deploy-tailscale.sh join <token>          # join an existing cluster on a new host
#   ./deploy-tailscale.sh status                # show qu status
#   ./deploy-tailscale.sh down                  # stop everything (keeps volumes)
#
# Reads a sibling `.env` for TAILSCALE_AUTHKEY, TS_HOSTNAME, and
# QUPTIME_ADVERTISE. If `.env` is missing on `init` or `join` it is
# scaffolded interactively.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
ENV_FILE="$SCRIPT_DIR/.env"

compose() {
    docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

fail() {
    echo "error: $*" >&2
    exit 1
}

require_docker() {
    command -v docker >/dev/null 2>&1 || fail "docker is not installed"
    docker compose version >/dev/null 2>&1 || fail "docker compose plugin is required"
}

prompt() {
    local var="$1" prompt_text="$2" default="${3:-}" reply
    if [[ -n "$default" ]]; then
        read -r -p "$prompt_text [$default]: " reply
        reply="${reply:-$default}"
    else
        read -r -p "$prompt_text: " reply
    fi
    printf '%s' "$reply"
}

scaffold_env() {
    if [[ -f "$ENV_FILE" ]]; then
        return
    fi
    echo "No .env found at $ENV_FILE — let's create one."
    local authkey hostname advertise
    authkey="$(prompt TAILSCALE_AUTHKEY 'Tailscale auth key (tskey-auth-...)')"
    [[ -n "$authkey" ]] || fail "TAILSCALE_AUTHKEY is required"
    hostname="$(prompt TS_HOSTNAME 'Tailscale hostname for this node' "quptime-$(hostname -s)")"
    advertise="$(prompt QUPTIME_ADVERTISE 'This node tailnet ip:port (e.g. 100.64.1.1:9901)')"
    [[ -n "$advertise" ]] || fail "QUPTIME_ADVERTISE is required"

    umask 077
    cat >"$ENV_FILE" <<EOF
TAILSCALE_AUTHKEY=$authkey
TS_HOSTNAME=$hostname
QUPTIME_ADVERTISE=$advertise
EOF
    echo "Wrote $ENV_FILE"
}

require_env() {
    [[ -f "$ENV_FILE" ]] || fail ".env not found at $ENV_FILE — run '$0 init' or '$0 join <token>'"
    # shellcheck disable=SC1090
    set -a; source "$ENV_FILE"; set +a
    [[ -n "${TAILSCALE_AUTHKEY:-}" ]]  || fail "TAILSCALE_AUTHKEY missing from $ENV_FILE"
    [[ -n "${QUPTIME_ADVERTISE:-}" ]]  || fail "QUPTIME_ADVERTISE missing from $ENV_FILE"
}

cmd_init() {
    require_docker
    scaffold_env
    require_env
    echo "Starting cluster as the first node (auto-init from QUPTIME_ADVERTISE=$QUPTIME_ADVERTISE)..."
    compose up -d
    echo
    echo "Done. To add another host, run on this node:"
    echo "  $0 enroll <name>"
    echo "then run on the new host:"
    echo "  $0 join <token>"
}

cmd_enroll() {
    require_docker
    require_env
    local name="${1:-}"
    [[ -n "$name" ]] || fail "usage: $0 enroll <name>"
    compose exec quptime qu enroll create --name "$name" --auto-approve --ttl 1h
}

cmd_join() {
    require_docker
    local token="${1:-}"
    [[ -n "$token" ]] || fail "usage: $0 join <token>"
    scaffold_env
    require_env

    echo "Bringing up the tailnet sidecar..."
    compose up -d tailscale

    echo "Redeeming enrollment token (advertising as $QUPTIME_ADVERTISE)..."
    compose run --rm quptime \
        qu enroll join "$token" --advertise "$QUPTIME_ADVERTISE" --yes

    echo "Starting quptime..."
    compose up -d
    echo
    echo "Done. Verify from any node with: $0 status"
}

cmd_status() {
    require_docker
    require_env
    compose exec quptime qu status
}

cmd_down() {
    require_docker
    require_env
    compose down
}

main() {
    local cmd="${1:-}"
    [[ -n "$cmd" ]] || { sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'; exit 1; }
    shift || true
    case "$cmd" in
        init)    cmd_init "$@" ;;
        enroll)  cmd_enroll "$@" ;;
        join)    cmd_join "$@" ;;
        status)  cmd_status "$@" ;;
        down)    cmd_down "$@" ;;
        -h|--help|help) sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//' ;;
        *) fail "unknown command: $cmd (try '$0 help')" ;;
    esac
}

main "$@"
