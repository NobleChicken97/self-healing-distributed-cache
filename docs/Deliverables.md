# Deliverables

Each deliverable below has a concrete acceptance criterion. If it can't be checked
off against its criterion, it isn't done yet.

## 1. Source code repository
- Structure: cmd/ (entrypoints), internal/ring (hashing), internal/gossip (failure
  detection), internal/replication, internal/store (KV + TTL), cmd/chaos (test tool).
- Acceptance: a fresh clone builds and runs an N-node cluster locally via a single
  script or docker-compose file, no manual steps beyond that.

## 2. Architecture diagram
- One diagram showing: client request routing via the hash ring, replication paths
  between primary and replica nodes, and the gossip mesh between all nodes.
- Acceptance: someone unfamiliar with the project could trace a single write's path
  through the system using only this diagram.

## 3. Decision log (DECISIONS.md)
- One entry per major design choice, each with four parts: Context / Options
  considered / Choice made / What we gave up and what we gained.
- Minimum required entries: hashing scheme, failure detection protocol, replication
  factor and placement strategy, consistency model, language choice.
- Acceptance: can be read cold, five minutes before an interview, and used to answer
  "why did you choose X over Y" without re-deriving the reasoning from scratch.

## 4. Chaos test suite
- A script that: starts an N-node cluster, generates continuous client traffic,
  kills a random node mid-traffic, asserts zero failed requests beyond one
  configurable retry, asserts keys eventually rebalance to surviving nodes, restarts
  the killed node, and asserts rebalancing occurs again on rejoin.
- Acceptance: runs repeatably (not just once by luck) and produces a pass/fail
  report with numbers — requests sent, requests succeeded, requests retried, time
  to full rebalance.

## 5. Demo recording
- A short screen recording (2-4 minutes) of the chaos test running live, narrated:
  a node dies, traffic keeps flowing, the node restarts, the cluster rebalances.
- Acceptance: someone who has never seen the project can watch this and understand
  what "self-healing" means in concrete terms, not just as a buzzword.

## 6. README.md
- Setup instructions, a short architecture summary, how to run the chaos test, and
  a link to the decision log.
- Acceptance: someone unfamiliar with the project can clone the repo and get the
  chaos-test demo running in under 10 minutes.

## 7. Benchmark and observability output (recommended, not mandatory)
- Basic numbers: request latency in steady state vs. during a node failure, and key
  redistribution count triggered by a single node join or leave.
- Acceptance: these numbers exist somewhere written down and get referenced when
  discussing the cost of rebalancing — "it moved N keys and added Xms latency" beats
  "it rebalances" in an interview.
