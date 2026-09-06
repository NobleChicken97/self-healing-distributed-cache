#!/bin/bash
# Setup script for cache node
# Run this on each Lightsail instance after Terraform deployment

set -e

NODE_ID="${1:-node-1}"
PEERS="${2:-}"

echo "=== Setting up $NODE_ID ==="

# Update system
echo "Updating system..."
sudo apt-get update -y
sudo apt-get install -y curl wget git

# Install Docker
echo "Installing Docker..."
curl -fsSL https://get.docker.com | sh
sudo systemctl enable docker
sudo systemctl start docker

# Add current user to docker group
sudo usermod -aG docker $USER

# Wait for Docker
sleep 5

# Clone and build cache server
echo "Building cache server..."
cd /opt
sudo git clone https://github.com/NobleChicken97/self-healing-distributed-cache.git cache
cd cache
sudo docker build -t cache-server:latest .

# Determine peers and identity based on node ID.
# Node identity uses IP:port everywhere so ring, gossip, and liveness checks
# all agree (see pipeline.yml deploy job for the canonical flag set).
if [ -z "$PEERS" ]; then
  case $NODE_ID in
    node-1)
      SELF_IP="${NODE1_IP:-13.126.24.246}"
      PEERS="${NODE2_IP:-13.127.78.189:8080},${NODE3_IP:-15.252.208.189:8080}"
      ;;
    node-2)
      SELF_IP="${NODE2_IP:-13.127.78.189}"
      PEERS="${NODE1_IP:-13.126.24.246:8080},${NODE3_IP:-15.252.208.189:8080}"
      ;;
    node-3)
      SELF_IP="${NODE3_IP:-15.252.208.189}"
      PEERS="${NODE1_IP:-13.126.24.246:8080},${NODE2_IP:-13.127.78.189:8080}"
      ;;
  esac
fi
SELF_IP="${SELF_IP:-127.0.0.1}"

# Run cache server (host networking: SWIM UDP probes must not traverse
# Docker bridge NAT)
echo "Starting cache server..."
echo "Peers: $PEERS"

sudo docker run -d \
  --name cache-server \
  --restart always \
  --network host \
  cache-server:latest \
  -addr :8080 \
  -cluster-port 7946 \
  -gossip-advertise-addr "$SELF_IP" \
  -id "${SELF_IP}:8080" \
  -advertise-addr "${SELF_IP}:8080" \
  -peers "$PEERS"

echo "=== $NODE_ID setup complete ==="
echo "Cache server running on port 8080"

# Verify
sleep 3
sudo docker ps
curl -s http://localhost:8080/ring/info | head -c 200
echo ""
