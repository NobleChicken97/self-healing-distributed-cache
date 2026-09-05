# Plan: Self-Healing Distributed Cache

## Current status
- Phase 0 complete: concurrent store, active TTL expiry, HTTP API, CLI client, unit tests, and decision-log entries are implemented.
- Phase 1 complete: consistent hashing ring with virtual nodes, ring-based request routing, key-movement tests, and decision-log entries are implemented.
- Phase 2 complete: async replication (factor 2), replica placement on ring, read fallback to replicas, and decision-log entries are implemented.
- Phase 3 complete: SWIM-based failure detection via hashicorp/memberlist, alive-node tracking, failure event logging, and decision-log entries are implemented.
- Phase 4 complete: server-side routing with health-aware failover, cluster health integration, and decision-log entries are implemented.
- Phase 5 complete: rebalancing on node join/leave with pull-before-drop migration protocol, and decision-log entries are implemented.
- Phase 6 complete: TTL consistency across replicas using absolute expiry timestamp replication, and decision-log entries are implemented.
- Phase 7 complete: Chaos test harness with traffic generator, failure criteria, and metrics tracking implemented.
- Phase 8 complete: LRU eviction policy with memory cap and O(1) access tracking.
- Phase 9 complete: Quorum consistency mode with version tracking and majority acknowledgment.
- All decision-log entries complete.
- Validation: `go test ./...` passes (all 7 packages: store, ring, server, cluster, rebalance, chaos).
- `go test -race ./...` is blocked by the local Windows GCC toolchain (`cc1.exe` reports 64-bit mode is not compiled in).
- Status: All phases complete. Ready for portfolio demonstration.
- CI/CD (2026-09-05): fixed `sts:AssumeRoleWithWebIdentity` AccessDenied — repo uses GitHub immutable OIDC `sub` (created after 2026-07-15 cutoff); pipeline now assumes dedicated least-privilege role `shdc-github-actions-ecr` (see `deploy/github_oidc_shdc.tf`). Shared roles for other projects untouched.

## How to use this document
Each phase has a goal, the key tradeoff decisions to actually understand (not just
implement) before moving on, a success criterion that must be met before advancing,
and a fallback if you get stuck. Do not move to the next phase until the current
phase's success criterion is met — this project fails as a portfolio piece if it's
half-working across many phases rather than fully working across a few.

Estimated total time: 3-6 weeks part-time, alongside GATE prep and placements. If
you're short on time, cut from the stretch phases (8-9) first, never from phases 0-7.

---

## Language and tooling decision (made upfront, not per-phase)
- Language: Go. Reason: goroutines and channels make concurrent node logic (gossip,
  replication, TTL sweeps) far less painful than in Python; strong existing
  libraries exist for gossip (hashicorp/memberlist implements SWIM).
- Tradeoff to understand: Python would be faster to prototype in but concurrency
  correctness (race conditions across replicas) is much easier to get subtly wrong.
  Rust would be even safer at compile-time but the learning curve cost isn't worth
  it for a portfolio timeline. Go is the middle ground — write this reasoning into
  DECISIONS.md verbatim once you've actually felt the tradeoff, not before.
- Gossip library decision point: you can either implement SWIM (heartbeat + gossip
  dissemination) from scratch, or use hashicorp/memberlist and focus your original
  work on the cache logic, hashing, and rebalancing instead. See Phase 3 fallback.

---

## Phase 0: Foundations — single-node cache
Goal: a working in-memory KV store with TTL, on one node, reachable over the network.

Key decisions to understand:
- TCP vs HTTP for the client protocol. HTTP is simpler to demo and debug; a raw TCP
  protocol is closer to how Redis actually works. Either is fine for v1 — pick one
  and record why.
- Lazy TTL expiry (check on read) vs active sweep (background goroutine). Lazy is
  simpler; active sweep matters more once you have replicas that must expire
  in sync. Understand this tradeoff now, it resurfaces in Phase 6.

