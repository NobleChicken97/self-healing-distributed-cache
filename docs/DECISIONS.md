# Decision Log: SHDC

## Phase 0: HTTP vs raw TCP

- **Context:** The first node needs a small network API that is easy to exercise from a separate process and inspect during the later chaos demo.
- **Options considered:** Raw TCP gives a Redis-like protocol and lower framing overhead; HTTP/JSON is simpler to debug and script.
- **Choice made:** Use HTTP/JSON with `POST /set`, `GET /get`, and `DELETE /delete` for v1.
- **What we gave up and what we gained:** We give up protocol compactness and some throughput in exchange for a standard, observable interface that can be tested with common tools and extended without designing a custom wire protocol.

## Phase 0: Lazy vs active TTL expiry

- **Context:** Expired values must not be returned, and later phases need replicas to honor one authoritative expiry time.
- **Options considered:** Lazy expiry removes a key only when it is read; active expiry periodically sweeps expired entries in a background goroutine.
- **Choice made:** Store an absolute expiry timestamp and run an active sweep with a one-second default interval. Reads also check the timestamp, so expiry does not depend on the next sweep tick.
- **What we gave up and what we gained:** We give up a small amount of background CPU and accept up to one sweep interval of memory retention in exchange for predictable cleanup and a design that can replicate the same expiry timestamp later.

## Phase 1: Consistent hashing vs modulo hashing

- **Context:** Keys must be distributed across N nodes, and the system must survive node joins/leaves without reshuffling most keys.
- **Options considered:** Modulo hashing (`key_hash % N`) is simple but reshuffles ~75% of keys when N changes by 1. Consistent hashing (a ring of hash values, each node owning an arc) reshuffles only ~1/N of keys.
- **Choice made:** Consistent hashing with SHA-256 as the hash function and sorted slice + binary search for O(log V) lookups (V = total virtual points).
- **Measured results:** Adding a 3rd node moved 33.37% of keys (expected ~33%); removing a node moved 33.21%. Modulo hashing under the same conditions moved 75.53% of keys — confirming the failure mode.
- **What we gave up and what we gained:** We give up O(1) lookup complexity (modulo) for O(log V) in exchange for minimal key movement on topology change, which is essential for replication and rebalancing later.

## Phase 1: Virtual nodes

- **Context:** Without virtual nodes, a physical node can own a disproportionately large arc of the ring, causing uneven load.
- **Options considered:** One point per physical node (simpler, but uneven distribution) vs. 100-200 virtual nodes per physical node (smoother distribution, slightly more memory).
- **Choice made:** 150 virtual nodes per physical node, each hashed as `nodeID#replicaIndex`.
- **Measured results:** Without virtual nodes, 10,000 keys across 3 nodes gave a distribution of {node-a: 5872, node-b: 1389, node-c: 2739} (stddev 1877.8). With 150 virtual nodes: {node-a: 3342, node-b: 3321, node-c: 3337} (stddev 9.0).
- **What we gave up and what we gained:** We give up ~150x more ring entries per node in exchange for near-perfect load balance across physical nodes.

## Phase 2: Replication factor and placement

- **Context:** Each key must exist on more than one node to survive a single node failure.
- **Options considered:** Replication factor 1 (no replication, simplest but no fault tolerance), 2 (1 primary + 1 replica, survives 1 failure), or 3 (survives 2 failures but 3x storage).
- **Choice made:** Replication factor 2 for v1. Replicas are placed on the next distinct physical node clockwise on the ring from the primary (the Cassandra/DynamoDB approach).
- **What we gave up and what we gained:** We give up 50% of usable memory (each key stored twice) in exchange for the ability to survive any single node failure without data loss.

## Phase 2: Sync vs async replication (CAP tradeoff)

- **Context:** After writing to the primary, when do we confirm the write to the client — after replicating, or before?
- **Options considered:** Synchronous (wait for replica ack before confirming) vs asynchronous (confirm immediately, replicate in background).
- **Choice made:** Asynchronous replication. The primary writes locally, confirms to the client immediately, then propagates to the replica in a background goroutine.
- **CAP framing:** This is an explicit AP (availability + partition tolerance) choice over CP (consistency + partition tolerance). Under async replication:
  - **What we gain:** Writes succeed even if the replica is temporarily slow or unreachable. Lower write latency.
  - **What we lose:** A small window where a write confirmed to the client could be lost if the primary dies before replicating. A read from a replica may return stale data if replication hasn't completed yet.
- **Verified by test:** `TestFailoverRead` confirms that killing the primary node for a key still allows reads to succeed via the replica fallback path.

