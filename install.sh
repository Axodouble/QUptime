#!/bin/bash
set -euo pipefail

# Helper function which echo's all commands before executing them in grayscale prefixed with >
echo_cmd() {
    echo -e "\033[90m> $1\033[0m"
    eval "$1"
}

INSTALL_BIN="/usr/local/bin/qu"
SERVICE_FILE="/etc/systemd/system/qu.service"
SERVICE_USER="${SUDO_USER:-$(whoami)}"
SERVICE_GROUP="$(id -gn "$SERVICE_USER" 2>/dev/null || echo root)"

# Check if jq and curl are installed, if not, error out and ask the user to install them
if ! command -v jq > /dev/null; then
    echo "Error: jq is not installed. Please install jq and try again."
    exit 1
fi
if ! command -v curl > /dev/null; then
    echo "Error: curl is not installed. Please install curl and try again."
    exit 1
fi

# Check if the user is allowed to write to /usr/local/bin, if so, install qu there, else error out and ask the user to install qu manually
if [ -w "$(dirname "$INSTALL_BIN")" ]; then
    # Get release tag by $(curl -s https://git.cer.sh/api/v1/repos/axodouble/quptime/releases/latest | jq -r '.tag_name')
    RELEASE=$(curl -s https://git.cer.sh/api/v1/repos/axodouble/quptime/releases/latest | jq -r '.tag_name')
    # Download the latest release binary from the Git repository and save it to /usr/local/bin/qu
    
    echo_cmd "curl -L -o \"/usr/local/bin/qu\" \"https://git.cer.sh/axodouble/quptime/releases/download/${RELEASE}/qu-${RELEASE}-linux-amd64\""
    echo_cmd "chmod +x \"/usr/local/bin/qu\""
    echo "> qu has been installed to /usr/local/bin/qu"
    
    if "$INSTALL_BIN" --help 2>/dev/null | grep -q "completion"; then
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
        write_completion bash /usr/share/bash-completion/completions/qu \
        || write_completion bash /etc/bash_completion.d/qu
        write_completion zsh  /usr/share/zsh/site-functions/_qu
        write_completion fish /usr/share/fish/vendor_completions.d/qu.fish
    else
        echo "> qu does not expose completion support; skipping shell completion installation."
    fi
else
    echo "Error: You are not allowed to write to /usr/local/bin. Please install qu manually, or run this script with sudo."
    exit 1
fi

echo "> Creating systemd service file for qu serve..."

    cat <<EOL > "$SERVICE_FILE"
[Unit]
Description=QUptime Serve
After=network.target

[Service]
ExecStart=$INSTALL_BIN serve
Restart=always
User=$SERVICE_USER
Group=$SERVICE_GROUP

[Install]
WantedBy=multi-user.target
EOL
echo_cmd "systemctl daemon-reload"
echo_cmd "systemctl enable $(basename "$SERVICE_FILE")"
echo "> qu serve service has been created and enabled. You can start it with 'systemctl start $(basename "$SERVICE_FILE")'"

echo "Installation complete, before starting `qu serve`, make sure to run `qu init` and read the documentation."
