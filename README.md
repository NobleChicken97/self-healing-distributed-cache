# Self-Healing Distributed Cache

A distributed, in-memory key-value cache built from scratch in Go that shards data
across multiple independent nodes, replicates keys for fault tolerance, detects node
failures automatically via gossip, and rebalances data with zero downtime.

## Features

- **Consistent Hashing**: Keys are distributed across nodes using a hash ring with
  virtual nodes for even load distribution. Adding or removing a node moves only ~1/N keys.
- **Replication**: Each key is replicated to one additional node (replication factor 2).
  Writes are asynchronous for low latency.
- **Failure Detection**: Nodes detect peer failures automatically using the SWIM
  gossip protocol (via hashicorp/memberlist). Failure detection within ~2 seconds.
- **Automatic Failover**: Requests for a dead node's keys are automatically redirected
  to replicas with no client-side awareness. Health-aware routing skips known-dead nodes
  immediately (~2.5ms failover).
- **Rebalancing**: When a node joins or leaves, keys are migrated safely using a
  pull-before-drop protocol that ensures no window where a key is absent from all nodes.
- **TTL Consistency**: Absolute expiry timestamps are replicated, ensuring all replicas
  expire keys simultaneously (<1ms drift).

## Architecture

```
                    ┌─────────────────────────────────────────────────┐
                    │                 Client Request                 │
                    └─────────────────┬───────────────────────────────┘
                                      │
                                      ▼
                    ┌─────────────────────────────────────────────────┐
                    │           Any Node (Server-Side Routing)       │
                    │  ┌─────────────┐  ┌──────────────────────────┐ │
                    │  │  Hash Ring   │  │  Cluster Health (SWIM)   │ │
                    │  │  (sharding)  │  │  (failure detection)     │ │
                    │  └──────┬──────┘  └──────────┬───────────────┘ │
                    └─────────┼────────────────────┼─────────────────┘
                              │                    │
            ┌─────────────────┼────────────────────┼─────────────────┐
            │                 │                    │                 │
            ▼                 ▼                    ▼                 ▼
    ┌───────────┐     ┌───────────┐        ┌───────────┐     ┌───────────┐
    │  Node A   │◄───►│  Node B   │◄──────►│  Node C   │◄───►│  Node N   │
    │ (Primary) │     │ (Replica) │        │ (Replica) │     │           │
    └───────────┘     └───────────┘        └───────────┘     └───────────┘
          │                 │                    │                 │
          └─────────────────┴────────────────────┴─────────────────┘
                              Gossip Mesh (SWIM)
```

### Request Flow

1. Client sends request to any node
2. Node checks hash ring to find the key's primary owner
3. If this node is the primary: serve locally, replicate asynchronously
4. If another node is primary: forward request (transparent to client)
5. If primary is dead: route to replica using health-aware failover

### Replication Path

1. Client SET → Primary node
2. Primary writes locally, computes absolute expiry
3. Primary asynchronously pushes to replica(s) via `/replica/set`
4. Replica stores with same absolute expiry timestamp

### Rebalance Protocol (Pull-Before-Drop)

1. Ring changes (node join/leave)
2. Each node computes which keys moved to new owners
3. For each moved key:
   - New owner pulls value from old owner via `/rebalance/pull`
   - New owner stores locally via `/rebalance/accept`
   - Old owner retains key until new owner confirms

### Automatic Rebalance Trigger

When a node failure is detected via SWIM gossip, the cluster automatically triggers rebalancing:

1. SWIM detects node failure (~2 seconds)
2. Cluster health updates mark node as dead
3. `OnTopologyChange` callback fires
4. All surviving nodes trigger rebalance
5. Keys from failed node are redistributed

### Replication Retry

Failed replications are automatically retried:

1. If replica is unreachable, the failure is tracked
2. A background goroutine retries every 5 seconds
3. Up to 3 retry attempts per failed key
4. Successful retry removes key from pending queue

## Quick Start

### Prerequisites

- Go 1.22 or later
- hashicorp/memberlist dependency (fetched automatically)

### Build

```bash
go build ./...
```

### Run a 3-Node Cluster

Open three terminals and run:

```bash
# Terminal 1: Seed node
go run ./cmd/cache -addr :8080 -id node-a

# Terminal 2: Join via node-a
go run ./cmd/cache -addr :8081 -id node-b -peers ":8080"

# Terminal 3: Join via node-a
go run ./cmd/cache -addr :8082 -id node-c -peers ":8080"
```

### CLI Client Usage

```bash
# Set a key
go run ./cmd/cache-client -url http://localhost:8080 -command set -key mykey -value myvalue -ttl-ms 10000

# Get a key (will route to correct node automatically)
go run ./cmd/cache-client -url http://localhost:8080 -command get -key mykey

# Delete a key
go run ./cmd/cache-client -url http://localhost:8080 -command delete -key mykey
```

### API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/set` | Set a key with optional TTL |
| GET | `/get?key=<key>` | Get a key's value |
| DELETE | `/delete?key=<key>` | Delete a key |
| GET | `/ring/info` | View ring membership |
| GET | `/cluster/info` | View cluster health |
| GET | `/health` | Health check for load balancers |
| GET | `/metrics` | Operational metrics |
| POST | `/rebalance` | Trigger rebalance |
| GET | `/rebalance/status` | View rebalance status |
| POST | `/quorum/set` | Quorum write (stronger consistency) |
| GET | `/quorum/get?key=<key>` | Quorum read (stronger consistency) |

