#!/bin/bash
set -euo pipefail

# Setup script for go-bench-away deployment on Debian
# Run as root or with sudo

NATS_VERSION="${1:-2.10.24}"

echo "=== go-bench-away Deployment Setup ==="
echo "NATS Server version: ${NATS_VERSION}"
echo ""

# Create users
echo "--- Creating service users ---"
id -u nats &>/dev/null || useradd -r -s /usr/sbin/nologin nats
id -u bench &>/dev/null || useradd -r -m -s /bin/bash bench

# Create directories
echo "--- Creating directories ---"
mkdir -p /etc/nats
mkdir -p /var/lib/nats/jetstream
mkdir -p /var/log/nats
chown -R nats:nats /var/lib/nats
chown -R nats:nats /var/log/nats

# Install NATS Server
echo "--- Installing NATS Server v${NATS_VERSION} ---"
NATS_URL="https://github.com/nats-io/nats-server/releases/download/v${NATS_VERSION}/nats-server-v${NATS_VERSION}-linux-amd64.tar.gz"
TMP_DIR=$(mktemp -d)
curl -sL "$NATS_URL" -o "$TMP_DIR/nats-server.tar.gz"
tar xzf "$TMP_DIR/nats-server.tar.gz" -C "$TMP_DIR"
cp "$TMP_DIR"/nats-server-v${NATS_VERSION}-linux-amd64/nats-server /usr/local/bin/nats-server
chmod +x /usr/local/bin/nats-server
rm -rf "$TMP_DIR"
echo "Installed: $(nats-server --version)"

# Install config and services
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "--- Installing config ---"
cp "$SCRIPT_DIR/nats-server.conf" /etc/nats/nats-server.conf

echo "--- Installing systemd services ---"
cp "$SCRIPT_DIR/nats-server.service" /etc/systemd/system/
cp "$SCRIPT_DIR/go-bench-away-worker.service" /etc/systemd/system/
cp "$SCRIPT_DIR/go-bench-away-web.service" /etc/systemd/system/

# Install go-bench-away binary if present
if [ -f "$SCRIPT_DIR/../go-bench-away-linux-amd64" ]; then
    echo "--- Installing go-bench-away binary ---"
    cp "$SCRIPT_DIR/../go-bench-away-linux-amd64" /usr/local/bin/go-bench-away
    chmod +x /usr/local/bin/go-bench-away
fi

systemctl daemon-reload

echo ""
echo "=== Setup Complete ==="
echo ""
echo "Next steps:"
echo "  1. Start NATS:    sudo systemctl start nats-server"
echo "  2. Verify:        sudo systemctl status nats-server"
echo "  3. Start worker:  sudo systemctl start go-bench-away-worker"
echo "  4. Start web:     sudo systemctl start go-bench-away-web"
echo "  5. Enable on boot:"
echo "     sudo systemctl enable nats-server"
echo "     sudo systemctl enable go-bench-away-worker"
echo "     sudo systemctl enable go-bench-away-web"
echo ""
echo "Progressive NATS upgrade path:"
echo "  Current EC2: v2.9.16"
echo "  Safe upgrade order: 2.9.x -> 2.10.x -> 2.11.x"
echo "  Re-run: sudo ./setup.sh 2.11.4  (to upgrade)"
echo "  NATS supports hot reload: sudo systemctl reload nats-server"
echo ""
echo "To migrate data from EC2:"
echo "  On EC2:  nats -s nats://0283509234734458@localhost stream backup <stream> ./backup/"
echo "  On new:  nats -s nats://0283509234734458@localhost stream restore <stream> ./backup/"
