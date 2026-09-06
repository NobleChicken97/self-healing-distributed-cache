# PRD: SHDC

## 1. Summary
Build a distributed, in-memory key-value cache from scratch that shards data across
multiple independent nodes, replicates keys for fault tolerance, detects node failures
automatically via gossip, and rebalances data with zero downtime. The goal is to prove
hands-on understanding of distributed systems fundamentals, not just usage of an
existing tool like Redis or Memcached.

## 2. Problem Statement
Current project portfolio (Viber, semantic caching proxy, NetSentry, hybrid RAG,
agentic guardrails) is heavy on LLM/AI-wrapper work. This creates two gaps:

1. No project demonstrates systems/backend depth independent of AI.
2. No project demonstrates cloud/infra-at-scale thinking (replication, consistency,
   fault tolerance, rebalancing).

This project directly addresses both gaps, and is broad enough to be relevant both
for generalist SDE roles (e.g. Eli Lilly's IT stack req) and for FAANG-style systems
design interviews.

## 3. Goals
- Build a cache cluster of 3-5 nodes that behaves as one logical cache.
- Survive a live node kill mid-traffic with zero failed client requests (beyond one
  configurable retry).
- Be able to explain, unprompted, the tradeoff behind every major design decision:
  hashing scheme, consistency model, failure detection mechanism, replication factor.
- Produce a demoable artifact: a chaos-test script and recording that proves
  self-healing rather than just claiming it.

## 4. Non-Goals (v1)
- Not building a general-purpose database — no complex queries, no transactions,
  no multi-key atomicity.
- Not implementing strong consistency (Raft/Paxos) in the core scope — quorum
  consistency is a stretch goal only, not a requirement.
- Not optimizing for raw throughput against real Redis — correctness and resilience
  are the priority, not performance parity.

## 5. Target Use Case
Portfolio and interview artifact. Primary audience: technical interviewers assessing
systems-design maturity for SDE/backend roles. Secondary and arguably more important
audience: the builder's own understanding — this is a learning project first, a
portfolio piece second.

## 6. Functional Requirements
FR1. Run as multiple independent cache nodes that together form one logical cluster.
FR2. Use consistent hashing (not modulo) to distribute keys, so adding or removing a
     node reshuffles a minimal number of keys.
FR3. Replicate every key to at least one additional node; reads must still succeed
     if the primary holder of a key goes down.
FR4. Detect a failed node automatically (heartbeat/gossip) and reroute its traffic
     without manual intervention.
FR5. When a failed node returns (or a new node joins), automatically rebalance keys
     with zero downtime for clients.
FR6. Support TTL-based expiry, honored consistently across all replicas of a key.
FR7. Demonstrate the cluster surviving a live kill of a random node mid-traffic with
     zero failed client requests (beyond one configurable retry).

## 7. Non-Functional Requirements
NFR1. Language: Go — chosen for concurrency primitives and mature gossip libraries.
      Tradeoff recorded in the decision log rather than assumed.
NFR2. Every node failure and recovery event must be observable via logs (or a simple
      dashboard) — not handled silently internally.
NFR3. Chaos testing must be repeatable via script, not a one-off manual demo.
NFR4. Every major design decision must be logged with a stated tradeoff — see
      Deliverables.md for the decision-log format.

## 8. Stretch Goals (only after core is solid)
SG1. LRU/LFU eviction policy under memory pressure, per node.
SG2. Cluster-wide quorum consistency mode (quorum reads/writes) as an optional
     stricter alternative to eventual consistency.

## 9. Success Metrics
- All 7 functional requirements demonstrably working, provable via the chaos-test
  script's output.
- Decision log covering at minimum: hashing scheme, failure detection mechanism,
  replication strategy, consistency model — each with an explicit "what we gave up"
  line, not just "what we chose."
- Able to explain the full design cold, without notes, in a mock-interview setting.
