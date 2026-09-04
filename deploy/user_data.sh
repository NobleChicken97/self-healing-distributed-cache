#!/bin/bash
# User data script for AWS Lightsail cache nodes
# This runs automatically on first boot

set -e

NODE_ID="${node_id}"
NODE1_IP="${node1_ip}"
NODE2_IP="${node2_ip}"
NODE3_IP="${node3_ip}"

echo "=== Starting setup for $NODE_ID ==="

# Update system
apt-get update -y
apt-get install -y curl wget git

# Install Docker
curl -fsSL https://get.docker.com | sh
systemctl enable docker
systemctl start docker

# Wait for Docker to be ready
sleep 10

# Clone and build cache server
cd /opt
git clone https://github.com/NobleChicken97/self-healing-distributed-cache.git cache
cd cache
docker build -t cache-server .

# Determine peers based on node ID
case $NODE_ID in
  node-1)
    PEERS="$NODE2_IP:8080,$NODE3_IP:8080"
    ;;
  node-2)
    PEERS="$NODE1_IP:8080,$NODE3_IP:8080"
    ;;
  node-3)
    PEERS="$NODE1_IP:8080,$NODE2_IP:8080"
    ;;
  *)
    PEERS=""
    ;;
esac

# Run cache server
docker run -d \
  --name cache-server \
  --restart always \
  -p 8080:8080 \
  -p 7946:7946 \
  cache-server \
  -addr :8080 \
  -id $NODE_ID \
  -peers "$PEERS"

echo "=== $NODE_ID setup complete ==="
