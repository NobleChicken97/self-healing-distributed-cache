#!/bin/bash
# Deploy cache server to Lightsail instances
# Usage: ./deploy.sh <image_tag>
# Example: ./deploy.sh latest
# Example: ./deploy.sh sha-abc1234

set -e

IMAGE_TAG="${1:-latest}"
AWS_REGION="ap-south-1"
AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
ECR_REGISTRY="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"
ECR_REPOSITORY="self-healing-cache"
FULL_IMAGE="${ECR_REGISTRY}/${ECR_REPOSITORY}:${IMAGE_TAG}"

# Node IPs (update these to match your Terraform outputs)
NODE1_IP="${LIGHTSAIL_NODE1_IP:-13.126.24.246}"
NODE2_IP="${LIGHTSAIL_NODE2_IP:-13.127.78.189}"
NODE3_IP="${LIGHTSAIL_NODE3_IP:-15.252.208.189}"
SSH_KEY="${LIGHTSAIL_SSH_KEY:-$HOME/.ssh/cache-cluster-key}"
SSH_USER="admin"

echo "=== Deploying ${FULL_IMAGE} ==="

# Function to deploy to a node
deploy_node() {
  local NODE_IP=$1
  local NODE_ID=$2
  local PEERS=$3

  echo ""
  echo "=== Deploying to ${NODE_ID} (${NODE_IP}) ==="

  ssh -i "$SSH_KEY" -o StrictHostKeyChecking=no -o ConnectTimeout=10 "${SSH_USER}@${NODE_IP}" << EOF
    set -e

    # Login to ECR
    aws ecr get-login-password --region ${AWS_REGION} | docker login --username AWS --password-stdin ${ECR_REGISTRY}

    # Pull new image
    docker pull ${FULL_IMAGE}

    # Stop and remove old container
    docker stop cache-server 2>/dev/null || true
    docker rm cache-server 2>/dev/null || true

    # Run new container
    docker run -d \
      --name cache-server \
      --restart always \
      -p 8080:8080 \
      -p 7946:7946 \
      ${FULL_IMAGE} \
      -addr :8080 \
      -id ${NODE_ID} \
      -peers "${PEERS}"

    # Verify
    sleep 3
    docker ps | grep cache-server
    curl -s http://localhost:8080/ring/info | head -c 100
    echo ""

    echo "=== ${NODE_ID} deployed successfully ==="
EOF
}

# Deploy to all nodes
deploy_node "$NODE1_IP" "node-1" "${NODE2_IP}:8080,${NODE3_IP}:8080"
deploy_node "$NODE2_IP" "node-2" "${NODE1_IP}:8080,${NODE3_IP}:8080"
deploy_node "$NODE3_IP" "node-3" "${NODE1_IP}:8080,${NODE2_IP}:8080"

echo ""
echo "=== Deployment complete! ==="
echo ""
echo "Test with:"
echo "  curl -X POST http://${NODE1_IP}:8080/set -H 'Content-Type: application/json' -d '{\"key\":\"test\",\"value\":\"hello\"}'"
echo "  curl http://${NODE2_IP}:8080/get?key=test"
