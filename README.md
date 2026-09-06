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
4. If the primary's store no longer has the key (e.g. it restarted empty),
   consult replicas before answering 404 — and heal asynchronously by writing
   the replica's copy (with its exact expiry) back locally, restoring
   redundancy without slowing the read. Genuine misses cost replica probes
5. If another node is primary: forward request (transparent to client)
6. If primary is dead: route to replica using health-aware failover

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

1. If a replica is unreachable, the failure is tracked per replica
2. A background goroutine retries every 5 seconds until the replica
   acknowledges (entries clear automatically if the key expires or is deleted)
3. Entries failing longer than 24 hours are dropped with a warning, bounding
   tracking memory for permanently dead peers (the key stays
   under-replicated until its next write)
4. Successful retry removes the key from the pending queue

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
go run ./cmd/cache -addr :8080 -id 127.0.0.1:8080 -advertise-addr 127.0.0.1:8080 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8081,127.0.0.1:8082"

# Terminal 2: Join via node-a
go run ./cmd/cache -addr :8081 -id 127.0.0.1:8081 -advertise-addr 127.0.0.1:8081 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8080,127.0.0.1:8082"

# Terminal 3: Join via node-a
go run ./cmd/cache -addr :8082 -id 127.0.0.1:8082 -advertise-addr 127.0.0.1:8082 -gossip-advertise-addr 127.0.0.1 -peers "127.0.0.1:8080,127.0.0.1:8081"
```

Identity must use `host:port` everywhere (not short names like `node-a`):
every node builds its ring from the same ID set, and gossip liveness is
checked against those IDs — mismatched naming diverges the rings and breaks
routing. `-gossip-advertise-addr` is required whenever the gossip bind address
isn't directly dialable by peers (Docker, multi-host).

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
| `-advertise-addr` | `<addr>` | Address advertised in ring (for NAT/Docker) |
| `-peers` | `` | Comma-separated peer HTTP addresses |
| `-cluster-port` | `<port>+1000` | Gossip protocol port (0 = auto) |
| `-gossip-advertise-addr` | `` | Public IP for gossip (Docker/Lightsail) |
| `-mem-cap` | `0` | Memory cap in bytes for LRU eviction (0 = unlimited) |

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

All 11 test packages pass (verified with Go 1.25):

| Package | Description |
|---------|-------------|
| `audit/` | Integration tests, recovery scenarios, rebalance stress |
| `audit/rebalance/` | Rebalance boundary and stress tests |
| `audit/server/` | Server endpoint and quorum tests |
| `cmd/cache/` | CLI entrypoint and helper function tests |
| `internal/chaos/` | Chaos test harness and repeatability |
| `internal/cluster/` | SWIM failure detection tests |
| `internal/rebalance/` | Key migration logic tests |
| `internal/ring/` | Consistent hashing ring tests |
| `internal/server/` | HTTP server routing, replication, failover tests |
| `internal/store/` | KV store, TTL, LRU eviction tests |

**Test coverage includes:**
- Unit tests: store operations, ring hashing, consistent hashing distribution
- Integration tests: replication, failover, rebalance, TTL consistency
- Chaos tests: traffic generation, failure scenarios, repeatability
- Quorum tests: write/read with majority acknowledgment, version conflict resolution
- Load tests: throughput, concurrent clients, memory usage under load
- Recovery tests: node failure and rejoin scenarios

**Note:** Race detector (`-race`) requires a 64-bit GCC toolchain which may not be available on all Windows setups. Run `go test ./... -count=1` to verify.

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

## Field Console (shdc.noblechicken.me)

Every node serves a live operations dashboard at `/` — same-origin, so it
calls the cluster API directly with no proxy or keys:

- **Mesh**: live SVG topology (hash ring + SWIM probes) with per-node ledgers
- **Ledger**: write/read/delete console with latency + status inspector
- **Labs**: quorum round-trip and a 15-second synchronized-TTL drill
- **Telemetry**: entries, memory, pending replications, rebalance trigger
- **Wiring**: endpoint ledger and curl examples

```bash
# Local preview: the page at / IS the console
go run ./cmd/cache -addr :8080 -id 127.0.0.1:8080 -peers "127.0.0.1:8081"
# open http://localhost:8080/
```

Production URL (after adding the Namecheap `A` records below):

```text
http://shdc.noblechicken.me:8080/
```

DNS setup (Namecheap → Advanced DNS): three `A` records, host `shdc`,
values `13.126.24.246`, `13.127.78.189`, `15.252.208.189`. Round-robin
across all nodes is deliberate — each serves the console and the full API.
Port `:8080` is required (nodes don't listen on :80; a Caddy upgrade could
change that later). `website/` holds the dashboard source; `website.go`
embeds it into the binary.

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
├── audit/             # Integration and audit tests
├── deploy/            # Terraform, CI/CD, deployment scripts
├── docs/
│   ├── API.md         # Complete API documentation
│   ├── ARCHITECTURE.md # System architecture diagrams
│   ├── DECISIONS.md   # Tradeoff analysis
│   ├── DEPLOYMENT.md  # Deployment guide
│   ├── Deliverables.md # Project deliverables checklist
│   ├── Plan.md        # Implementation plan
│   ├── PRD.md         # Product requirements
│   └── Todos.md       # Development checklist
└── .github/workflows/ # CI/CD pipeline definitions
```

## Key Design Decisions

1. **Consistent Hashing**: Minimizes key movement when nodes change (~1/N keys)
2. **Async Replication**: Lower write latency, small window for data loss
3. **SWIM Gossip**: Decentralized failure detection without single point of failure
4. **Server-Side Routing**: Clients don't need to know cluster topology
5. **Pull-Before-Drop Rebalance**: Zero-downtime key migration
6. **Absolute TTL Replication**: All replicas expire simultaneously

## Limitations

- No persistence (in-memory only) — a restarted node serves its primaries'
  keys from replicas and heals asynchronously on read; genuinely absent keys
  cost replica probes before 404
- Ring membership is static (built from startup flags) — gossip tracks
  liveness for failover, but the ring itself is only changed by redeploying
  with new flags; `POST /rebalance` drives migration for operator-led moves
- Quorum reads require a real majority (503 otherwise) — stronger consistency,
  lower availability, exactly as documented for the opt-in mode
- No authentication or encryption
- Single data center (no multi-region)
- Best-effort replication with retry (small window for data loss on primary failure)

## CI/CD

This project uses a single GitHub Actions workflow (`.github/workflows/pipeline.yml`)
for continuous integration and delivery:

- **Linting**: `gofmt` and `go vet` checks
- **Testing**: Runs on Ubuntu, Windows, and macOS, plus integration/stress suites
- **Coverage**: Coverage reports generated on Ubuntu
- **Multi-platform Builds**: Linux (AMD64, ARM64), macOS (AMD64, ARM64), Windows (AMD64)
- **Docker Build & Push**: Builds and pushes to Amazon ECR via OIDC
  (`shdc-github-actions-ecr` role, no long-lived AWS keys)
- **Deploy**: Rolling deploy to 3 AWS Lightsail nodes with health checks,
  cluster-membership verification, and CRUD + performance smoke tests
- **Release**: On version tags (`v*`), binaries are archived into a GitHub Release

> Note: `go test -race` is not run in CI (it needs a 64-bit GCC toolchain unavailable
> on some runners). Run `go test ./... -count=1` locally to verify.

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
- **Cloud**: AWS (Lightsail via Terraform + GitHub Actions; see `deploy/`), with
  Kubernetes manifests documented in `docs/DEPLOYMENT.md` for EKS/GKE/AKS

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