## Phase 3: Failure detection — gossip/SWIM vs centralized heartbeat

- **Context:** Nodes must detect peer failures automatically without manual intervention.
- **Options considered:** Centralized heartbeat (all nodes report to one coordinator) vs. decentralized gossip/SWIM.
- **Choice made:** Use hashicorp/memberlist, which implements the SWIM protocol. This is a mature, well-tested library that handles the gossip protocol details.
- **Why not from scratch:** Per Plan.md fallback — the novel, interview-relevant work is in the cache logic, hashing, replication, and rebalancing built on top of failure detection, not in re-deriving SWIM byte-for-byte. The library handles: suspicion mechanism, gossip dissemination, and timeouts.
- **What was built from scratch vs. adopted:** The cluster event handling, alive-node tracking, and integration with our ring routing are original. The SWIM protocol internals (ping/ack, gossip rounds, suspicion) are from hashicorp/memberlist.
- **Measured results:** Killing a node is detected by surviving nodes within ~2 seconds via gossip protocol.
- **What we gave up and what we gained:** We give up some "from scratch" credibility in exchange for a battle-tested implementation. We gain: faster development, proven correctness under network partitions, and the ability to focus on the cache-specific logic that differentiates this project.
- **Verified by test:** `TestClusterJoinLeave` confirms that shutting down node-3 is detected as FAILED by node-1 within the bounded time window.

## Phase 4: Client-side vs server-side routing

- **Context:** When a client requests a key that this node doesn't own, where should the routing logic live?
- **Options considered:** Client-side (client tracks the ring and picks a node) vs. server-side (any node can proxy a request to the correct owner).
- **Choice made:** Server-side routing. Any node receiving a request it doesn't own forwards it to the correct owner via the ring, transparent to the client.
- **What we gave up and what we gained:** We give up some latency (extra network hop for proxied requests) in exchange for simpler clients that don't need to know the ring topology. Clients can connect to any node and get correct results.
- **Integration with failure detection:** Server-side routing integrates naturally with the cluster health info from Phase 3:
  - If the primary for a key is known dead (per cluster health), skip it immediately and go to replicas
  - If the primary is unreachable (HTTP failure), fall back to replicas
  - Replicas are tried in order of health preference (alive replicas first)
- **Measured results:** Health-aware failover completes in ~2.5ms (no timeout waiting for dead primary).
- **Verified by test:** `TestHealthAwareRouting` confirms that when a primary is marked dead in cluster health, reads are immediately served from replicas. `TestFailoverRead` confirms that when a primary is actually unreachable, reads fall back to replicas.

## Phase 5: Rebalancing on join/leave (zero downtime)

- **Context:** When a node joins or leaves, keys must be redistributed according to the new ring topology without dropping or duplicating data, and without stopping client traffic.
- **Migration protocol (pull-before-drop):**
  1. Compute which keys changed ownership (old_owner -> new_owner)
  2. For each moved key:
     a. Old owner pulls the key's value from its local store
     b. Old owner pushes the value to the new owner via `/rebalance/accept`
     c. New owner stores the key locally
     d. Old owner deletes the key only after confirmation
  3. During step 2, both nodes have the key, so reads always succeed.
- **Key invariant:** No window exists where a key is absent from all reachable nodes during migration. The old owner retains the key until the new owner confirms receipt.
- **Measured results:** Rebalancing 3 keys to a new node completes in ~3ms.
- **Edge cases observed:**
  - If the new owner is unreachable, the migration fails and the key remains on the old owner (safe failure mode).
  - If a key doesn't exist on the old owner (already expired or deleted), the migration is skipped.
- **Verified by test:** `TestRebalanceOnNodeJoin` confirms that when a third node joins a 2-node cluster, keys are migrated correctly and all keys remain readable throughout the rebalance.

## Phase 8: Eviction policy (LRU)

- **Context:** Nodes need a memory cap to prevent unbounded growth. When the cap is exceeded, keys must be evicted.
- **Options considered:**
  - LRU (Least Recently Used): simpler to implement, good for temporal locality
  - LFU (Least Frequently Used): better for frequency-based access patterns, more complex
- **Choice made:** LRU with a doubly-linked list for O(1) access tracking and eviction.
- **Implementation:**
  - Added `NewWithEviction(sweepInterval, memCap)` constructor
  - Added `SetWithExpiry()`, `GetWithVersion()`, `SetVersion()` methods for version tracking
  - Memory tracking: each entry tracks its size (key + value bytes)
  - Eviction triggered on every write when over cap
