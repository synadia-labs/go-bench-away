#!/bin/bash
set -euo pipefail

# Utility Script for go-bench-away
# Usage: ./scripts/util.sh [command]

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { printf "%b[INFO]%b %s\n" "${GREEN}" "${NC}" "$1"; }
log_warn() { printf "%b[WARN]%b %s\n" "${YELLOW}" "${NC}" "$1"; }
log_err() { printf "%b[ERROR]%b %s\n" "${RED}" "${NC}" "$1"; }

check_binary() {
    local binary="$1"
    if ! command -v "$binary" &> /dev/null; then
        log_err "Required binary '$binary' not found in PATH."
        return 1
    else
        log_info "Found '$binary' at $(command -v "$binary")"
        return 0
    fi
}

install_git() {
    log_info "Git is missing."
    
    if [ ! -t 0 ]; then
        log_warn "Non-interactive terminal detected. Skipping automatic git installation."
        return 1
    fi

    read -p "Do you want to attempt automatic installation? [y/N] " -n 1 -r
    echo
    if [[ ! "$REPLY" =~ ^[Yy]$ ]]; then
        return 1
    fi

    if [[ "$OSTYPE" == "linux-gnu"* ]]; then
        if command -v apt-get &> /dev/null; then
            log_info "Detected apt-get. Installing git..."
            sudo apt-get update && sudo apt-get install -y git
        else
             log_warn "Could not detect a supported package manager (apt). Please install git manually."
             return 1
        fi
    elif [[ "$OSTYPE" == "darwin"* ]]; then
        log_warn "On macOS, please install git via Homebrew ('brew install git') or Xcode tools."
        return 1
    else
        log_warn "Unsupported OS for auto-installation. Please install git manually."
        return 1
    fi
}

install_go() {
    log_info "Go is missing."
    
    if [ ! -t 0 ]; then
        log_warn "Non-interactive terminal detected. Skipping automatic Go installation."
        return 1
    fi

    read -p "Do you want to attempt automatic installation (Go 1.25.1)? [y/N] " -n 1 -r
    echo
    if [[ ! "$REPLY" =~ ^[Yy]$ ]]; then
        return 1
    fi

    if [[ "$OSTYPE" == "linux-gnu"* ]]; then
        log_info "Detected Linux. Downloading Go 1.25.1..."
        local go_url="https://go.dev/dl/go1.25.1.linux-amd64.tar.gz"
        local tmp_dir
        tmp_dir=$(mktemp -d)
        
        if curl -L "$go_url" -o "$tmp_dir/go.tar.gz"; then
            log_info "Download complete. Extracting to /usr/local/go..."
            # Remove existing installation if present to avoid conflicts/leftovers
            sudo rm -rf /usr/local/go
            if sudo tar -C /usr/local -xzf "$tmp_dir/go.tar.gz"; then
                log_info "Go installed to /usr/local/go"
                # Suggest path update
                log_warn "Please add /usr/local/go/bin to your PATH."
                export PATH="$PATH:/usr/local/go/bin"
            else
                log_err "Failed to extract Go."
                return 1
            fi
        else
            log_err "Failed to download Go."
            return 1
        fi
        rm -rf "$tmp_dir"
    elif [[ "$OSTYPE" == "darwin"* ]]; then
        log_warn "On macOS, please install Go via Homebrew ('brew install go') or the official installer."
        return 1
    else
        log_warn "Unsupported OS for auto-installation. Please install Go manually."
        return 1
    fi
}

check_prereqs() {
    log_info "Checking system prerequisites..."
    local failed=0

    # Check Go
    if ! check_binary "go"; then
        if ! install_go; then
            failed=1
        fi
    else
        local go_version
        go_version=$(go version)
        log_info "Go version: $go_version"
    fi

    # Check Git
    if ! check_binary "git"; then
        if ! install_git; then
            failed=1
        fi
    else
        local git_version
        git_version=$(git --version)
        log_info "Git version: $git_version"
    fi

    # Check NATS Server connectivity (simple TCP check)
    log_info "Checking NATS connectivity..."
    # If users provide NATS URL via env var, verify it.
    local nats_url="${NATS_URL:-localhost}"
    local nats_port="${NATS_PORT:-4222}"
    
    # Simple nc check if available, else warn
    if command -v nc &> /dev/null; then
        if nc -z -w 2 "$nats_url" "$nats_port" 2>/dev/null; then
             log_info "NATS server reachable at $nats_url:$nats_port"
        else
             log_warn "Could not connect to NATS at $nats_url:$nats_port (Is it running?)"
        fi
    else
        log_warn "'nc' not found, skipping network connectivity check."
    fi

    if [ "$failed" -eq 1 ]; then
        log_err "Prerequisite checks failed. Please install missing dependencies."
        exit 1
    fi
    log_info "All prerequisite checks passed."
}

