# Deployment Guide

This guide covers various deployment options for SHDC,
from local development to production cloud deployments.

## Table of Contents

1. [Quick Start (Docker Compose)](#quick-start-docker-compose)
2. [Single Binary Deployment](#single-binary-deployment)
3. [Docker Deployment](#docker-deployment)
4. [Kubernetes Deployment](#kubernetes-deployment)
5. [Cloud Deployment Options](#cloud-deployment-options)
6. [Production Considerations](#production-considerations)

---

## Quick Start (Docker Compose)

The fastest way to run a local cluster:

```bash
# Clone the repository
git clone https://github.com/yourusername/self-healing-distributed-cache.git
cd self-healing-distributed-cache

# Start a 3-node cluster
docker-compose up -d

# Verify the cluster is running
curl http://localhost:8080/ring/info
curl http://localhost:8081/ring/info
curl http://localhost:8082/ring/info

# Set a key (routes automatically to the correct node)
curl -X POST http://localhost:8080/set \
  -H "Content-Type: application/json" \
  -d '{"key":"hello","value":"world"}'

# Get the key (works from any node)
curl http://localhost:8081/get?key=hello

# View logs
docker-compose logs -f node-a

# Stop the cluster
docker-compose down
```

### Monitoring

`/metrics` returns JSON (not Prometheus exposition format) — scrape it with
`curl`/`jq`. Native Prometheus/Grafana support is future work; there is
currently no monitoring compose profile.

---

## Single Binary Deployment

For simple deployments or edge computing:

### Build

```bash
# Build for current platform
go build -o cache-server ./cmd/cache/
go build -o cache-client ./cmd/cache-client/

# Cross-compile for Linux AMD64
GOOS=linux GOARCH=amd64 go build -o cache-server-linux ./cmd/cache/

# Cross-compile for ARM64 (Raspberry Pi, AWS Graviton)
GOOS=linux GOARCH=arm64 go build -o cache-server-arm64 ./cmd/cache/
```

### Run

```bash
# Terminal 1: Seed node
./cache-server -addr :8080 -id 127.0.0.1:8080 -advertise-addr 127.0.0.1:8080 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8081,127.0.0.1:8082"

# Terminal 2: Join via node-a
./cache-server -addr :8081 -id 127.0.0.1:8081 -advertise-addr 127.0.0.1:8081 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8080,127.0.0.1:8082"

# Terminal 3: Join via node-a
./cache-server -addr :8082 -id 127.0.0.1:8082 -advertise-addr 127.0.0.1:8082 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8080,127.0.0.1:8081"
```

Identity must use `host:port` everywhere: every node builds its ring from the
same ID set and gossip liveness is checked against those IDs. Short names
(`-id node-a`) diverge the rings and break routing. Gossip ports default to
HTTP+1000 per node (no `-cluster-port` needed locally); seeds are derived
from the peer HTTP addresses automatically.

### Systemd Service (Linux)

Create `/etc/systemd/system/shdc-node-a.service`:

```ini
[Unit]
Description=SHDC Node A
After=network.target

[Service]
Type=simple
User=cache
WorkingDirectory=/opt/cache
ExecStart=/opt/cache/cache-server -addr :8080 -id 127.0.0.1:8080 -advertise-addr 127.0.0.1:8080 -gossip-advertise-addr 127.0.0.1 -cluster-port 7946
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable shdc-node-a
sudo systemctl start shdc-node-a
```

---

## Docker Deployment

### Build Image

```bash
docker build -t shdc:latest .
```

### Run Single Node

```bash
docker run -d \
  --name shdc-node \
  -p 8080:8080 \
  -p 7946:7946 \
  shdc:latest \
  -addr :8080 -id 127.0.0.1:8080
```

### Run Multi-Node Cluster with Docker Network

Identity must use `host:port` on every node so rings agree (see the
single-binary section above). Gossip uses the shared `-cluster-port` 7946.

```bash
# Create network
docker network create shdc-net

# Start nodes
docker run -d --name shdc-a --network shdc-net \
  -p 8080:8080 \
  shdc:latest \
  -addr :8080 -id node-a:8080 -advertise-addr node-a:8080 -gossip-advertise-addr node-a -cluster-port 7946

docker run -d --name shdc-b --network shdc-net \
  -p 8081:8080 \
  shdc:latest \
  -addr :8080 -id node-b:8080 -advertise-addr node-b:8080 -gossip-advertise-addr node-b -cluster-port 7946 -peers "node-a:8080"

docker run -d --name shdc-c --network shdc-net \
  -p 8082:8080 \
  shdc:latest \
  -addr :8080 -id node-c:8080 -advertise-addr node-c:8080 -gossip-advertise-addr node-c -cluster-port 7946 -peers "node-a:8080"
```

---

## Kubernetes Deployment

### Prerequisites

- Kubernetes cluster (1.20+)
- kubectl configured
- Container registry access

### Container Registry

Push your image to a registry:

```bash
# GitHub Container Registry
docker tag self-healing-cache:latest ghcr.io/yourusername/cache:latest
docker push ghcr.io/yourusername/cache:latest

# Docker Hub
docker tag self-healing-cache:latest yourusername/cache:latest
docker push yourusername/cache:latest
```

### Kubernetes Manifests

Create `deploy/kubernetes/` directory with the following files:

#### Namespace

```yaml
# deploy/kubernetes/namespace.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: shdc-cluster
```

#### Headless Service (for gossip protocol)

```yaml
# deploy/kubernetes/service.yaml
apiVersion: v1
kind: Service
metadata:
  name: shdc-headless
  namespace: shdc-cluster
spec:
  clusterIP: None
  selector:
    app: shdc-node
  ports:
    - name: http
      port: 8080
      targetPort: 8080
    - name: gossip
      port: 7946
      targetPort: 7946
    - name: gossip-udp # SWIM probes run over UDP on the same port
      protocol: UDP
      port: 7946
      targetPort: 7946
```

#### StatefulSet

```yaml
# deploy/kubernetes/statefulset.yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: shdc-node
  namespace: shdc-cluster
spec:
  serviceName: shdc-headless
  replicas: 3
  selector:
    matchLabels:
      app: shdc-node
  template:
    metadata:
      labels:
        app: shdc-node
    spec:
      containers:
        - name: cache
          image: ghcr.io/yourusername/cache:latest
          # Identity MUST be host:port on every node (ring/gossip/liveness
          # namespaces must agree — short names diverge the rings).
          args:
            - "-addr=:8080"
            - "-id=$(POD_NAME).shdc-headless:8080"
            - "-advertise-addr=$(POD_NAME).shdc-headless:8080"
            - "-cluster-port=7946"
            - "-gossip-advertise-addr=$(POD_NAME).shdc-headless"
            - "-peers=shdc-node-0.shdc-headless:8080,shdc-node-1.shdc-headless:8080,shdc-node-2.shdc-headless:8080"
          env:
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
          ports:
            - name: http
              containerPort: 8080
            - name: gossip
              containerPort: 7946
            - name: gossip-udp
              protocol: UDP
              containerPort: 7946
          readinessProbe:
            httpGet:
              path: /ring/info
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            httpGet:
              path: /ring/info
              port: 8080
            initialDelaySeconds: 15
            periodSeconds: 20
          resources:
            requests:
              memory: "64Mi"
              cpu: "100m"
            limits:
              memory: "256Mi"
              cpu: "500m"
---
# deploy/kubernetes/ingress.yaml
apiVersion: v1
kind: Service
metadata:
  name: shdc-lb
  namespace: shdc-cluster
spec:
  type: LoadBalancer
  selector:
    app: shdc-node
  ports:
    - name: http
      port: 80
      targetPort: 8080
```

### Deploy to Kubernetes

```bash
# Apply manifests
kubectl apply -f deploy/kubernetes/

# Verify pods are running
kubectl get pods -n shdc-cluster -w

# Check logs
kubectl logs -n shdc-cluster shdc-node-0 -f

# Port-forward for local access
kubectl port-forward -n shdc-cluster svc/shdc-lb 8080:80

# Scale the cluster
kubectl scale statefulset shdc-node -n shdc-cluster --replicas=5
```

---

## Cloud Deployment Options

### AWS

#### EC2 / ECS

```bash
# Using AWS CLI to create ECS cluster
aws ecs create-cluster --cluster-name shdc-cluster

# Deploy with ECS CLI or CloudFormation
# See: deploy/aws/ecs-service.json
```

#### EKS (Elastic Kubernetes Service)

```bash
# Create EKS cluster
eksctl create cluster --name shdc-cluster --nodes 3

# Deploy
kubectl apply -f deploy/kubernetes/
```

#### AWS Fargate (Serverless Containers)

```yaml
# deploy/aws/fargate-task.json
{
  "family": "shdc-node",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "256",
  "memory": "512",
  "containerDefinitions": [
    {
      "name": "cache",
      "image": "yourusername/cache:latest",
      "portMappings": [
        { "containerPort": 8080 },
        { "containerPort": 7946 }
      ]
    }
  ]
}
```

### Google Cloud Platform

#### GKE (Google Kubernetes Engine)

```bash
# Create GKE cluster
gcloud container clusters create shdc-cluster --num-nodes=3

# Deploy
kubectl apply -f deploy/kubernetes/
```

#### Cloud Run (Serverless)

```bash
# Deploy to Cloud Run (stateless, single node)
gcloud run deploy shdc-node \
  --image ghcr.io/yourusername/cache:latest \
  --port 8080 \
  --platform managed
```

### Azure

#### AKS (Azure Kubernetes Service)

```bash
# Create AKS cluster
az aks create --resource-group myRG --name shdc-cluster --node-count 3

# Deploy
kubectl apply -f deploy/kubernetes/
```

#### Azure Container Instances

```bash
# Deploy container group
az container create \
  --resource-group myRG \
  --name shdc-node \
  --image yourusername/cache:latest \
  --ports 8080 7946
```

---

## Production Considerations

### Security

1. **Network Security**
   - Use VPC/network policies to isolate cluster traffic
   - Enable TLS for HTTP API (reverse proxy with nginx/traefik)
   - Restrict gossip port (7946) to internal traffic only

2. **Authentication**
   - Add API key or JWT authentication for client requests
   - Use mTLS for inter-node communication

3. **Secrets Management**
   - Store credentials in Kubernetes Secrets, AWS Secrets Manager, or HashiCorp Vault
   - Never hardcode secrets in container images

### Observability

1. **Metrics**
   - Expose Prometheus metrics endpoint
   - Track: request latency, error rates, cache hit ratio, memory usage
   - Set up Grafana dashboards

2. **Logging**
   - Structured JSON logging
   - Centralized logging with ELK/Loki/CloudWatch

3. **Tracing**
   - Add distributed tracing with OpenTelemetry/Jaeger
   - Track request flow across nodes

### High Availability

1. **Replication Factor**
   - Default: 2 (survives 1 node failure)
   - Production: 3 (survives 2 node failures)

2. **Multi-AZ Deployment**
   - Spread nodes across availability zones
   - Use topology-aware replication

3. **Backup & Recovery**
   - Periodic snapshots for persistence
   - Cross-region replication for disaster recovery

### Performance Tuning

1. **Resource Allocation**
   - CPU: 0.5-2 cores per node
   - Memory: 256MB-2GB per node (depending on dataset size)
   - Network: Ensure low latency between nodes (<1ms ideal)

2. **Cache Configuration**
   - Set appropriate TTLs for your workload
   - Configure memory caps with LRU eviction
   - Use quorum mode for critical data

3. **Connection Pooling**
   - Reuse HTTP connections between nodes
   - Tune `MaxIdleConns` and `MaxIdleConnsPerHost`

### Scaling

1. **Horizontal Scaling**
   - Add nodes to the ring for more capacity
   - Rebalancing happens automatically
   - Consider ~1/N key movement when adding nodes

2. **Vertical Scaling**
   - Increase memory cap for larger datasets
   - More CPU for higher throughput

3. **Read Scaling**
   - Add more replicas for read-heavy workloads
   - Use quorum reads for consistency

---

## Configuration Reference

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `:8080` | HTTP listen address |
| `-id` | `<addr>` | Unique node ID — use `host:port` on every node so rings agree |
| `-advertise-addr` | `<addr>` | Address advertised in ring (reachable self address) |
| `-peers` | `` | Comma-separated peer HTTP addresses |
| `-cluster-port` | `<port>+1000` | Gossip protocol port (0 = auto) |
| `-gossip-advertise-addr` | `` | Host peers dial for gossip (required behind NAT/Docker) |
| `-mem-cap` | `0` | Per-node memory cap in bytes for LRU eviction (0 = unlimited) |

### Environment Variables

None — all configuration is via command-line flags (see Configuration
Reference above). Earlier revisions of this guide listed `CACHE_*` variables;
they were never implemented and have been removed from the docs.

---

## Troubleshooting

### Common Issues

1. **Node not joining cluster**
   - Check network connectivity between nodes
   - Verify gossip port is accessible
   - Check logs for memberlist errors

2. **Keys not found after node join**
   - Rebalancing may take time to complete
   - Check `/rebalance/status` endpoint
   - Verify ring topology with `/ring/info`

3. **High memory usage**
   - Enable LRU eviction with memory cap
   - Set appropriate TTLs for keys
   - Monitor with `/cluster/info` and metrics

### Debug Commands

```bash
# Check ring topology
curl http://localhost:8080/ring/info | jq

# Check cluster health
curl http://localhost:8080/cluster/info | jq

# Trigger rebalance
curl -X POST http://localhost:8080/rebalance

# Check rebalance status
curl http://localhost:8080/rebalance/status | jq
```
