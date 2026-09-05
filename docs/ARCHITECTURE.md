# Architecture Diagram

## System Overview

```
                         ┌─────────────────────────────────────────────┐
                         │              Client Application             │
                         │         (cache-client CLI or HTTP)          │
                         └───────────────────┬─────────────────────────┘
                                             │
                                             │ HTTP Request
                                             │ (any node)
                                             ▼
┌────────────────────────────────────────────────────────────────────────────────┐
│                              Cache Cluster                                     │
│                                                                                │
│  ┌──────────────────────────────────────────────────────────────────────────┐  │
│  │                     Server-Side Routing Layer                           │  │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────────────┐ │  │
│  │  │   Hash Ring     │  │  Cluster Health │  │   Failure Detector      │ │  │
│  │  │  (consistent    │  │   (SWIM gossip  │  │   (memberlist)          │ │  │
│  │  │   hashing)      │  │    state)       │  │                         │ │  │
│  │  └────────┬────────┘  └────────┬────────┘  └─────────────────────────┘ │  │
│  │           │                    │                                         │  │
│  └───────────┼────────────────────┼─────────────────────────────────────────┘  │
│              │                    │                                            │
│              │    ┌───────────────┼───────────────┐                           │
│              │    │               │               │                           │
│              ▼    ▼               ▼               ▼                           │
│  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐  ┌───────────────┐  │
│  │    Node A     │  │    Node B     │  │    Node C     │  │    Node N     │  │
│  │  ┌─────────┐  │  │  ┌─────────┐  │  │  ┌─────────┐  │  │  ┌─────────┐  │  │
│  │  │ Primary │  │  │  │ Primary │  │  │  │ Primary │  │  │  │ Primary │  │  │
│  │  │  Store  │  │  │  │  Store  │  │  │  │  Store  │  │  │  │  Store  │  │  │
│  │  └────┬────┘  │  │  └────┬────┘  │  │  └────┬────┘  │  │  └────┬────┘  │  │
│  │       │       │  │       │       │  │       │       │  │       │       │  │
│  │  ┌────┴────┐  │  │  ┌────┴────┐  │  │  ┌────┴────┐  │  │  ┌────┴────┐  │  │
│  │  │ Replica │  │  │  │ Replica │  │  │  │ Replica │  │  │  │ Replica │  │  │
│  │  │  Store  │  │  │  │  Store  │  │  │  │  Store  │  │  │  │  Store  │  │  │
│  │  └─────────┘  │  │  └─────────┘  │  │  └─────────┘  │  │  └─────────┘  │  │
│  └───────┬───────┘  └───────┬───────┘  └───────┬───────┘  └───────┬───────┘  │
│          │                  │                  │                  │          │
│          └──────────────────┴──────────────────┴──────────────────┘          │
│                                    │                                         │
│                              Gossip Mesh                                     │
│                           (SWIM Protocol)                                    │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

## Request Routing Flow

```
Client Request (GET key-x)
        │
        ▼
┌───────────────────┐
│   Receive on      │
│   Any Node        │
└─────────┬─────────┘
          │
          ▼
┌───────────────────┐     ┌─────────────────┐
│  Hash Ring        │────►│  Owner: Node B  │
│  Lookup(key-x)    │     └─────────────────┘
└─────────┬─────────┘
          │
          ▼
     ┌────────────┐
     │ This node  │
     │ is owner?  │
     └─────┬──────┘
           │
     ┌─────┴─────┐
     │           │
    YES          NO
     │           │
     ▼           ▼
┌─────────┐  ┌──────────────────┐
│ Serve   │  │ Forward to Node B│
│ Locally │  │ (transparent)    │
└─────────┘  └────────┬─────────┘
                      │
                      ▼
               ┌─────────────┐
               │ Node B is   │
               │ alive?      │
               └──────┬──────┘
                      │
                ┌─────┴─────┐
                │           │
               YES          NO
                │           │
                ▼           ▼
         ┌──────────┐  ┌─────────────────┐
         │ Node B   │  │ Route to Replica│
         │ Serves   │  │ (Node C or A)   │
         └──────────┘  └─────────────────┘
```

## Replication Flow

```
Client SET key-x = value
        │
        ▼
┌───────────────────┐
│   Primary Node    │
│   (Node A)        │
└─────────┬─────────┘
          │
          ├──────────────────────────────────┐
          │                                  │
          ▼                                  ▼
┌─────────────────┐              ┌─────────────────────┐
│ Write to Local  │              │ Async Replication   │
│ Store           │              │ (background goroutine)│
│                 │              └──────────┬──────────┘
│ expiresAt =     │                         │
│ now + ttl       │                         ▼
└─────────────────┘              ┌─────────────────────┐
                                 │ POST /replica/set   │
                                 │ to Replica Node(s)  │
                                 │                     │
                                 │ Includes:           │
                                 │ - key               │
                                 │ - value             │
                                 │ - expires_at_ms     │
                                 │   (absolute time)   │
                                 └─────────────────────┘
```

## Rebalance Flow (Node Join)

```
New Node D Joins
        │
        ▼