perform_backup() {
    log_info "Starting localized backup procedure..."
    local backup_dir
    backup_dir="migration_backup_$(date +%Y%m%d_%H%M%S)"
    mkdir -p "$backup_dir"

    # 1. Capture Environment
    log_info "Saving environment variables to $backup_dir/env.txt"
    env | grep -E "NATS_|GO_|GIT_" > "$backup_dir/env.txt" || true

    # 2. Capture Binary Paths
    {
        printf "GO_BIN=%s\n" "$(command -v go)"
        printf "GIT_BIN=%s\n" "$(command -v git)"
    } > "$backup_dir/binaries.txt"

    # 3. NATS JetStream Backup (CLI-based)
    if command -v nats &> /dev/null; then
        log_info "Detected 'nats' CLI. Attempting hot backup of JetStream streams..."
        local js_backup_dir="$backup_dir/jetstream"
        mkdir -p "$js_backup_dir"
        
        # Get list of all streams (including KV/Obj backing streams)
        # We try to use the configured context or fall back to default connection
        local streams
        if streams=$(nats stream ls -a -n 2>/dev/null); then
            if [ -z "$streams" ]; then
                log_warn "No streams found to backup."
            else
                echo "$streams" | while read -r stream; do
                    if [ -n "$stream" ]; then
                        log_info "Backing up stream: $stream"
                        if nats stream backup "$stream" "$js_backup_dir/$stream" &> /dev/null; then
                            log_info "  Success."
                        else
                            log_err "  Failed to backup stream '$stream'."
                        fi
                    fi
                done
            fi
        else
             log_warn "Failed to list streams. Check NATS connectivity/credentials."
        fi
    else
        log_info "'nats' CLI not found. Skipping hot backup."
    fi

    # 4. Best-effort JetStream Data Backup (Filesystem-based)
    # Attempt to find running nats-server and its config
    if pgrep nats-server > /dev/null; then
        log_info "Detected running nats-server."
        # Try to find store_dir from config or arguments (highly heuristic)
        # MacOS/Linux ps command variance makes this tricky conformantly, focusing on common cases
        local pid
        pid=$(pgrep nats-server | head -n 1)
        
        # Try to find config file from process args
        local cmdline
        if [ -f "/proc/$pid/cmdline" ]; then
             cmdline=$(tr '\0' ' ' < "/proc/$pid/cmdline")
        else
             # Fallback for some macOS/BSD
             cmdline=$(ps -p "$pid" -o command=)
        fi

        log_info "NATS Server command line: $cmdline"
        
        # Look for -c or --config or -sd or --store_dir
        # This is a simple grep, not a full arg parser
        local config_file=""
        if [[ "$cmdline" =~ -c[[:space:]]+([^[:space:]]+) ]]; then
            config_file="${BASH_REMATCH[1]}"
        elif [[ "$cmdline" =~ --config[[:space:]]+([^[:space:]]+) ]]; then
            config_file="${BASH_REMATCH[1]}"
        fi

        if [ -n "$config_file" ] && [ -f "$config_file" ]; then
             log_info "Found config file: $config_file"
             cp "$config_file" "$backup_dir/nats-server.conf"
             
             # Attempt to read store_dir from config
             local store_dir
             store_dir=$(grep "store_dir" "$config_file" | awk '{print $2}' | tr -d '";')
             if [ -n "$store_dir" ]; then
                 # Handle relative paths (relative to config file location usually, or CWD of server)
                 # We'll just warn the user to back it up if it's not absolute or obvious
                 log_info "Configured store_dir: $store_dir"
                 log_warn "Please manually backup the directory '$store_dir' if it contains critical data."
             fi
        else
            log_warn "Could not pinpoint nats-server config file automatically."
        fi
        
    else
        log_warn "No running nats-server detected. Skipping NATS data backup."
    fi

    # Create tarball
    local tarball="${backup_dir}.tar.gz"
    if [ -d "$backup_dir" ]; then
        tar -czf "$tarball" -C "$(dirname "$backup_dir")" "$(basename "$backup_dir")"
        rm -rf "$backup_dir"
    fi
    
    log_info "Backup complete: $tarball"
    log_info "Transfer this archive to your new host."
}

# Main
cmd="${1:-help}"

case "$cmd" in
    check)
        check_prereqs
        ;;
    backup)
        check_prereqs
        perform_backup
        ;;
    help|--help|-h)
        printf "Usage: %s [command]\n\n" "$0"
        printf "Commands:\n"
        printf "  check   - Verify system prerequisites (Go, Git, NATS connectivity)\n"
        printf "  backup  - Verify prerequisites and create a migration backup archive (env vars, config)\n"
        printf "  help    - Show this help message\n\n"
        printf "Description:\n"
        printf "  This utility script assists with setting up and migrating go-bench-away environments.\n"
        printf "  Use 'check' to validate a new host, and 'backup' to prepare for migration from an existing host.\n"
        ;;
    *)
        log_err "Unknown command: $cmd"
        "$0" help
        exit 1
        ;;
esac
