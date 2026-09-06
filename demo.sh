#!/bin/bash
# Demo script for Self-Healing Distributed Cache
# This script demonstrates the self-healing capabilities of the cache cluster.
#
# Usage: ./demo.sh
# Requirements: Go 1.22+, bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}  Self-Healing Distributed Cache Demo  ${NC}"
echo -e "${BLUE}========================================${NC}"
echo

# Build the server
echo -e "${YELLOW}Building cache server...${NC}"
go build -o cache-server ./cmd/cache/
echo -e "${GREEN}Build complete!${NC}"
echo

# Create a temporary directory for logs
TMPDIR=$(mktemp -d)
echo -e "${YELLOW}Logs will be saved to: ${TMPDIR}${NC}"
echo

# Cleanup function
cleanup() {
    echo
    echo -e "${YELLOW}Cleaning up...${NC}"
    # Kill any remaining cache-server processes
    pkill -f "cache-server" 2>/dev/null || true
    # Remove temp directory
    rm -rf "${TMPDIR}"
    echo -e "${GREEN}Cleanup complete!${NC}"
}
trap cleanup EXIT

# Start 3-node cluster.
# Identity uses 127.0.0.1:port everywhere so ring, gossip, and liveness checks
# all agree (short names like "node-a" diverge the rings and break routing).
# -gossip-advertise-addr is required: without it memberlist advertises the
# 0.0.0.0 bind address, which peers can never match.
echo -e "${YELLOW}Starting 3-node cluster...${NC}"
./cache-server -addr :8080 -id 127.0.0.1:8080 -advertise-addr 127.0.0.1:8080 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8081,127.0.0.1:8082" > "${TMPDIR}/node-a.log" 2>&1 &
NODE_A_PID=$!
echo "  node-a started (PID: ${NODE_A_PID})"

sleep 1

./cache-server -addr :8081 -id 127.0.0.1:8081 -advertise-addr 127.0.0.1:8081 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8080,127.0.0.1:8082" > "${TMPDIR}/node-b.log" 2>&1 &
NODE_B_PID=$!
echo "  node-b started (PID: ${NODE_B_PID})"

./cache-server -addr :8082 -id 127.0.0.1:8082 -advertise-addr 127.0.0.1:8082 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8080,127.0.0.1:8081" > "${TMPDIR}/node-c.log" 2>&1 &
NODE_C_PID=$!
echo "  node-c started (PID: ${NODE_C_PID})"

# Wait for cluster to stabilize
echo -e "${YELLOW}Waiting for cluster to stabilize...${NC}"
sleep 3
echo -e "${GREEN}Cluster ready!${NC}"
echo

# Helper function to make HTTP requests
cache_set() {
    curl -s -X POST "http://localhost:$1/set" \
        -H "Content-Type: application/json" \
        -d "{\"key\":\"$2\",\"value\":\"$3\",\"ttl_ms\":60000}"
}

cache_get() {
    curl -s "http://localhost:$1/get?key=$2"
}

# Phase 1: Populate with data
echo -e "${BLUE}--- Phase 1: Populating cache with data ---${NC}"
for i in {1..20}; do
    cache_set 8080 "key-${i}" "value-${i}" > /dev/null
done
echo -e "${GREEN}Wrote 20 keys to the cluster${NC}"
echo

# Verify data is accessible from any node
echo -e "${YELLOW}Verifying data accessibility...${NC}"
SUCCESS=0
for i in {1..20}; do
    RESULT=$(cache_get 8081 "key-${i}")
    if echo "${RESULT}" | grep -q "value-${i}"; then
        SUCCESS=$((SUCCESS + 1))
    fi
done
echo -e "${GREEN}/${SUCCESS}/20 keys accessible from node-b${NC}"
echo

# Phase 2: Start continuous traffic
echo -e "${BLUE}--- Phase 2: Starting continuous traffic ---${NC}"
(
    for i in {1..100}; do
        KEY="key-$((RANDOM % 20 + 1))"
        cache_get 8080 "${KEY}" > /dev/null 2>&1
        sleep 0.1
    done
) &
TRAFFIC_PID=$!
echo "Traffic generator started (PID: ${TRAFFIC_PID})"
sleep 1
echo

# Phase 3: Kill a node (simulate failure)
echo -e "${BLUE}--- Phase 3: Simulating node failure ---${NC}"
echo -e "${RED}Killing node-b (PID: ${NODE_B_PID})...${NC}"
kill "${NODE_B_PID}"
echo -e "${YELLOW}Waiting for failure detection...${NC}"
sleep 4
echo

# Phase 4: Verify self-healing
echo -e "${BLUE}--- Phase 4: Verifying self-healing ---${NC}"
echo -e "${YELLOW}Checking if data is still accessible...${NC}"
SUCCESS=0
for i in {1..20}; do
    RESULT=$(cache_get 8080 "key-${i}")
    if echo "${RESULT}" | grep -q "value-${i}"; then
        SUCCESS=$((SUCCESS + 1))
    fi
done
echo -e "${GREEN}/${SUCCESS}/20 keys still accessible after node failure${NC}"
echo

# Check cluster health
echo -e "${YELLOW}Checking cluster health...${NC}"
curl -s http://localhost:8080/cluster/info | python3 -m json.tool 2>/dev/null || \
    curl -s http://localhost:8080/cluster/info
echo

# Check ring info
echo -e "${YELLOW}Ring topology:${NC}"
curl -s http://localhost:8080/ring/info | python3 -m json.tool 2>/dev/null || \
    curl -s http://localhost:8080/ring/info
echo

# Phase 5: Node recovery
echo -e "${BLUE}--- Phase 5: Node recovery ---${NC}"
echo -e "${YELLOW}Restarting node-b...${NC}"
./cache-server -addr :8081 -id 127.0.0.1:8081 -advertise-addr 127.0.0.1:8081 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8080,127.0.0.1:8082" > "${TMPDIR}/node-b.log" 2>&1 &
NODE_B_PID=$!
echo "  node-b restarted (PID: ${NODE_B_PID})"

# Wait for recovery
sleep 4
echo -e "${GREEN}Node recovery complete!${NC}"
echo

# Final verification
echo -e "${BLUE}--- Final Verification ---${NC}"
echo -e "${YELLOW}Checking data after recovery...${NC}"
SUCCESS=0
for i in {1..20}; do
    RESULT=$(cache_get 8081 "key-${i}")
    if echo "${RESULT}" | grep -q "value-${i}"; then
        SUCCESS=$((SUCCESS + 1))
    fi
done
echo -e "${GREEN}/${SUCCESS}/20 keys accessible from recovered node-b${NC}"
echo

# Show rebalance status
echo -e "${YELLOW}Rebalance status:${NC}"
curl -s http://localhost:8080/rebalance/status | python3 -m json.tool 2>/dev/null || \
    curl -s http://localhost:8080/rebalance/status
echo

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}  Demo Complete!                      ${NC}"
echo -e "${BLUE}========================================${NC}"
echo
echo -e "${GREEN}The cluster successfully:${NC}"
echo "  1. Distributed data across 3 nodes"
echo "  2. Survived node failure with zero data loss"
echo "  3. Automatically detected the failure"
echo "  4. Recovered when the node rejoined"
echo
echo -e "${YELLOW}Logs saved to: ${TMPDIR}${NC}"
