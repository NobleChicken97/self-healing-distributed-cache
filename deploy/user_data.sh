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

# Determine peers and own IP based on node ID
case $NODE_ID in
  node-1)
    SELF_IP="$NODE1_IP"
    PEERS="$NODE2_IP:8080,$NODE3_IP:8080"
    ;;
  node-2)
    SELF_IP="$NODE2_IP"
    PEERS="$NODE1_IP:8080,$NODE3_IP:8080"
    ;;
  node-3)
    SELF_IP="$NODE3_IP"
    PEERS="$NODE1_IP:8080,$NODE2_IP:8080"
    ;;
  *)
    PEERS=""
    ;;
esac

# NOTE: Terraform does not attach this script to the Lightsail instances
# (see main.tf); the GitHub Actions pipeline is the canonical deploy path.
# Kept as a manual fallback — flag set mirrors pipeline.yml.

# Run cache server (host networking: SWIM UDP probes must not traverse
# Docker bridge NAT; IP:port identity keeps ring/gossip/liveness in sync)
docker run -d \
  --name cache-server \
  --restart always \
  --network host \
  cache-server \
  -addr :8080 \
  -cluster-port 7946 \
  -gossip-advertise-addr "$SELF_IP" \
  -id "$SELF_IP:8080" \
  -advertise-addr "$SELF_IP:8080" \
  -peers "$PEERS"

echo "=== $NODE_ID setup complete ==="
