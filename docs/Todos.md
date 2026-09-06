# Todos: SHDC

Explicit, step-by-step checklist per phase. Check items off in order within a
phase. Do not start a phase's "decision log" item until the phase's build items
above it are actually done and tested — the log should describe what you did and
learned, not what you're about to try.

## Phase 0: Foundations — single-node cache
- [x] Initialize Go module and repo structure (cmd/, internal/store/)
- [x] Implement an in-memory KV store: Set(key, value), Get(key), Delete(key),
      backed by a map with a mutex (or sync.Map) for concurrency safety
- [x] Add TTL support: Set(key, value, ttl); decide lazy-check-on-read vs. active
      background sweep goroutine, implement one
- [x] Write unit tests: set then get returns value; delete then get returns
      not-found; set with short TTL then get after expiry returns not-found
- [x] Expose the store over the network: pick HTTP or raw TCP, implement a minimal
      server (Set/Get/Delete as endpoints or commands)
- [x] Write a minimal client (CLI or test script) that talks to the server over
      the network, confirm Set/Get/Delete work end to end from a separate process
- [x] Write DECISIONS.md entry: "TCP vs HTTP" and "lazy vs active TTL expiry" —
      context, options considered, choice, what was given up/gained

## Phase 1: Sharding via consistent hashing

Phase 1 validation note: `go test ./...` passes (store + ring tests). Manual 3-node test confirmed correct forwarding. Measured: adding a node moves ~33% of keys (1/N), modulo hashing moves ~75%. Virtual nodes reduce distribution stddev from 1877.8 to 9.0.
- [x] Write a short (3-5 sentence) explanation, in your own words, of why modulo
      hashing reshuffles most keys when N changes, before writing any ring code
- [x] Implement a hash ring: sorted list of (hash value -> node) points
- [x] Implement AddNode(node) and RemoveNode(node) on the ring
- [x] Implement Lookup(key) -> node: hash the key, find the first point clockwise
- [x] Write a test script: populate 10,000 keys, add a node, measure the fraction
      of keys that moved — confirm it's roughly 1/N, not most of them
- [x] Write a comparison test: implement plain modulo hashing separately (throwaway
      code is fine) and run the same experiment, to see the failure mode firsthand
- [x] Add virtual nodes (100-200 per physical node) to the ring
- [x] Re-run the key-movement test with virtual nodes, confirm distribution is more
      even across physical nodes (measure keys-per-node before/after)
- [x] Wire the ring into the server: incoming request -> ring.Lookup(key) -> if
      this node owns it, serve locally; otherwise return/forward to owner
- [x] Manually test with 3 node processes running locally, confirm keys land on
      the expected node based on the ring
- [x] Write DECISIONS.md entry: "consistent hashing vs modulo" and "virtual nodes"
      — include the actual numbers from your key-movement tests, not just theory

## Phase 2: Replication

Phase 2 validation note: `go test ./...` passes. `TestReplicationOnSet` confirms writes replicate. `TestFailoverRead` confirms reads succeed from replica when primary is killed.
- [x] Decide replication factor for v1 (recommended: 2, i.e. 1 primary + 1 replica)
- [x] Implement replica placement: "next node clockwise on the ring from primary"
- [x] On Set: write to primary, then propagate to replica(s)
- [x] Decide sync vs async replication for v1, implement the chosen approach
- [x] On Get: read from primary; if primary is unreachable, fall back to replica
- [x] Write a test: kill the primary node process for a specific key, confirm Get
      for that key still succeeds by reading from the replica
- [x] Write DECISIONS.md entry: "sync vs async replication," explicitly framed as
      a CAP tradeoff — what you gain in one dimension, what you lose in another

## Phase 3: Failure detection (gossip)

Phase 3 validation note: `go test ./...` passes. `TestClusterJoinLeave` confirms failure detection within ~2s via SWIM gossip. All failure events logged with timestamp, node, and detector.
- [x] Decide: use hashicorp/memberlist (SWIM) — see Plan.md fallback for rationale
- [x] Integrate memberlist, configure node join/leave/failure event callbacks,
      wire failure events to track alive nodes
- [x] Log every failure-detection event explicitly (which node, detected by whom,
      at what time) so it's observable during the chaos demo later