Success criteria:
- Can Set/Get/Delete a key over the network from a separate client process.
- TTL expiry works and is covered by a test (key exists, then doesn't, after TTL).

Fallback if blocked: if TTL is being fussy, ship Set/Get/Delete first without TTL,
get Phase 1 and 2 working, then return to TTL in Phase 6 as originally scoped.

---

## Phase 1: Sharding via consistent hashing
Goal: multiple nodes, each owning a slice of the keyspace, routed correctly.

Key decisions to understand:
- Consistent hashing vs modulo hashing. Modulo hashing (key_hash % N) reshuffles
  nearly all keys when N changes. Consistent hashing (a ring of hash values, each
  node owning an arc) reshuffles only the keys that were between the changed node
  and its neighbor. Prove this to yourself with a small script comparing key
  movement under both schemes when a node is added.
- Virtual nodes. Without virtual nodes, one physical node can end up owning a much
  larger arc of the ring than others (uneven load). Virtual nodes (each physical
  node mapped to many points on the ring, e.g. 100-200) smooth this out. Understand
  why before adding them, not just that "more points = more even."

Success criteria:
- Ring correctly maps a key to a node.
- Adding or removing a node moves roughly 1/N of keys, not most of them — verified
  by an actual test, not assumed.

Fallback if blocked: build the ring without virtual nodes first, get routing
working end to end, then add virtual nodes as a follow-up improvement rather than
blocking on getting them right the first time.

---

## Phase 2: Replication
Goal: every key exists on its primary node plus at least one replica.

Key decisions to understand:
- Replication placement strategy: typically "the next N-1 nodes clockwise on the
  ring from the primary." Understand why this ties naturally into consistent
  hashing (it's how Cassandra/DynamoDB-style systems do it).
- Synchronous vs asynchronous replication on write. Synchronous (wait for replica
  ack before confirming write) is safer but slower and reduces availability if the
  replica is slow/down. Asynchronous is faster but risks losing the most recent
  write if the primary dies before replicating. Pick one for v1, document why, and
  note that this is a direct instance of the CAP tradeoff.

Success criteria:
- Killing the primary node for a key still allows a successful read of that key
  from a replica.

Fallback if blocked: get single-replica (replication factor 2) fully correct before
even considering higher replication factors — don't generalize to N replicas until
2 works and is well understood.

---

## Phase 3: Failure detection (gossip)
Goal: nodes detect a peer's death automatically, without a central coordinator.

Key decisions to understand:
- Gossip/SWIM vs centralized heartbeat (all nodes report to one coordinator).
  Centralized is simpler but reintroduces a single point of failure — defeats the
  purpose of a self-healing, decentralized cluster. Gossip avoids this but adds
  complexity and eventual (not instant) failure detection.
- False positive risk: a slow-but-alive node can get mistakenly marked dead under
  gossip if timeouts are too aggressive. Understand this before tuning timeouts.

Success criteria:
- Killing a node process causes the other nodes to mark it as failed within a
  bounded, observable time window (log this explicitly).

Fallback if blocked: use hashicorp/memberlist directly instead of implementing SWIM
from scratch. This is not cutting a corner on the project's value — the novel,
interview-relevant work is in the cache logic, hashing, and rebalancing built on
top of failure detection, not in re-deriving SWIM byte-for-byte. Record this choice
and its tradeoff (faster to build vs. less "from scratch") in DECISIONS.md.

---

## Phase 4: Automatic failover and request rerouting
Goal: client traffic for a dead node's keys is automatically redirected to a
surviving replica, with no manual intervention.

Key decisions to understand:
- Where routing logic lives: client-side (client tracks the ring and picks a live
  node) vs server-side (any node can proxy a request to the correct owner). Server-
  side is simpler for clients but adds a network hop; client-side is faster but
  pushes complexity to every client. Pick one, document the tradeoff.

Success criteria:
- With one node killed, client requests for keys it owned succeed via a replica,
  with no client-side awareness of which node died.

Fallback if blocked: implement server-side proxying first (simpler), defer client-
side smart routing to a stretch improvement.

---

## Phase 5: Rebalancing on join/leave (zero downtime)
Goal: when a node returns or a new node joins, keys are redistributed correctly
without dropping or duplicating data, and without stopping client traffic.

This is the hardest phase in the entire project — budget disproportionate time here.

Key decisions to understand:
- How to move keys without a window where a key exists nowhere (dropped) or on two
  conflicting nodes (duplicated/diverged). A common safe pattern: the new owner
  starts accepting writes and pulls existing data from the old owner in the
  background, only removing the key from the old owner once confirmed copied.
- What "zero downtime" actually means here: no client request should ever get a
  "key not found" for a key that legitimately exists, during a rebalance.

Success criteria:
- A node join or leave triggers a rebalance where a running client traffic
  generator sees zero missing-key errors for keys that existed before the event.

Fallback if blocked: if true zero-downtime rebalancing proves too hard in the time
available, ship a "brief pause during rebalance" version first (acceptable, and
still honest to describe as such), get it correct, then iterate toward zero
downtime. A working, honestly-described "near-zero downtime" system beats a broken
"zero downtime" claim.

---

## Phase 6: TTL consistency across replicas
Goal: TTL expiry is honored the same way on every replica of a key, not just the
primary.

Key decisions to understand:
- Independent per-node TTL clocks vs a single authoritative expiry timestamp
  replicated alongside the value. Independent clocks can drift and cause a key to
  "come back" after being read as expired on one replica. Revisit the lazy-vs-
  active sweep decision from Phase 0 here — active sweep with a replicated
  timestamp is usually the more correct answer.

Success criteria:
- A key set with a short TTL expires at (approximately) the same time on all of
  its replicas, verified by a test that checks all replicas, not just the primary.

Fallback if blocked: if perfect synchronization is hard, document the actual drift
window you observe and treat it as an accepted, explained limitation rather than
silently ignoring it — this is itself a valid tradeoff to put in the decision log.

---

## Phase 7: Chaos test harness and live-kill demo
Goal: the requirement that proves everything above actually works together.

Key decisions to understand:
- What counts as a "failed request" for the test's purposes (timeout threshold,
  retry count) — define this explicitly before writing the test, not after seeing
  results you like.

Success criteria:
- Script starts a cluster, generates traffic, kills a random node mid-traffic,
  and reports zero failed requests beyond one configurable retry — repeatably,
  across multiple runs, not just once.

Fallback if blocked: if repeatability is flaky, that's a signal of a real bug
upstream (usually in Phase 5's rebalancing or Phase 3's failure-detection timing)
— treat chaos-test flakiness as a bug to fix, not a test to loosen.

---

## Phase 8 (stretch): Eviction policy
Goal: LRU or LFU eviction per node under memory pressure.

Success criteria: setting a low memory cap and overfilling it evicts the correct
keys per the chosen policy, verified by a test.

Fallback: skip entirely if time is short — this is explicitly a stretch goal and
does not gate the core project's completeness.

---

## Phase 9 (stretch): Quorum consistency mode
Goal: an optional stricter mode where reads/writes require acknowledgment from a
quorum of replicas, as an alternative to the default eventual consistency.

Key decision to understand: this is the clearest, most interview-relevant
demonstration of the CAP tradeoff in the whole project — quorum mode sacrifices
some availability (a write can fail if quorum isn't reachable) to gain stronger
consistency guarantees. Only attempt this once phases 0-7 are solid; a half-built
quorum mode is worse for the portfolio than a solid eventually-consistent system.

Fallback: skip if time is short, but if you do attempt it, be prepared to explain
exactly what guarantee it adds over eventual consistency and what it costs.

---

## Overall fallback philosophy
If a phase's success criterion cannot be met in a reasonable amount of extra time,
it is better to ship an honestly-described, slightly weaker version (documented as
such in DECISIONS.md) than to keep pushing on a from-scratch implementation past
the point of diminishing return. The project's portfolio value comes from being
able to explain real tradeoffs you hit, including where you compromised — not from
having zero compromises.