See [docs/API.md](./docs/API.md) for complete API documentation.

## Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `:8080` | HTTP listen address |
| `-id` | `<addr>` | Unique node ID |
| `-peers` | `` | Comma-separated peer addresses |
| `-cluster-port` | `<port>+1000` | Gossip protocol port |

## Running Tests

```bash
# Run all tests
go test ./...

# Run with verbose output
go test ./... -v

# Run specific package tests
go test ./internal/... -v

# Run with race detector (requires GCC toolchain)
go test -race ./...
```

## Test Results

All tests pass (verified on Windows with Go 1.25):

```
ok  selfhealingcache/audit           6.171s
ok  selfhealingcache/audit/rebalance 2.155s
ok  selfhealingcache/audit/server    1.949s
ok  selfhealingcache/internal/chaos  2.057s
ok  selfhealingcache/internal/cluster 5.267s
ok  selfhealingcache/internal/rebalance 1.862s
ok  selfhealingcache/internal/ring   1.203s
ok  selfhealingcache/internal/server 3.790s
ok  selfhealingcache/internal/store  0.787s
```

**Test coverage includes:**
- Unit tests: store operations, ring hashing, consistent hashing distribution
- Integration tests: replication, failover, rebalance, TTL consistency
- Chaos tests: traffic generation, failure scenarios
- Quorum tests: write/read with majority acknowledgment

**Note:** Race detector (`-race`) requires a 64-bit GCC toolchain which may not be available on all Windows setups.

## Demo

Run the automated demo to see self-healing in action:

```bash
# Linux/macOS
./demo.sh

# Windows
demo.bat
```

The demo will:
1. Start a 3-node cluster
2. Populate with test data
3. Simulate a node failure
4. Verify data remains accessible
5. Recover the failed node

## Decision Log

See [DECISIONS.md](./docs/DECISIONS.md) for detailed tradeoff analysis of every major
design decision including:
- HTTP vs raw TCP protocol
- Lazy vs active TTL expiry
- Consistent hashing vs modulo hashing
- Virtual nodes for load balancing
- Synchronous vs asynchronous replication
- Gossip/SWIM vs centralized heartbeat
- Client-side vs server-side routing
- Absolute vs relative TTL replication

## Project Structure

```
├── cmd/
│   ├── cache/         # Server binary
│   └── cache-client/  # CLI client binary
├── internal/
│   ├── chaos/         # Chaos test harness
│   ├── cluster/       # SWIM failure detection
│   ├── rebalance/     # Key migration
│   ├── ring/          # Consistent hashing ring
│   ├── server/        # HTTP server with routing
│   └── store/         # In-memory KV store with TTL
└── docs/
    ├── DECISIONS.md   # Tradeoff analysis
    ├── Plan.md        # Implementation plan
    ├── PRD.md         # Product requirements
    └── Todos.md       # Development checklist
```

## Key Design Decisions

1. **Consistent Hashing**: Minimizes key movement when nodes change (~1/N keys)
2. **Async Replication**: Lower write latency, small window for data loss
3. **SWIM Gossip**: Decentralized failure detection without single point of failure
4. **Server-Side Routing**: Clients don't need to know cluster topology
5. **Pull-Before-Drop Rebalance**: Zero-downtime key migration
6. **Absolute TTL Replication**: All replicas expire simultaneously

## Limitations

- No persistence (in-memory only)
- No authentication or encryption
- Single data center (no multi-region)
- Best-effort replication with retry (small window for data loss on primary failure)

## CI/CD

This project uses GitHub Actions for continuous integration and delivery:

### Continuous Integration (`.github/workflows/ci.yml`)

- **Linting**: `gofmt` and `go vet` checks
- **Testing**: Runs on Ubuntu, Windows, and macOS
- **Race Detection**: Tests run with `-race` flag
- **Coverage**: Coverage reports generated on Ubuntu
- **Multi-platform Builds**: Linux (AMD64, ARM64), macOS (AMD64, ARM64), Windows (AMD64)
- **Docker Build**: Builds and tests Docker image

### Release Automation (`.github/workflows/release.yml`)

Triggered on version tags (`v*`):

- Builds release binaries for all platforms
- Creates GitHub Release with artifacts
- Builds and pushes Docker image to GitHub Container Registry

### Create a Release

```bash
# Tag a version
git tag -a v1.0.0 -m "Release v1.0.0"

# Push to trigger release workflow
git push origin v1.0.0
```

## Deployment

See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) for comprehensive deployment options:

- **Docker Compose**: Quick local cluster setup
- **Single Binary**: Simple deployment for edge computing
- **Docker**: Containerized deployment
- **Kubernetes**: Production orchestration with StatefulSets
- **Cloud**: AWS (ECS/EKS/Fargana), GCP (GKE/Cloud Run), Azure (AKS/ACI)

### Quick Docker Deploy

```bash
# Build and run with Docker Compose
docker-compose up -d

# Or build and run manually
docker build -t cache:latest .
docker run -d -p 8080:8080 cache:latest -addr :8080 -id node-1
```

## License

Learning project for distributed systems fundamentals.