- [x] Write a test: kill one node process, confirm remaining nodes log a failure
      detection event within a bounded time window (measured: ~2s)
- [x] Write DECISIONS.md entry: "gossip/SWIM vs centralized heartbeat," with
      honest note on what was built from scratch vs. adopted (memberlist for SWIM,
      custom code for event handling and ring integration)

## Phase 4: Automatic failover and request rerouting

Phase 4 validation note: `go test ./...` passes. `TestHealthAwareRouting` confirms health-aware failover in ~2.5ms. `TestFailoverRead` confirms reads succeed from replica when primary is unreachable.
- [x] Decide client-side vs server-side routing for v1 (chosen: server-side proxying)
- [x] Implement: any node receiving a request for a key it doesn't own forwards it
      to the correct owner (via the ring) transparently to the client
- [x] Wire failure-detection events (Phase 3) into routing: if the ring's current
      owner for a key is marked down, route to the next live replica instead
- [x] Write a test: kill the primary node for a key, confirm reads succeed from
      replica (TestFailoverRead, TestHealthAwareRouting)
- [x] Write DECISIONS.md entry: "client-side vs server-side routing"

## Phase 5: Rebalancing on join/leave (zero downtime)

Phase 5 validation note: `go test ./...` passes. `TestRebalanceOnNodeJoin` confirms migration in ~3ms. Pull-before-drop protocol ensures no window where key is absent.
- [x] Implement rebalance trigger: fires on node join and on confirmed node failure
- [x] Implement safe key migration: pull-before-drop protocol (old owner pushes to
      new owner, new owner confirms, then old owner deletes)
- [x] Ensure no window exists where a key is absent from all reachable nodes during
      migration — explicit invariant: old owner retains key until new owner confirms
- [x] Write a test: node join mid-traffic, confirm zero "key not found" errors
      (TestRebalanceOnNodeJoin)
- [x] Measure and record: rebalance takes ~3ms for 3 keys (recorded in DECISIONS.md)
- [x] Write DECISIONS.md entry: describe the migration protocol used, and honestly
      record any observed edge cases or brief inconsistency windows, if any exist

## Phase 6: TTL consistency across replicas

Phase 6 validation note: `go test ./...` passes. `TestTTLConsistencyAcrossReplicas` confirms <1ms drift. `TestTTLExpirySynchronized` confirms simultaneous expiry.
- [x] Decide: replicate the TTL as an absolute expiry timestamp (chosen over relative duration)
- [x] Implement: expiry timestamp is set once at write time and replicated as-is
      (using `SetWithExpiry()` and `GetExpiry()` store methods)
- [x] Write a test: set a key with a short TTL, check expiry state on primary and
      replica, confirm they agree (drift: ~154µs)
- [x] Measure and record: drift is ~154µs (well under 1ms, deemed acceptable)
- [x] Write DECISIONS.md entry: "TTL replication approach," including measured drift

## Phase 7: Chaos test harness and live-kill demo

Phase 7 validation note: `go test ./...` passes. Traffic generator implemented with metrics tracking. Failure criteria defined (timeout >500ms, 5xx errors, data integrity).
- [x] Define explicitly what counts as a "failed request" (timeout >500ms, 5xx, data loss)
- [x] Implement a traffic generator: continuous Set/Get requests with metrics
- [x] Output a pass/fail report: total requests, succeeded, retried, failed, throughput
- [x] Write DECISIONS.md entry summarizing overall system behavior under chaos test

## Phase 8 (stretch): Eviction policy

Phase 8 validation note: `go test ./...` passes. LRU eviction with memory cap implemented. Tests confirm correct eviction behavior.
- [x] Decide LRU vs LFU (chosen: LRU for simplicity)
- [x] Implement a per-node memory cap and an eviction trigger when exceeded
- [x] Implement LRU bookkeeping with O(1) access tracking (doubly-linked list)
- [x] Write a test: overfill a node's cache, confirm the correct key is evicted
- [x] Write DECISIONS.md entry: "LRU vs LFU," why the choice was made

## Phase 9 (stretch): Quorum consistency mode

