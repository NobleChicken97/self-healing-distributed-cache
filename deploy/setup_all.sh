#!/bin/bash
# Setup all cache nodes via SSH
# Run this from your local machine after Terraform deployment

set -e

# Node IPs from Terraform output
NODE1_IP="13.126.24.246"
NODE2_IP="13.127.78.189"
NODE3_IP="15.252.208.189"

# SSH key path (Terraform creates this)
SSH_KEY="$HOME/.ssh/cache-cluster-key"
SSH_USER="admin"

echo "=== Setting up cache cluster ==="

# Function to setup a node
setup_node() {
  local NODE_IP=$1
  local NODE_ID=$2
  local PEERS=$3

  echo ""
  echo "=== Setting up $NODE_ID ($NODE_IP) ==="

  # Copy setup script
  scp -i $SSH_KEY -o StrictHostKeyChecking=no setup_node.sh $SSH_USER@$NODE_IP:/tmp/

  # Run setup script
  ssh -i $SSH_KEY -o StrictHostKeyChecking=no $SSH_USER@$NODE_IP \
    "chmod +x /tmp/setup_node.sh && sudo bash /tmp/setup_node.sh $NODE_ID '$PEERS'"

  echo "=== $NODE_ID setup complete ==="
}

# Setup each node
setup_node $NODE1_IP "node-1" "$NODE2_IP:8080,$NODE3_IP:8080"
setup_node $NODE2_IP "node-2" "$NODE1_IP:8080,$NODE3_IP:8080"
setup_node $NODE3_IP "node-3" "$NODE1_IP:8080,$NODE2_IP:8080"

echo ""
echo "=== All nodes setup complete ==="
echo ""
echo "Testing cluster..."

# Test from node 1
echo "Setting test key..."
curl -s -X POST http://$NODE1_IP:8080/set \
  -H "Content-Type: application/json" \
  -d '{"key":"test","value":"hello-cluster"}'

echo ""
echo "Getting test key from node 2..."
curl -s http://$NODE2_IP:8080/get?key=test

echo ""
echo "Getting test key from node 3..."
curl -s http://$NODE3_IP:8080/get?key=test

echo ""
echo "=== Cluster is working! ==="
