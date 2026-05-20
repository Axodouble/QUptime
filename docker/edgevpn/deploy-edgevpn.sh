#!/usr/bin/env bash
# Bootstrap helper for the EdgeVPN docker-compose recipe.
#
# Usage:
#   ./deploy-edgevpn.sh gentoken                # generate a new EdgeVPN community token
#   ./deploy-edgevpn.sh init                    # first node — auto-init a one-node cluster
#   ./deploy-edgevpn.sh enroll <name>           # mint a qu enrollment token from a running cluster
#   ./deploy-edgevpn.sh join <token>            # join an existing cluster on a new host
#   ./deploy-edgevpn.sh status                  # show qu status
#   ./deploy-edgevpn.sh down                    # stop everything (keeps volumes)
#
# Reads a sibling `.env` for EDGEVPNTOKEN, EDGEVPN_ADDRESS, and
# QUPTIME_ADVERTISE. If `.env` is missing on `init` or `join` it is
# scaffolded interactively.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
ENV_FILE="$SCRIPT_DIR/.env"
EDGEVPN_IMAGE="quay.io/mudler/edgevpn:latest"

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
    local prompt_text="$1" default="${2:-}" reply
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
    echo "If you don't have an EdgeVPN token yet, cancel and run: $0 gentoken"
    local token address advertise
    token="$(prompt 'EdgeVPN community token (same on every node)')"
    [[ -n "$token" ]] || fail "EDGEVPNTOKEN is required"
    address="$(prompt 'EdgeVPN overlay address (e.g. 10.1.0.11/24, unique per node)')"
    [[ -n "$address" ]] || fail "EDGEVPN_ADDRESS is required"
    local default_advertise=""
    if [[ "$address" =~ ^([0-9.]+)/[0-9]+$ ]]; then
        default_advertise="${BASH_REMATCH[1]}:9901"
    fi
    advertise="$(prompt 'QUptime advertise host:port' "$default_advertise")"
    [[ -n "$advertise" ]] || fail "QUPTIME_ADVERTISE is required"

    umask 077
    cat >"$ENV_FILE" <<EOF
EDGEVPNTOKEN=$token
EDGEVPN_ADDRESS=$address
QUPTIME_ADVERTISE=$advertise
EOF
    echo "Wrote $ENV_FILE"
}

require_env() {
    [[ -f "$ENV_FILE" ]] || fail ".env not found at $ENV_FILE — run '$0 init' or '$0 join <token>'"
    # shellcheck disable=SC1090
    set -a; source "$ENV_FILE"; set +a
    [[ -n "${EDGEVPNTOKEN:-}" ]]      || fail "EDGEVPNTOKEN missing from $ENV_FILE"
    [[ -n "${EDGEVPN_ADDRESS:-}" ]]   || fail "EDGEVPN_ADDRESS missing from $ENV_FILE"
    [[ -n "${QUPTIME_ADVERTISE:-}" ]] || fail "QUPTIME_ADVERTISE missing from $ENV_FILE"
}

cmd_gentoken() {
    require_docker
    echo "EDGEVPNTOKEN=$(docker run --rm "$EDGEVPN_IMAGE" -g | base64 | tr -d '\n')" > .env 
    echo "EDGEVPN_ADDRESS=10.202.0.51/24" >> .env 
    echo "QUPTIME_ADVERTISE=10.202.0.51:9901" >> .env
    
    echo "Generated new EdgeVPN token and wrote to .env (also set example EDGEVPN_ADDRESS and QUPTIME_ADVERTISE)"
    echo "Make sure to update the EDGEVPN_ADDRESS to a unique IP in the same subnet for each node, and set QUPTIME_ADVERTISE to the corresponding host:port that this node will be reachable at from other nodes."
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
    echo "then on the new host (with the same EDGEVPNTOKEN in its .env):"
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

    echo "Bringing up the EdgeVPN sidecar..."
    compose up -d edgevpn

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
    [[ -n "$cmd" ]] || { sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 1; }
    shift || true
    case "$cmd" in
        gentoken) cmd_gentoken "$@" ;;
        init)     cmd_init "$@" ;;
        enroll)   cmd_enroll "$@" ;;
        join)     cmd_join "$@" ;;
        status)   cmd_status "$@" ;;
        down)     cmd_down "$@" ;;
        -h|--help|help) sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//' ;;
        *) fail "unknown command: $cmd (try '$0 help')" ;;
    esac
}

main "$@"
