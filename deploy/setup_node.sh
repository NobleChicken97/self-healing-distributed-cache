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

# Determine peers based on node ID
if [ -z "$PEERS" ]; then
  case $NODE_ID in
    node-1)
      PEERS="${NODE2_IP:-13.127.78.189:8080},${NODE3_IP:-15.252.208.189:8080}"
      ;;
    node-2)
      PEERS="${NODE1_IP:-13.126.24.246:8080},${NODE3_IP:-15.252.208.189:8080}"
      ;;
    node-3)
      PEERS="${NODE1_IP:-13.126.24.246:8080},${NODE2_IP:-13.127.78.189:8080}"
      ;;
  esac
fi

# Run cache server
echo "Starting cache server..."
echo "Peers: $PEERS"

sudo docker run -d \
  --name cache-server \
  --restart always \
  -p 8080:8080 \
  -p 7946:7946 \
  cache-server:latest \
  -addr :8080 \
  -id $NODE_ID \
  -peers "$PEERS"

echo "=== $NODE_ID setup complete ==="
echo "Cache server running on port 8080"

# Verify
sleep 3
sudo docker ps
curl -s http://localhost:8080/ring/info | head -c 200
echo ""
