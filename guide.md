# Interview Guide — SHDC × Eli Lilly SWE

> How to use this: every topic has two versions. **Say it simple** = the idea in plain words (use when they ask "explain it to me simply" or to open an answer). **Interview-ready** = the same idea with the vocabulary, numbers, and tradeoffs that signal senior thinking. Never recite — internalize the simple version, then layer the precise terms.
>
> The JD boils down to: *learns fast, knows CS fundamentals, owns the full lifecycle, thinks critically, communicates well, ships reliably.* Every section below ends with the Lilly trait it proves — steal those lines verbatim when they ask behavioral questions.

## Table of contents

1. [The 30-second pitch](#1-the-30-second-pitch)
2. [What the app is (system tour)](#2-what-the-app-is-system-tour)
3. [Routes and request flows](#3-routes-and-request-flows)
4. [Tech stack and why each tech](#4-tech-stack-and-why-each-tech)
5. [Decisions: X over Y, with honest pros and cons](#5-decisions-x-over-y-with-honest-pros-and-cons)
6. [War stories: problems we faced and how we solved them](#6-war-stories-problems-we-faced-and-how-we-solved-them)
7. [Mentality and methodology](#7-mentality-and-methodology)
8. [Learnings](#8-learnings)
9. [How this aligns with Lilly (JD mapping)](#9-how-this-aligns-with-lilly-jd-mapping)
10. [What separates me from peers](#10-what-separates-me-from-peers)
11. [Numbers cheat-sheet](#11-numbers-cheat-sheet)
12. [Technical grilling Q&A bank](#12-technical-grilling-qa-bank)
13. [Behavioral Q&A bank (STAR)](#13-behavioral-qa-bank-star)
14. [Non-project CS Q&A bank](#14-non-project-cs-qa-bank)
15. [Questions to ask them](#15-questions-to-ask-them)
16. [Honest limitations + what I'd do next](#16-honest-limitations--what-id-do-next)

---

## 1. The 30-second pitch

**Say it simple:** "I built a mini-Redis that runs on three cloud servers, copies every key twice, notices within seconds when a server dies, and keeps answering from the surviving copy — then I put a live mission-control dashboard on it. The whole thing ships itself through a pipeline."

**Interview-ready:** "SHDC is a sharded, replicated, eventually-consistent key-value store in Go: consistent hashing with 150 virtual nodes, replication factor 2 with async propagation, SWIM gossip via hashicorp/memberlist for failure detection, server-side request routing with replica failover, absolute-timestamp TTL replication, and an opt-in quorum mode. It deploys rolling to three AWS Lightsail nodes through a GitHub Actions → ECR pipeline with OIDC auth, health gates, and smoke tests, and each node serves an embedded live operations dashboard."

*Lilly trait proved: can compress complexity without losing accuracy — the core of good stakeholder communication.*

---

## 2. What the app is (system tour)

**Say it simple:** Three identical programs on three servers pretend to be one big dictionary. A math ring decides which server owns each word. Every word is also photocopied onto the next server. The servers constantly whisper "are you alive?" to each other, and if one goes quiet, the others answer for it.

**Interview-ready:** Three stateless-ish Go nodes (in-memory store, no disk) form a cluster. The hash ring maps each key to a primary; the next node clockwise holds the replica. Writes are primary-then-async-replicate with absolute expiry timestamps. Reads hit the primary, fall back to replicas on death *or* on primary-miss (restart-wiped primaries heal from replicas). SWIM gossip tracks liveness; routing consults it. An embedded dashboard exposes mesh, CRUD console, quorum/TTL labs, and telemetry. CI builds, tests, containerizes, pushes to ECR, and rolling-deploys with health + membership + smoke gates.

*Lilly trait proved: systems thinking — seeing components and their interactions, not just code.*

---

## 3. Routes and request flows

### 3.1 Route table (all 19 — know the starred ones cold)

| Method | Route | Purpose | Key statuses |
|---|---|---|---|
| POST | `/set` | Write key (`key`, `value`, `ttl_ms`; `0` = no expiry) | 200 ok · 400 bad JSON/empty key/negative TTL · 405 wrong method |
| GET | `/get?key=` | Read; primary → replica fallback → replica-heal → 404 | 200 · 404 · 400 missing param |
| DELETE | `/delete?key=` | Delete; replicated to replicas | 200 |
| POST | `/quorum/set` | Majority-ack write, returns `version` | 200 · 503 `quorum_failed` |
| GET | `/quorum/get?key=` | Majority read, highest version wins | 200 · 404 all-miss · 503 minority |
| GET | `/ring/info` | Ring membership (`node_id`, `ring_nodes`) | 200 |
| GET | `/cluster/info` | `alive_count`, `members`, node id | 200 |
| GET | `/health` | LB-style health (`healthy`/`degraded`) | 200 |
| GET | `/metrics` | entries, memory, cap, eviction flag, pending repls, alive | 200 |
| POST | `/rebalance` | Trigger migration (202 accepted, runs async) | 202 |
| GET | `/rebalance/status` | Last result: moved/failed/duration | 200 |
| GET | `/` | Embedded dashboard (HTML) | 200 |
| POST | `/replica/set|delete`, GET `/replica/get`, POST `/replica/quorum/set` | Internal replication (serve local, never forward) | 200/404 |
| POST | `/rebalance/pull|accept|complete` | Migration protocol steps | 200/404 |

**Say it simple:** "Public counter, staff-only back room. Client routes never recurse — internal routes always answer from their own shelf, which is what makes forwarding loops impossible."

**Interview-ready:** "The forwarding invariant: public handlers may proxy, internal `/replica/*` handlers only touch the local store. That single rule eliminates an entire class of routing loops, and the primary-miss fallback deliberately uses `/replica/get` for exactly that reason."

### 3.2 Write flow (`POST /set`)

**Say it simple:** "Give the word to any clerk. He checks the ring roster — if it's his word, he files it and photocopies it to the next clerk later. If it's someone else's, he walks it over. If the owner called in dead, he gives it to the backup."

**Interview-ready:** "Decode → validate → `ring.Lookup`. Owner-local: `store.Set`, compute absolute `expiresAt`, async `replicateSet` with retry tracking. Non-local: remarshal and forward; dead primary → `forwardToReplica` (body restored first — that was a real 400 bug we fixed). Replication is async for p99 write latency at the cost of a small loss window — the documented CAP tradeoff."

### 3.3 Read flow (`GET /get`)

**Say it simple:** "Ask anyone. They check the roster — if it's theirs and they have it, answer. If their copy burned in a restart, they phone the backup, answer you, and quietly re-file their own copy. If it's someone else's, they forward you."

**Interview-ready:** "Owner-local hit serves directly. Owner-local miss consults replicas via `/replica/get` then asynchronously heals with the replica's exact expiry (store-direct, no re-replication fan-out) — genuine misses cost replica probes. Non-local goes through `forwardWithFallback`: alive primary first, else alive-then-dead replicas, serving locally when self is the replica."

### 3.4 Quorum flow (opt-in strict mode)

**Say it simple:** "Normal mode trusts whoever answers first. Strict mode takes a vote: a write counts only if most servers confirm, and a read asks everyone and believes the newest version. Fewer lies, more waiting — and if not enough servers answer, it says so instead of guessing."

**Interview-ready:** "Quorum writes assign `version = local+1`, persist locally, then synchronously replicate the versioned value; success needs `floor(RF/2)+1` acks. Quorum reads collect local + full member set (primary *plus* replicas — an earlier implementation missed the primary whenever the entry node was a replica, making majority unreachable by construction) and return highest version, else 503. Absent-everywhere is still an honest 404 — unanimous negative responses are a quorum answer."

### 3.5 Gossip + rebalance + TTL in one breath

**Say it simple:** "Servers ping each other constantly; silence means death within ~2 seconds, and traffic reroutes instantly. Moving day uses pull-before-drop: the new owner grabs a copy first, confirms, and only then does the old owner throw theirs away — so a word is never nowhere. Expiry dates are stamped once and photocopied, so all copies vanish in the same millisecond."

**Interview-ready:** "memberlist SWIM with 7946/TCP+UDP; topology events feed health-aware routing and rebalance triggers. Migration is pull → accept → complete, old owner deletes only on confirmation. TTLs replicate as absolute `expires_at_ms` (measured drift ~154µs, processing-time only). Retries run every 5s per failed (key, replica) pair, bounded by a 24h pending-age cap with warn logs."

*Lilly trait proved: can explain the same system at multiple altitudes — essential for mixed technical/non-technical stakeholders.*

---

## 4. Tech stack and why each tech

| Tech | Role | Why this, not the alternative |
|---|---|---|
| **Go 1.25** | Entire backend | Goroutines/channels make gossip + replication + TTL sweeps tractable; Python risks subtle concurrency bugs, Rust's learning curve wasn't worth the portfolio timeline. Middle-ground, stated upfront. |
| **hashicorp/memberlist (SWIM)** | Failure detection | Adopted, not re-derived: the interview value is in cache logic/hashing/rebalancing *on top of* gossip, not re-proving SWIM byte-for-byte. Honestly logged as such. |
| **HTTP + JSON API** | Client protocol | Debuggable and demoable over raw TCP (closer to Redis, but opaque in demos). Correctness first, wire-protocol purity later. |
| **Docker (scratch image)** | Packaging | ~static binary, tiny surface, non-root user. Taught us: scratch has no shell/wget — healthchecks must live outside the container. |
| **GitHub Actions → ECR → Lightsail** | CI/CD + hosting | Full pipeline (lint → 3-OS tests → integration → builds → Docker → rolling deploy with health/membership/smoke gates). Lightsail Nano 3×$5 over EC2/EKS: zero VPC overhead for 3 static nodes. ECR over GHCR: IAM-native auth via OIDC. |
| **Terraform** | Infra as code | Lightsail + firewall + IAM role/policy codified; state deliberately local (documented decision, zero shared-backend risk). |
| **AWS IAM OIDC federation** | Deploys without keys | Short-lived tokens via `token.actions.githubusercontent.com`; dedicated least-privilege role (`shdc-*`), shared roles untouched. FullAccess would have been lazy and blast-radius-sloppy. |
| **Vanilla JS + anime.js (CDN, guarded)** | Dashboard | Zero build step (it embeds into the Go binary). Component libraries were deliberately rejected — they'd inject the exact visual sameness the brief banned. Motion degrades to instant states; content renders with JS off. |
| **Fraunces / Archivo / IBM Plex Mono** | Type system | Three roles, three families on contrast axes (serif display / grotesk UI / mono data) — never two similar sans-serifs. |
| **CloudTrail, `gh`, `nslookup`, black-box scripts** | Forensics kit | Every production claim was proven from evidence, never assumed. |

*Lilly trait proved: technology choices with explicit reasoning and rejected alternatives — "willingness to learn any technology" backed by demonstrated evaluation skill.*

---

## 5. Decisions: X over Y, with honest pros and cons

1. **HTTP over raw TCP** — Pro HTTP: curl-debuggable, dashboard-friendly, faster to correct. Con: not wire-compatible with Redis clients, more bytes per op. Verdict: right for v1; protocol purity is a later optimization, not a correctness issue.
2. **Active TTL sweep over lazy-only** — Pro active: replicas expire in sync (revisit in Phase 6 paid off). Con: a per-second full-map write lock (fine at this scale; snapshot-then-delete if it ever matters).
3. **Consistent hashing over modulo** — Measured, not asserted: adding a node moves ~1/N (~33%) vs ~75% for modulo; vnodes cut distribution stddev from 1877.8 to 9.0. Con: 150 vnodes × N points to sort — trivial cost, real evenness.
4. **Async replication (RF=2)** — Pro: p99 write latency, demo-friendly. Con: primary can die before replicating (documented loss window). CAP stated plainly: availability + partition tolerance, eventual consistency.
5. **Adopted SWIM over hand-rolled gossip** — Pro: months of debugging avoided, focus stayed on novel logic. Con: less "from scratch" bragging rights. Verdict: portfolio value is in explained tradeoffs, and this tradeoff itself is one.
6. **Server-side routing over client-side** — Pro: dumb clients, demoable from curl. Con: extra network hop. Defer smart clients, don't gold-plate.
7. **Absolute over relative TTL replication** — kills clock-drift resurrection bugs at the cost of one timestamp field.
8. **LRU over LFU** — simpler bookkeeping, O(1) with doubly-linked list; LFU frequency sketches weren't justified.
9. **Quorum opt-in over quorum-default** — strict mode available without taxing the hot path; honest 503s instead of confident lies.
10. **Lightsail over EC2/EKS** — Pro: static IPs, flat $15/mo, no VPC ceremony. Con: no autoscaling/IAM instance profiles (which directly caused the runner-side ECR token design — constraints breeding design).
11. **OIDC over long-lived AWS keys** — Pro: nothing to leak/rotate; per-repo least-privilege. Con: the immutable-`sub` format broke us once (fixed with exact CloudTrail forensics).
12. **Same-origin dashboard over GitHub Pages hosting** — an HTTPS page cannot call the HTTP-only cluster (mixed-content wall, proven by spec not opinion). Embedding kills CORS/TLS/proxy problems in one move. Con: binary grows ~200KB; page version ties to deploy version (acceptable — they're one unit).
13. **Host networking over bridge port-mapping for gossip** — bridge NAT rewrote UDP probe sources so SWIM classified every probe "unexpected node" forever. Host networking removes the NAT path entirely. Con: less container isolation on single-purpose nodes — fine.
14. **`IP:port` identity over pretty `node-N` names** — mismatched namespaces diverged every ring and broke liveness matching (the ~50% write-400 outage). Con: uglier logs. Correctness beats cosmetics; say that sentence in the interview.
15. **Read-healing over write-back-on-restart** — heals lazily per read (cheap, proven live 0→3 entries) instead of bulk bootstrap machinery. Con: genuinely-absent keys cost replica probes; a delete racing a heal can zombie a copy for one TTL window (documented, accepted).
16. **Age-bounded retries over counted retries** — dropping after "3 tries" (~15s) would harm durability; 24h age-bound with warn logs bounds memory without giving up early.

*Lilly trait proved: critical thinking + attention to detail — every choice has a receipt (measurement or log line), and cons are stated, not hidden.*

---

## 6. War stories: problems we faced and how we solved them

Use STAR shape (Situation → Task → Action → Result) for each. Lead with the OIDC one — it's your strongest "debugged production" story.

1. **OIDC immutable subject (the flagship story).** S: pipeline dead at AWS auth, 12 retries, `Not authorized to perform sts:AssumeRoleWithWebIdentity`; a permissions fix (PR #3) hadn't helped. T: unblock deploys without touching teammates' shared AWS roles. A: ignored guesses, pulled CloudTrail `AssumeRoleWithWebIdentity` events — the token `sub` carried `@ownerID/@repoID` suffixes the trust policy didn't match; confirmed IDs via `gh api`, confirmed the cause in GitHub's post-2026-07-15 immutable-sub rollout docs. R: new isolated least-privilege role, secret repointed, pipeline green; shared roles untouched. *Line to steal: "I don't guess at IAM — CloudTrail shows the exact evaluated principal."*
2. **The unmasking chain.** S: each fix revealed the next failure (Dockerfile inline-comment EXPOSE → sha-tag mismatch → node ECR auth → docker group → gossip port → bind address → UDP firewall → advertise address → ring namespaces → consumed-body 400s). T: get the pipeline honestly green. A: fixed each layer with evidence (container logs, not theories), added a regression test per bug. R: full green + smoke tests. *Line: "Fixing the symptom that hides five bugs isn't progress — I kept a chain-of-evidence log."*
3. **Gossip that could never converge.** S: `alive_nodes: 1` everywhere, `failed to join: connection refused`, then `unexpected node` probe storms. T: make SWIM work across Docker + hosts. A: traced three compounding defects (seed ports, localhost bind, UDP NAT rewrite) from memberlist debug logs; derived seeds by convention, bound 0.0.0.0, opened UDP, advertised public IPs, moved to host networking. R: 3/3 converged, proven in container logs.
4. **Restart-wiped primaries shadowed replicas (3 keys 404 cluster-wide).** S: rolling deploy = data unavailable despite RF=2. T: restore availability without redesign. A: black-box proof first (20 keys → kill → restart → 17/20), then primary-miss fallback via non-forwarding `/replica/get`, then async heal-back. R: 20/20 + `entry_count` 0→3 live; unit-tested both halves.
5. **Quorum that couldn't reach quorum.** S: adding majority enforcement exposed the query set missing the primary for non-primary entry nodes — majority unreachable *by construction*. T: honor the documented contract. A: member set = primary + replicas; regression test fails-without/passes-with (stash-verified). R: minority→503, majority→200, absent→404, existing quorum tests unmodified and green.
6. **Parallel-session collision.** S: discovered a second agent's commits (website, retry rework) swept up my uncommitted work mid-audit. T: reconcile without losing or duplicating anything. A: diff archaeology (reflog, per-commit stats, hunk-by-hunk review), kept the good, tested the union, committed cleanly on top. R: zero lost work, green pipelines throughout. *Line: "I treat surprise history the way I treat surprise logs — read everything before touching anything."*
7. **Windows TIME_WAIT suite flakes.** S: full suite failed with ephemeral-port exhaustion (15k+ TIME_WAIT). T: distinguish regression from environment. A: measured socket table, waited for drain, green on re-run; documented spacing rule instead of "fixing" tests. *Line: "A red test you can't explain is data, not a verdict."*

*Lilly trait proved: reasoned debugging under ambiguity + calm incident handling + honest communication.*

---

## 7. Mentality and methodology

**Say it simple:** "Prove it, touch little, write it down. Never guess when a log can tell you. Never break other people's stuff to fix your own."

**Interview-ready:** "Evidence before synthesis (CloudTrail, container logs, black-box repros before code changes); blast-radius discipline (additive-only AWS changes, `shdc-` namespacing, shared roles untouched); 95%-or-ask gate on every mutation; one fix + one regression test per finding; docs edited surgically in the same commit as the behavior; pipelines as the definition of done, not local green. When uncertain, I time-box investigation, then escalate with options and a recommendation — exactly as I did on the tfstate backend and quorum semantics calls."

---

## 8. Learnings

1. **Identity namespaces must agree everywhere** — ring IDs, gossip names, liveness keys: one mismatch silently poisons routing, and nothing errors loudly. Now a checklist item on every distributed design I touch.
2. **NAT is a gossip killer** — UDP source-rewriting middleboxes defeat address-matched protocols; host networking or explicit advertisement, verified from both ends.
3. **Docs are load-bearing** — three separate outages traced to docs/code drift (env vars, quorum contract, compose flags). Sync-in-same-commit is now a personal rule.
4. **Display artifacts lie** — two separate "corruption" scares were console-decoding illusions; I verify bytes before "fixing" anything. Same instinct as not trusting a dashboard without checking the API beneath it.
5. **Availability is a read path property** — RF=2 means nothing if the primary answers 404 for data sitting on a replica. Redundancy must be *consulted*, not just stored.

---

## 9. How this aligns with Lilly (JD mapping)

- **"Investigate, learn, analyze, design, prototype, implement/deploy"** → that is literally the commit history: CloudTrail forensics → prototype fixes → Terraform/IAM → pipeline → live verification.
- **"Vivid learner, experiments without fear of failure"** → adopted memberlist, host networking, quorum hardening, and a from-scratch dashboard in unfamiliar territory (raw SVG dataviz, SWIM internals), each behind a test or a screenshot.
- **"CS fundamentals + SDLC"** → hashing math with measurements, CAP stated per decision, full lifecycle owned solo: design → code → test → CI → deploy → monitor → docs.
- **"Any technology across the spectrum"** → Go backend, vanilla-JS frontend with real taste constraints, AWS (IAM/ECR/Lightsail/Terraform), Docker, DNS, GitHub Actions OIDC, Prometheus-shaped thinking via JSON telemetry.
- **"Reasoning, detail, critical thinking"** → the war stories above; the 400/503/quorum distinctions; refusing to ship guesses.
- **"Interpersonal, dispersed teams"** → the parallel-session reconcile: reviewed a stranger-bot's 2,500-line diff hunk-by-hunk, kept the good, shipped the union green — async collaboration under ambiguity.
- **"Assemble, share, apply learnings"** → DECISIONS.md (15+ entries with numbers), this guide, docs-synced-per-commit habit.
- **"Reliable self-starter, can-do"** → greenfield-to-production with zero hand-holding: infra, pipeline, domain, DNS plan, live cluster.
- **"Think differently, prototype, productize"** → the same-origin dashboard (rejected the obvious Pages answer for structural reasons) and read-healing (lazy repair instead of bootstrap machinery).
- **"Team spirit, diverse teams"** → shared-AWS discipline (never touched others' roles), additive-only changes, review-then-merge posture throughout.

---

## 10. What separates me from peers

Most candidates present a CRUD app with auth. I present: a distributed system with *measured* claims (1/N movement, 154µs drift, 2s detection), production AWS forensics (CloudTrail principal analysis), failure-mode thinking (restart-emptiness, NAT-vs-gossip, minority reads), a no-auth threat model stated honestly, design taste under constraints (anti-slop system, contrast-checked, reduced-motion), and documentation a stranger could operate from. Peers show features; I show guarantees, their proofs, and their prices.

---

## 11. Numbers cheat-sheet (memorize these)

- 3 nodes · RF 2 · 150 vnodes · ap-south-1 · 3×$5/mo Nano
- Key movement on join ~1/N (~33%) vs ~75% modulo; vnode stddev 1877.8 → 9.0
- Failure detection ~2s; failover fast-path immediate on known-dead
- TTL drift ~154µs (processing-time only); rebalance ~3ms (test scale)
- Retries every 5s per failed (key, replica); pending entries age out at 24h
- Live distribution model: 3187/3259/3554 per 10k keys (σ 4.8%)
- Suite: 11 packages green; pipeline ~6–7 min end-to-end; smoke: 50 writes, 100ms avg-warn threshold
- OIDC IDs: owner 141447050, repo 1357349698 (know what they are, not by heart)

---

## 12. Technical grilling Q&A bank

**Consistent hashing:** *Why not modulo?* — Modulo rehashes ~(N-1)/N keys on membership change (measured ~75% 3→4); the ring only reassigns the changed arc (~1/N, measured). *Hot keys?* — Ring balances key *counts*, not popularity; Zipfian hotspots need caching tiers or key-splitting — stated limitation, not solved here. *Vnode count tradeoff?* — More vnodes = smoother distribution + bigger sorted point list; 150 is the standard sweet spot.

**CAP/PACELC:** — RF=2 async: consistent when healthy (single writer path), available under one failure, partition → primary-miss fallback prefers availability; quorum mode trades availability for consistency explicitly (503s). *Split-brain?* — No fencing; divergent writes converge last-writer-per-version, documented as eventual.

**SWIM:** — Random probes + indirect ping-reqs + suspicion before declaration; ~2s detection here. *Why UDP?* — fire-and-forget probes at scale; TCP fallback exists. *False positives?* — slow nodes get suspected; timeouts tuned, and suspect ≠ removal (ring never auto-mutates — deliberate).

**Quorum math:** — RF=2 → need 2/2 (strict!); RF=3 → 2/3 survives one loss. *Why strictly enforce?* — answering from a minority can outvote fresh nodes you can't reach; a 503 is honest, a stale 200 is a lie. *Version ties?* — first-writer (local) wins ties; versions are primary-assigned, comparable within a key's write path.

**Concurrency:** — Store is RWMutex-guarded with lock-upgrade discipline on expiry; LRU touch uses pointer-identity recheck; `/metrics` map read holds the mutex (found by review, fixed). Race detector blocked by Windows toolchain — stated, not hidden.

**Scale/interview traps:** *100 nodes?* — Full-mesh SWIM chatter grows O(N); memberlist handles hundreds fine, thousands want Lifeguard tuning. *Thundering herd on restart?* — Heals are per-read, no stampede path. *Clock skew?* — Absolute expiries assume roughly-synced clocks; NTP-standard caveat. *Exactly-once replication?* — At-least-once + idempotent sets; deletes replicate async (zombie window documented). *Thundering keys (10MB values)?* — No chunking; memory-cap eviction is the only guard.

**Ops:** *How do you know it's healthy?* — /health per node, alive_count quorum in deploy gates, CRUD + perf smokes, pending_repls in metrics. *Deploy safety?* — Rolling, one node at a time, 30×2s health waits, membership + smoke gates; a red gate stops the rollout. *Secrets?* — OIDC short-lived tokens; node SSH key + IPs in GitHub Secrets; no creds in images.

---

## 13. Behavioral Q&A bank (STAR)

- **Disagreement with a teammate?** → The parallel-session reconcile + the quorum-enforcement call: presented evidence, options, and a recommendation instead of decreeing.
- **A time you failed?** → The `forwardWithBody` empty-body 400s *I* shipped past unit tests; caught by black-box battery, fixed with regression test + stash-verified guard. Failure → harness improvement, not just a patch.
- **Ambiguity?** → "Not authorized to perform sts:AssumeRoleWithWebIdentity" with a fresh permissions fix already in: ignored the obvious answer, read CloudTrail, found the immutable-sub rollout.
- **Tight deadline tradeoff?** → Adopted memberlist instead of hand-rolling SWIM; documented the tradeoff instead of pretending.
- **Teaching/knowledge sharing?** → DECISIONS.md, this guide, per-commit doc sync; black-box scripts others can rerun.
- **Working with difficult constraints?** → Shared AWS account: every fix designed additive-only with `shdc-` isolation; Lightsail's lack of instance profiles birthed the runner-side token design.
- **Proudest detail?** → The 503: choosing an honest error over a confident lie, with the test that pins it.

---

## 14. Non-project CS Q&A bank (they will probe breadth)

- **OS:** processes vs threads; mutex vs semaphore vs RWLock (used RWMutex + upgrade discipline); what a race detector does (happens-before tracking); TIME_WAIT exhaustion (debugged live: 16k sockets, drain-and-rerun proof).
- **Networking:** TCP vs UDP and *why gossip uses both*; DNS A vs CNAME (did both live); HTTP status semantics (why 404 vs 503 vs 502 each mean something precise here); CORS preflight mechanics (implemented the middleware); mixed-content wall (drove the embed architecture); TLS handshake in one paragraph.
- **Data:** indexes (hash ring ≈ distributed index; vnodes ≈ partitions); replication vs sharding vs quorum (lived all three); why absolute timestamps beat relative TTLs across clocks.
- **Security:** least privilege (per-repo role, repo-scoped ECR policy); short-lived credentials (OIDC > static keys); no-auth threat model stated + hardening path (reverse-proxy auth, mTLS between nodes); secrets hygiene (nothing in images/logs).
- **SDLC/Agile/DevOps:** trunk-based small commits, pipeline as definition of done, gates that stop rollouts,-docs-in-same-commit, incident routine (observe → hypothesize → reproduce → fix → regression-test → verify live), backlog discipline (Todos.md as the living backlog).
- **Frontend taste:** defend the register (committed ink + bone, rationed accents, three type roles, no card grids); accessibility (labels, focus, contrast ratios, reduced motion, no-JS copy); why vanilla over a component library for bespoke work (sameness is the failure mode).

---

## 15. Questions to ask them

1. "How does platform/infra thinking show up for product engineers here — would someone in this role ever own a deploy pipeline end to end like this?"
2. "What's the team's incident routine, and how are postmortems shared across teams?"
3. "Where's the current reliability pain — flaky tests, deploy safety, observability gaps?"
4. "How are build-vs-buy calls made here — is there room to argue for boring technology with a written tradeoff?"
5. "What does growth look like technically in year one — depth in one system or breadth across the stack?"

---

## 16. Honest limitations + what I'd do next

Say these before they find them — candor about limits reads as seniority: single DC, HTTP-only (Caddy + TLS upgrade path sketched), in-memory only (snapshot persistence next), static ring membership, no auth (edge-auth design ready), JSON metrics (Prometheus exposition later), no read-repair write coalescing under concurrency storms. Next slice if hired-energy applied here: TLS termination, disk snapshots with replay, then RF=3 + chaos-drill automation in CI.

*Final Lilly line to close any answer: "I work best where learning is the job — this project is what happens when curiosity gets a backlog."*