- **Verified by test:**
  - `TestLRUEviction`: confirms least-recently-used keys are evicted
  - `TestLRUAccessOrder`: confirms access order affects eviction
  - `TestMemoryTracking`: confirms memory accounting is accurate

## Phase 9: Quorum consistency mode

- **Context:** The default eventual consistency mode has a small window where reads may return stale data. For use cases requiring stronger consistency, quorum mode ensures read-after-write consistency.
- **CAP tradeoff:** Quorum mode sacrifices some availability (a write can fail if quorum isn't reachable) to gain stronger consistency guarantees.
- **Implementation:**
  - **Quorum Write (`/quorum/set`)**: Writes to primary, then waits for acknowledgment from a majority of replicas before returning success.
  - **Quorum Read (`/quorum/get`)**: Queries a majority of replicas and returns the value with the highest version number.
  - **Version tracking**: Each key has a monotonically increasing version number. Writes increment the version; reads resolve conflicts by picking the highest version.
  - **Opt-in**: Quorum endpoints are separate from default endpoints, so eventual consistency remains the default.
- **Verified by test:**
  - `TestQuorumWriteAndRead`: confirms quorum write replicates to all replicas and quorum read returns the value
  - `TestVersionIncrement`: confirms version numbers increment correctly
  - `TestSetVersionConflictResolution`: confirms older versions don't overwrite newer ones

## Phase 7: Chaos test harness and live-kill demo

- **Context:** The chaos test is the ultimate proof that all previous phases work together to provide self-healing behavior.
- **Failure criteria defined:**
  - Timeout: >500ms for a request
  - Server error: 5xx response
  - Data integrity: GET returns 404 for a key that was previously SET successfully
- **Traffic generator:**
  - Continuously issues Set/Get requests at configurable rate (default 100 req/sec)
  - 70% reads / 30% writes (realistic read-heavy workload)
  - Configurable retry count for transient failures
- **Metrics tracked:**
  - Total requests, succeeded, failed, retried
  - Data loss incidents (keys that disappeared)
  - Throughput (requests/second)
  - Failed request details for debugging
- **Test scenarios verified:**
  - Node join: new node joins, keys migrate, zero data loss
  - Node failure: killed node's traffic routes to replicas via health-aware failover
  - Recovery: restarted node rejoins, cluster rebalances
- **Verified by test:** `TestTrafficGeneratorBasics` confirms the harness works. Integration tests (`TestFailoverRead`, `TestHealthAwareRouting`, `TestRebalanceOnNodeJoin`) verify the underlying mechanisms.

## Phase 6: TTL consistency across replicas

- **Context:** TTL-based expiry must be honored the same way on every replica of a key, not just the primary. A key should not "come back" after being read as expired on one replica.
- **Options considered:**
  - Replicate relative TTL duration: simpler but causes drift (each replica calculates its own expiry from `time.Now()` at write time)
  - Replicate absolute expiry timestamp: slightly more complex but ensures all replicas expire simultaneously
- **Choice made:** Replicate the absolute expiry timestamp (milliseconds since epoch) alongside the value. The primary computes `expiresAt = time.Now().Add(ttl)` once and sends it to all replicas.
- **Implementation:**
  - Added `SetWithExpiry()` and `GetExpiry()` methods to the store
  - Modified `replicateSet()` to send `expires_at_ms` instead of `ttl_ms`
  - Modified `handleReplicaSet()` to use the absolute expiry timestamp
- **Measured results:** Expiry drift between primary and replica is ~154µs (well under 1ms). This is purely processing-time drift, not clock drift.
- **Verified by test:**
  - `TestTTLConsistencyAcrossReplicas`: confirms primary and replica have identical expiry timestamps
  - `TestTTLExpirySynchronized`: confirms both replicas expire the key at the same wall-clock time

## Post-Deploy Hardening (2026-09-05, production evidence from Lightsail + local repro)

- **Context:** The CI/CD pipeline unmasked a chain of latent defects that unit tests never caught: each fix exposed the next stage (OIDC → Docker build → ECR auth → gossip → write path). All findings below were proven with CloudTrail logs, container logs, or live black-box probes — not assumed.
- **Ring/gossip identity namespaces must agree:**
  - Deploy used `-id node-N` while ring peers were `IP:port` strings, so every node built a divergent ring AND gossip liveness (`node-N`) never matched ring IDs (`IP:port`): all peers looked dead, rings disagreed on ownership, and failover paths received empty bodies (400s on ~50% of writes).
  - Choice: nodes use `-id IP:8080` + `-advertise-addr IP:8080` everywhere (README, compose, k8s example, all deploy scripts). Identical ring sets on all nodes; liveness matches.
  - Tradeoff accepted: node IDs are IPs, less pretty in logs than `node-N`.
- **Gossip reachability (three compounding defects):**
  - Seeds dialed the HTTP port while memberlist bound HTTP+1000 → seeds derived as `host(peer):gossip-port`, convention-aware (explicit uniform port vs default HTTP+1000).
  - Gossip bound 127.0.0.1 when `-addr` host was empty → `connection refused` via Docker mapping; now binds 0.0.0.0 for empty hosts.
  - Docker bridge NAT rewrote UDP probe sources so memberlist classified every probe "unexpected node" → containers run `--network host` with explicit `-gossip-advertise-addr` (public IP / service name).
- **Restarted-empty primary shadowed replicas (proven live: 3 keys 404 cluster-wide after one restart):**
  - `handleGet` on a primary miss now consults replicas via `/replica/get` before 404. Cost: extra probes on genuine misses (documented in README/API.md). No write-back healing yet — redundancy restores on next write.
  - Verified by test: `TestPrimaryMissFallsBackToReplica`; verified live: 20/20 readable from all nodes after empty restart (was 17/20).
- **Failover write body:** `forwardWithBody` handed the replica path a consumed body → 400. Body restored first. Verified by test: `TestDeadPrimarySetAcceptedByReplica` (fails without the fix).
- **Quorum reads now enforce majority:** previously any single response (even a 404 error body decoded as zero-value) could answer; now fewer-than-majority is 503, non-200s are skipped, bodies always closed. Matches the documented contract; availability cost is the documented tradeoff.
- **Robustness:** all node-to-node HTTP uses transports with dial + response-header timeouts (previously unbounded `DefaultTransport`); `/metrics` `pending_repls` read now holds the mutex (was a map race); `-mem-cap` flag wires LRU eviction at runtime (previously test-only); dead `updatedAt` field removed; unimplemented `CACHE_*` env vars removed from docs (flags are the only config).

## Follow-up Hardening (2026-09-06, same evidence-led procedure)

- **Read healing on primary miss:** serving from replicas left the restarted primary permanently empty (redundancy never restored). Reads now write the replica's copy back asynchronously with its exact absolute expiry — store-direct, so no re-replication fan-out. Proven live (restarted node `entry_count` 0 → 3 while serving 20/20) and by `TestPrimaryMissFallsBackToReplica`. Accepted wrinkle: a delete racing the heal can resurrect a zombie copy for one TTL window (standard eventual-consistency tradeoff, documented).
- **Bounded retry tracking:** `pendingRepls` grew forever for permanently dead peers (one entry per failed key). Entries failing longer than `maxPendingAge` (24h default, field-overridable for tests) are now dropped with a warning; the key stays under-replicated until its next write. The dead `maxRetries`/`failCount`/`pendingReplication` leftovers were removed. Verified by test: `TestPendingReplicationAgesOut`.

## Functional Dashboard + Domain Redo (2026-09-06)

- **Context:** the portfolio site was static (zero cluster calls) and published under the apex domain path (`noblechicken.me/self-healing-distributed-cache/`). Requirement: a genuinely functional console on `shdc.noblechicken.me` with a bespoke editorial identity — no AI-slop templating.
- **Same-origin serving (the load-bearing decision):** an HTTPS-hosted page cannot call the HTTP-only cluster API (browsers block mixed content), and GitHub Pages offers no proxy. So the console is embedded in the binary (`website/website.go`, go:embed) and served by the nodes at `/` behind the API routes — every call is plain same-origin `fetch`. Retired the GitHub Pages workflow; the project leaves the apex path.
- **CORS:** permissive (`*`, no credentials) so the node switcher can query peers directly. Grants nothing new: the API is no-auth by design and curl-equivalent from anywhere.
- **DNS (user action at Namecheap):** three `A` records, host `shdc` → 13.126.24.246, 13.127.78.189, 15.252.208.189 (round-robin; every node serves site + API via server-side routing). URL carries `:8080` (no :80 listener; Caddy upgrade path noted, not built).
- **Identity:** committed ink-moss + bone paper, terracotta/brass rationed; Fraunces (display) / Archivo (UI) / IBM Plex Mono (data); left-anchored asymmetric bands, hairline rules, SVG grain + arch art, anime.js guarded with reduced-motion + no-JS fallbacks. Verified by screenshot review in a real browser, not by vibes.
