# API Documentation

This document describes the HTTP API endpoints for the Self-Healing Distributed Cache.

## Base URL

All endpoints are relative to the node's HTTP address (e.g., `http://localhost:8080`).

## Endpoints

### Data Operations

#### Set a key

```
POST /set
```

Set a key-value pair with optional TTL.

**Request body:**
```json
{
  "key": "mykey",
  "value": "myvalue",
  "ttl_ms": 60000
}
```

| Field   | Type   | Required | Description                          |
|---------|--------|----------|--------------------------------------|
| key     | string | Yes      | The key to set                       |
| value   | string | Yes      | The value to store                   |
| ttl_ms  | int    | No       | Time-to-live in milliseconds (0=none)|

**Response:**
```json
{"status": "ok"}
```

**Behavior:**
- If this node is the primary for the key, it writes locally and replicates asynchronously
- If another node is the primary, the request is forwarded transparently
- If the primary is dead, the request is routed to a replica

---

#### Get a key

```
GET /get?key=<key>
```

Retrieve a value by key.

**Query parameters:**
| Param | Type   | Required | Description       |
|-------|--------|----------|-------------------|
| key   | string | Yes      | The key to retrieve|

**Response (200):**
```json
{
  "key": "mykey",
  "value": "myvalue"
}
```

**Response (404):**
```json
{"error": "key not found"}
```

**Behavior:**
- Routes to the primary node for the key
- Falls back to replica if primary is dead
- If the primary is alive but no longer has the key (e.g. it restarted with
  an empty store), replicas are consulted before 404 — genuinely absent keys
  cost replica probes, which is the documented availability tradeoff
- Returns 404 if key doesn't exist or has expired

---

#### Delete a key

```
DELETE /delete?key=<key>
```

Delete a key-value pair.

**Query parameters:**
| Param | Type   | Required | Description       |
|-------|--------|----------|-------------------|
| key   | string | Yes      | The key to delete |

**Response:**
```json
{"status": "ok"}
```

**Behavior:**
- Deletes from primary and replicates delete to replicas
- Routes to correct owner if this node isn't the primary

---

### Quorum Consistency Operations

These endpoints provide stronger consistency guarantees at the cost of higher latency.

#### Quorum Set

```
POST /quorum/set
```

Set a key with quorum consistency. The write must be acknowledged by a majority of replicas.

**Request body:**
```json
{
  "key": "mykey",
  "value": "myvalue",
  "ttl_ms": 60000
}
```

**Response (200):**
```json
{
  "status": "ok",
  "acks": 2,
  "replica_acks": 1,
  "version": 1
}
```

**Response (503):**
```json
{
  "status": "quorum_failed",
  "acks": 1,
  "replica_acks": 0,
  "needed": 2,
  "error": "..."
}
```

**Tradeoff:** Higher consistency, lower availability (fails if quorum unreachable).

---

#### Quorum Get

```
GET /quorum/get?key=<key>
```

Get a key with quorum consistency. Queries a majority of replicas and returns the most recent value.

**Response (200):**
```json
{
  "key": "mykey",
  "value": "myvalue",
  "version": 1,
  "from": "node-b"
}
```

**Response (503):**
```json
{
  "status": "quorum_failed",
  "acks": 1,
  "needed": 2,
  "error": "majority of nodes unreachable"
}
```

**Tradeoff:** Higher consistency, higher read latency. Unlike the default
read path, quorum reads do NOT fall back to a single replica: fewer than a
majority of responses is a 503, not a best-effort answer.

---

### Cluster Information

#### Ring Info

```
GET /ring/info
```

Get the current hash ring topology.

**Response:**
```json
{
  "node_id": "node-a",
  "ring_nodes": [
    {"ID": "node-a", "Addr": ":8080"},
    {"ID": "node-b", "Addr": ":8081"},
    {"ID": "node-c", "Addr": ":8082"}
  ]
}
```

---

#### Cluster Health

```
GET /cluster/info
```

Get cluster membership and health status.

**Response:**
```json
{
  "cluster_enabled": true,
  "node_id": "node-a",
  "alive_count": 3,
  "members": ["node-a", "node-b", "node-c"]
}
```

---

#### Health Check

```
GET /health
```

Health check endpoint for load balancers and orchestrators.

**Response:**
```json
{
  "status": "healthy",
  "node_id": "node-a",
  "alive_nodes": 3
}
```

| Status     | Description                          |
|------------|--------------------------------------|
| healthy    | Node is operating normally           |
| degraded   | Node is running but cluster issues   |

---

#### Metrics

```
GET /metrics
```

Get operational metrics for monitoring.

**Response:**
```json
{
  "node_id": "node-a",
  "entry_count": 42,
  "memory_usage": 1024,
  "memory_cap": 0,
  "has_eviction": false,
  "pending_repls": 0,
  "alive_nodes": 3,
  "cluster_enabled": true
}
```

| Field           | Type    | Description                          |
|-----------------|---------|--------------------------------------|
| entry_count     | int     | Number of keys stored on this node   |
| memory_usage    | int     | Estimated memory usage in bytes      |
| memory_cap      | int     | Memory cap (0 = unlimited)           |
| has_eviction    | bool    | Whether LRU eviction is enabled      |
| pending_repls   | int     | Number of replications awaiting retry|
| alive_nodes     | int     | Number of alive cluster members      |
| cluster_enabled | bool    | Whether cluster mode is enabled      |

---

### Rebalance Operations

#### Trigger Rebalance

```
POST /rebalance
```

Manually trigger a rebalance operation.

**Response:**
```json
{"status": "rebalance started"}
```

**Note:** Rebalancing also happens automatically when node failures are detected.

---

#### Rebalance Status

```
GET /rebalance/status
```

Get the status of the last rebalance operation.

**Response:**
```json
{
  "in_progress": false,
  "last_result": {
    "TotalKeys": 42,
    "MovedKeys": 10,
    "FailedKeys": 0,
    "Duration": "3.5ms",
    "StartedAt": "2024-01-01T00:00:00Z",
    "CompletedAt": "2024-01-01T00:00:00Z"
  },
  "migrations": [...]
}
```

---

## Internal Endpoints

These endpoints are used internally by the cluster and should not be called directly.

| Endpoint | Description |
|----------|-------------|
| `/replica/set` | Receive replicated write from primary |
| `/replica/delete` | Receive replicated delete from primary |
| `/replica/get` | Serve local copy (quorum reads + primary-miss fallback) |
| `/replica/quorum/set` | Receive quorum write from primary |
| `/rebalance/accept` | Accept key during rebalance migration |
| `/rebalance/pull` | Pull key from old owner during rebalance |
| `/rebalance/complete` | Signal migration complete, safe to delete |

---

## Error Responses

All endpoints return JSON error responses:

```json
{"error": "error message"}
```

| Status Code | Meaning                                      |
|-------------|----------------------------------------------|
| 400         | Bad request (missing/invalid parameters)     |
| 404         | Key not found                                |
| 405         | Method not allowed                           |
| 500         | Internal server error                        |
| 502         | Bad gateway (node unavailable)               |
| 503         | Service unavailable (no nodes/quorum)        |