┌───────────────────┐
│ Ring Updated      │
│ (A, B, C, D)      │
└─────────┬─────────┘
          │
          ▼
┌───────────────────────────────────────────────────────────┐
│ Each node computes which keys moved:                      │
│                                                           │
│ For each local key:                                       │
│   new_owner = ring.Lookup(key)                            │
│   if new_owner != this_node:                              │
│     → Key needs to migrate                               │
└───────────────────────────────────────────────────────────┘
          │
          ▼
┌───────────────────────────────────────────────────────────┐
│ Migration Protocol (Pull-Before-Drop):                    │
│                                                           │
│ 1. Old Owner (A) has key-x                               │
│ 2. New Owner (D) pulls from A:                           │
│    POST /rebalance/pull?key=key-x                        │
│    → Returns: {key, value}                               │
│ 3. New Owner (D) stores locally                          │
│ 4. Old Owner (A) deletes key-x                           │
│                                                           │
│ During steps 1-3: Both A and D have key-x                │
│ → No window where key is absent                          │
└───────────────────────────────────────────────────────────┘
```

## Failure Detection Flow

```
┌─────────────────────────────────────────────────────────────┐
│                    SWIM Gossip Protocol                      │
│                                                             │
│  ┌─────────┐    ping/ack     ┌─────────┐    ping/ack      ┌─────────┐
│  │ Node A  │◄───────────────►│ Node B  │◄───────────────►│ Node C  │
│  └─────────┘                 └─────────┘                 └─────────┘
│       │                           │                           │
│       │◄──────────────────────────┼──────────────────────────►│
│       │         gossip dissemination                         │
│       │         (suspected-failure state)                    │
│                                                             │
│  Failure Detection:                                         │
│  1. Node A pings Node B                                     │
│  2. No ack within timeout → B is suspected                 │
│  3. Gossip "B suspected" to other nodes                    │
│  4. If B doesn't respond → B is marked FAILED              │
│  5. All nodes update their health state                     │
│  6. OnTopologyChange callback fires → triggers rebalance   │
│                                                             │
│  Detection time: ~2 seconds                                 │
└─────────────────────────────────────────────────────────────┘
```

## Automatic Rebalance Trigger Flow

```
┌─────────────────────────────────────────────────────────────┐
│                 Automatic Rebalance Flow                     │
│                                                             │
│  1. SWIM detects node failure (~2 seconds)                  │
│     │                                                       │
│     ▼                                                       │
│  2. EventDelegate.NotifyLeave() fires                       │
│     │                                                       │
│     ▼                                                       │
│  3. onChange callback updates aliveNodes                    │
│     │                                                       │
│     ▼                                                       │
│  4. OnTopologyChange callback fires                        │
│     │                                                       │
│     ▼                                                       │
│  5. server.TriggerRebalance() called                        │
│     │                                                       │
│     ▼                                                       │
│  6. Rebalance runs in background goroutine                  │
│     - Computes which keys need to move                      │
│     - Pulls keys to new owners                              │
│     - Old owners delete after confirmation                  │
│                                                             │
│  Result: Zero-downtime key redistribution                   │
└─────────────────────────────────────────────────────────────┘
```

## Replication Retry Flow

```
┌─────────────────────────────────────────────────────────────┐
│                  Replication Retry Flow                      │
│                                                             │
│  1. Client SET → Primary writes locally                     │
│     │                                                       │
│     ▼                                                       │
│  2. Async replication to replicas                           │
│     │                                                       │
│     ├── Success → Done                                      │
│     │                                                       │
│     └── Failure → Track in pendingRepls map                 │
│              │                                              │
│              ▼                                              │
│  3. Background retryLoop (every 5 seconds)                  │
│     │                                                       │
│     ▼                                                       │
│  4. Retry failed replications                               │
│     │                                                       │
│     ├── Success → Remove from pendingRepls                  │
│     │                                                       │
│     └── Failure → Keep in pendingRepls (max 3 retries)      │
│                                                             │
│  Graceful Shutdown: replWg.Wait() ensures all in-flight    │
│  replications complete before process exits                 │
└─────────────────────────────────────────────────────────────┘
```

## Component Interaction

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           Server Package                                │
│                                                                         │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐   │
│  │ handleSet   │  │ handleGet   │  │ handleDelete│  │ handleRepl  │   │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘   │
│         │                │                │                │           │
│         └────────────────┴────────────────┴────────────────┘           │
│                                  │                                      │
│                    ┌─────────────┴─────────────┐                       │
│                    │                           │                       │
│                    ▼                           ▼                       │
│           ┌──────────────┐           ┌──────────────────┐             │
│           │  ownsLocal() │           │  forward() /     │             │
│           │  (ring lookup)│          │  forwardWithFallback│           │
│           └──────────────┘           └──────────────────┘             │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                     Dependencies                                │   │
│  │  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────────────┐   │   │
│  │  │  ring   │  │ store   │  │ cluster │  │ rebalance       │   │   │
│  │  │ package │  │ package │  │ package │  │ package         │   │   │
│  │  └─────────┘  └─────────┘  └─────────┘  └─────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────┘
```