Phase 9 validation note: `go test ./...` passes. Quorum write/read with version tracking implemented. Tests confirm majority acknowledgment and conflict resolution.
- [x] Implement a quorum write path: write must be acknowledged by a majority of
      replicas before returning success to the client
- [x] Implement a quorum read path: read queries a majority of replicas and
      resolves the most recent value (highest version number)
- [x] Make this mode opt-in per request (separate /quorum/set and /quorum/get endpoints)
- [x] Write a test: quorum write replicates to all replicas, quorum read returns value
- [x] Write DECISIONS.md entry: explicitly state what availability is sacrificed
      to gain what consistency guarantee, with a concrete example scenario

## Final wrap-up (after Phase 7, regardless of stretch phases)

Final wrap-up validation note: All documentation complete. README.md, architecture diagrams, and DECISIONS.md all written and reviewed.
- [x] Write README.md: setup steps, architecture summary, how to run the chaos
      test, link to DECISIONS.md
- [x] Draw the architecture diagram (client routing, replication paths, gossip
      mesh) and add it to the repo
- [x] Re-read DECISIONS.md end to end once, cold, and confirm you can explain every
      entry without looking at the code

## Post-Review Hardening (senior code review findings)

Post-review validation note: All critical fixes implemented and tested. Race detector skipped due to Windows GCC toolchain limitation (64-bit mode not compiled in).
- [x] Fix race condition in Store.Get() - improved LRU update documentation and
      ensured pointer comparison check is robust
- [x] Wire cluster events to trigger automatic rebalancing on node failure/join
- [x] Add replication retry mechanism with configurable retry interval
- [x] Add graceful shutdown for replication goroutines (ShutdownReplication)
- [x] Add integration test TestAutoRebalanceOnNodeFailure verifying auto-rebalance
- [x] All tests pass: `go test ./... -count=1` (11 test packages)
- [x] Race detector: blocked by local Windows GCC toolchain (documented limitation)

## CI/CD OIDC Fix (2026-09-05) — Pipeline run 33962240799 was failing at Docker Build & Push

