# Coverage Gap Analysis

Generated: 2026-09-04
Current coverage: server 42.2%, rebalance 55.2%

---

## Server Package (internal/server)

| Function/Branch | Classification | Reasoning |
|----------------|----------------|-----------|
| `TriggerRebalance()` | (b) Real gap | Async rebalance trigger never tested; core to Phase 5 |
| `handleRebalance()` | (b) Real gap | HTTP endpoint for rebalance never tested |
| `handleRebalanceStatus()` | (a) Low risk | Trivial status getter, defensive |
| `forwardQuorumSet()` | (b) Real gap | Quorum forwarding path never tested |
| `handleReplicaQuorumSet()` | (b) Real gap | Quorum replica handler never tested |
| `forward()` - ring lookup fail | (a) Low risk | Defensive branch, ring always has nodes |
| `forwardWithBody()` - ring lookup fail | (a) Low risk | Defensive branch, ring always has nodes |
| `forwardToReplica()` - all replicas fail | (b) Real gap | Error handling path in failover never tested |
| `handleReplicaWriteWithBody()` - delete | (b) Real gap | Delete path when acting as replica never tested |
| `handleReplicaWriteWithBody()` - invalid JSON | (b) Real gap | Error path never tested |
| `readRequestBody()` - error path | (a) Low risk | IO error unlikely in tests |
| `handleQuorumSet()` - quorum fail | (b) Real gap | Failure when majority unreachable never tested |
| `handleQuorumGet()` - no responses | (b) Real gap | Not-found path never tested |
| `handleQuorumGet()` - version conflict | (b) Real gap | Version resolution logic never tested |
| `handleReplicaGet()` - not found | (a) Low risk | Standard error path |
| `splitByAlive()` | (a) Low risk | Simple utility, indirectly tested |

---

## Rebalance Package (internal/rebalance)

| Function/Branch | Classification | Reasoning |
|----------------|----------------|-----------|
| `ExecuteMigration()` - pull failure | (b) Real gap | Network error during pull never tested |
| `ExecuteMigration()` - push failure | (b) Real gap | Network error during push never tested |
| `ExecuteMigration()` - invalid JSON | (b) Real gap | Decode error path never tested |
| `ExecuteMigration()` - non-200 status | (b) Real gap | Error status from server never tested |
| `Rebalance()` - concurrent call | (b) Real gap | inProgress guard never tested |
| `ComputeKeyMovements()` | (a) Low risk | Simple computation, indirectly tested |
| `Migrations()` | (a) Low risk | Simple getter |
| `LastResult()` | (a) Low risk | Simple getter |
| `IsInProgress()` | (a) Low risk | Simple getter |

---

## Summary of Real Gaps (Classification b)

### Server (8 real gaps)
1. TriggerRebalance async behavior
2. handleRebalance HTTP endpoint
3. forwardQuorumSet path
4. handleReplicaQuorumSet path
5. forwardToReplica all-replicas-fail error path
6. handleReplicaWriteWithBody delete and error paths
7. handleQuorumSet quorum failure path
8. handleQuorumGet version conflict resolution

### Rebalance (5 real gaps)
1. ExecuteMigration pull failure
2. ExecuteMigration push failure
3. ExecuteMigration invalid JSON
4. ExecuteMigration non-200 status
5. Rebalance concurrent call guard

---

## New Test Results

### Server Package Tests (audit/server/server_extra_test.go)

| Test | Result |
|------|--------|
| TestTriggerRebalance | PASS |
| TestServerCreation | PASS |
| TestConcurrentServerAccess | PASS |
| TestServerHandlerEndpoints | PASS |
| TestQuorumSetEndpoint | PASS |
| TestQuorumGetEndpoint | PASS |
| TestRebalanceEndpoint | PASS |
| TestRebalanceStatusEndpoint | PASS |
| TestClusterInfoEndpoint | PASS |

### Rebalance Package Tests (audit/rebalance/rebalance_extra_test.go)

| Test | Result |
|------|--------|
| TestExecuteMigrationPullFailure | PASS |
| TestExecuteMigrationPushFailure | PASS |
| TestExecuteMigrationInvalidJSON | PASS |
| TestRebalanceConcurrentCall | PASS |
| TestComputeKeyMovements | PASS |
| TestMigrationStatusString | PASS |
| TestRebalanceLastResult | PASS |
| TestRebalanceIsInProgress | PASS |
| TestMigrationsInitiallyEmpty | PASS |
| TestNewWithNilLogger | PASS |
| TestSetTransport | PASS |
| TestRebalanceDurationIsRecorded | PASS |
| TestRebalanceWithNoKeys | PASS |
| TestConcurrentRebalanceAccess | PASS |

All 23 new tests pass against the current code.

---

## Phase 5 Specific Gaps (per Plan.md)

Plan.md explicitly called out these as separate code paths that must both be tested:

1. **Brand-new node joining** - triggers rebalancing with continuous traffic and zero "key not found" errors
   - Status: Existing test `TestRebalanceOnNodeJoin` covers basic case but NOT continuous traffic during rebalance

2. **Node recovering after being marked failed** - triggers rebalancing again with same zero-error guarantee
   - Status: NOT tested at all - this is a separate code path that is completely untested

These are both classified as (b) real gaps.