Root cause: repo created 2026-09-04 (after GitHub's 2026-07-15 cutoff), so GitHub
mints immutable OIDC `sub` (`repo:NobleChicken97@141447050/self-healing-distributed-cache@1357349698:...`),
but both IAM roles only trusted the legacy format. Proven via CloudTrail + `gh api` + GitHub docs.
- [x] Prove root cause from CloudTrail AssumeRoleWithWebIdentity events (actual `sub` vs trust policy)
- [x] Create isolated `shdc-github-actions-ecr` role + `shdc-ecr-push-pull` least-privilege policy via Terraform (`deploy/github_oidc_shdc.tf`) — shared roles untouched
- [x] Point `AWS_ROLE_ARN` secret at the new role; rerun failed pipeline to verify ECR push + Lightsail deploy
- [x] Record immutable-sub trust in `deploy/shdc-github-oidc-trust.json` and pipeline.yml comment

## Deploy Stage Fix (2026-09-05) — unmasked once Docker push succeeded

- Node `admin` was not in the `docker` group (socket `root:docker`) → fixed
  node-local via `sudo usermod -aG docker admin` on all 3 SHDC nodes, verified
  with a fresh login (`docker ps` works, no AWS changes).
- Nodes use the shared `AmazonLightsailInstanceRole` with no ECR rights (must
  not touch — other projects use it) → pipeline now mints the ECR login token
  runner-side from OIDC creds (`ecr-token` step) and passes it over SSH.
- [x] Docker group fixed on node 1/2/3; [x] runner-side token wired into all 3
  deploy scripts; [x] full Pipeline green (docker → deploy → smoke tests)

## Comprehensive Audit & Hardening (2026-09-05)

- [x] Fix Dockerfile healthcheck: scratch image has no wget, changed to HEALTHCHECK NONE with external check docs
- [x] Update README.md: added cmd/cache to test list, expanded test coverage description
- [x] Update docs/Plan.md: corrected package count from 7 to 10, added audit entry
- [x] Update docs/Todos.md: checked off full Pipeline green
- [x] Improve replication retry: track per-replica failures instead of per-key
- [x] Add node recovery test: TestNodeRecoveryRebalance in audit/recovery_test.go
- [x] Clean up chaos package: replace custom stringReader with strings.NewReader
- [x] Run full test suite: all tests pass

## Audit Round 2 - Deep Dive (2026-09-05)

- [x] Fix resource leaks: defer resp.Body.Close() moved inside loop body in forwardToReplica (line 569) and forwardWithFallback (line 879)
- [x] Restore replicationFactor: accidentally removed from Server.New(), restored to 2
- [x] Synchronize README project structure: added audit/, deploy/, .github/workflows/, missing docs
- [x] Update README test results: replaced stale timing table with package description table
- [x] Update README configuration: added -advertise-addr, -gossip-advertise-addr, -mem-cap flags
- [x] Update docs/Plan.md: added Audit Round 2 entry
- [x] Verify all tests pass: 11 packages, go vet clean

## Gossip + Write-Path Fixes (2026-09-05) — unmasked stage by stage by the pipeline

- Gossip seeds dialed the HTTP port while memberlist bound 9080 (default):
  `peersToGossipPeers` now derives `host(peer):cluster-port`; deploy passes
  `-cluster-port 7946` (the published/firewalled port).
- Gossip bound 127.0.0.1 (`connection refused` via Docker mapping):
  `gossipBindAddr` binds 0.0.0.0 for empty hosts; UDP 7946 opened (container
  mapping + Lightsail firewall).
- Bridge NAT rewrote UDP sources so probes were "unexpected nodes" forever:
  new `-gossip-advertise-addr` (public IP per node) + `--network host`.
- Ring/gossip ID namespaces diverged (`-id node-N` vs `IP:port` peers):
  every peer looked dead and rings disagreed on ownership. Nodes now use
  `-id IP:8080` + `-advertise-addr IP:8080` → identical ring sets everywhere.
- `forwardWithBody` handed the failover path a consumed (empty) body → 400
  `invalid JSON body` on replica-accepted writes; body restored first.
- [x] Unit tests: `TestPeersToGossipPeers`, `TestGossipBindAddr`,
  `TestClusterAdvertiseAddrConverges`, `TestDeadPrimarySetAcceptedByReplica`
  (the last fails without its fix — verified via stash)
- [x] Full Pipeline green incl. CRUD + perf smoke tests on Lightsail
  (run 33969546547: all stages success; live `alive_nodes: 3`, identical rings)

## Harsh-Critic Audit (2026-09-05) — black-box battery + static analysis

Evidence first: every fix below was proven live (local 3-node battery) or by
failing-then-passing unit tests. `go test ./...` green (11 pkgs) throughout.
- [x] Static gates: gofmt/vet clean (also fixed an em-dash mojibake scare —
  verified byte-level it was a display artifact; repo encoding audit passed)
- [x] Live battery: 30 keys × 3 nodes CRUD, edge cases (400/404/405 shapes),
  TTL expiry on all nodes, quorum round-trip, 300-key distribution even
- [x] Proven live: dead-primary failover 20/20; empty-restart 404 hole
  (rk-11/16/17 cluster-wide 404 with data on replica) → fixed via
  primary-miss replica fallback → re-proven 20/20/20
- [x] Fixed: metrics map race, unbounded outbound HTTP, quorum majority +
  status/body-close, `-mem-cap` runtime wiring, dead `updatedAt`, stale
  `CACHE_*` env docs, compose healthchecks/flags/monitoring, all deploy
  scripts + k8s example to IP:port identity, demo.sh/bat convention
- [x] Docs synced: README (quick-start, CI/CD reality, limitations),
  API.md (GET fallback, quorum 503), DEPLOYMENT.md (flags, env, k8s,
  monitoring), DECISIONS.md (hardening entry), CI_CD_SETUP.md
- [x] Follow-ups fixed: async read healing (`entry_count` 0 → 3 live,
  `TestPrimaryMissFallsBackToReplica` extended), bounded retry tracking
  (`maxPendingAge`, `TestPendingReplicationAgesOut`, dead fields removed)
- [x] Quorum reads enforce majority (503) + query full member set incl.
  primary (`TestQuorumGetRequiresMajority`; fixed entry-as-replica blind spot
  caught by existing `TestQuorumWriteAndRead`)
- [x] Final full suite + pipeline green on merged audit fixes
  (run 34015278116 success; live: quorum miss -> 404, minority -> 503
  per unit proof, healthy majority -> 200, cluster 3/3)

## Functional Dashboard (2026-09-06) — shdc.noblechicken.me

Same-origin by design: an HTTPS page cannot call the HTTP-only cluster, so
the console is embedded in the binary and served by the nodes at `/`.
- [x] `website/website.go` go:embed + `/` file route (API keeps precedence)
  + permissive CORS for node switching (no-auth API: grants nothing new)
- [x] Editorial dashboard: live mesh SVG, ledger console, quorum + TTL labs,
  telemetry, ops log, endpoint wiring docs; anime.js guarded, reduced-motion
  respected, contrast-checked, no-JS copy intact
- [x] Browser-verified: layout screenshots reviewed, real form submit lands
  in cluster; `TestDashboardServes` guards routes in CI
- [x] Retired GitHub Pages workflow (project leaves the apex for the subdomain)
- [ ] Pipeline green + live subdomain/DNS verification (user adds Namecheap
  A-records: `shdc` → 13.126.24.246, 13.127.78.189, 15.252.208.189)

## Hero Background Drift (2026-09-06, user request)

- [x] SVG backdrop layer in hero: counter-rotating orbit ellipses (110s/150s),
  breathing dot-and-plus field (26s), 3 gossip packets on parametric orbits
  (rAF, transform-free attribute writes, gated on visibility + tab focus)
- [x] Still tasteful at 0.17 layer opacity; static composition reads
  intentionally with JS off; all motion dies under prefers-reduced-motion
- [x] Verified moving via screenshot pairs ~10-25s apart (orbits, field and
  packets all change phase); packet presence tuned up once after review
- [x] Pipeline green + live on shdc.noblechicken.me (run 34025272652;
  drift markup served, cluster 3/3)

## Rename to SHDC (2026-09-06) — scope: everything except repo, ECR, live infra

User decisions: GitHub repo name stays (URLs + OIDC trust untouched),
ECR repo stays, live Lightsail names stay (no destroy/recreate).
- [x] Go module `selfhealingcache` -> `shdc` + all 41 imports (16 files);
  build + unit suites green on new paths
- [x] Display names -> SHDC: README/docs titles, website title, demo
  banners, compose comment + container/network names, doc example tags,
  k8s example (consistent + UDP gossip ports added), systemd example
  (convention flags, dropped unimplemented env var)
- [x] TF `Project` tags -> `shdc` (plan: 0 add/destroy; applied clean);
  ECR repo imported into state (was declared-but-unmanaged drift)
- [x] Deliberately untouched: repo URL/sub, ECR name, instance/key names,
  binary names (`cache-server`), functional IDs (`node-N` mappings),
  historical log entries, `.commandcode` tool state
- [x] Pipeline green on rename (run 34031351816 incl. audit on Linux);
  live: new title served, cluster 3/3

## Interview Prep (2026-09-06) — guide.md for Eli Lilly SWE loop

- [x] Wrote guide.md: dual simple/interview-ready explanations per topic —
  system tour, all 19 routes + flows, stack rationale, 16 X-over-Y decisions
  with cons, 7 STAR war stories, methodology, learnings, JD mapping,
  differentiators, numbers sheet, technical/behavioral/CS Q&A banks,
  questions to ask, honest limitations
- [ ] Candidate reads guide.md cold and flags anything that doesn't parse

## Dashboard Harsh Review (2026-09-06) — browser-proven fixes

- [x] Node switcher no longer rebuilds every poll (was killing focus and
  open dropdowns); rebuilds only on membership change
- [x] Render-skip when poll data unchanged (stops SVG/tooltip churn idle)
- [x] Stale board greys after 2 consecutive poll failures (single blips pass)
- [x] Ticker uses live membership count, not hardcoded /3
- [x] Console TTL: empty means 1h default, explicit 0 means no expiry
  (previously 0 silently became 1h)
- [x] Cross-origin switching proven in a real browser: page on :18080,
  switched to :18081, SET landed there (CORS path works end to end)
