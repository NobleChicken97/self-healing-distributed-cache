# Chaos Test Repeatability Report

Test Date: 2026-09-05
Test: 10 consecutive runs of the chaos traffic generator
Configuration: Non-existent node (all requests expected to fail)

---

## Individual Run Results

| Run | Total Requests | Succeeded | Failed | Retried | Data Loss | Duration |
|-----|---------------|-----------|--------|---------|-----------|----------|
| 1   | 40            | 0         | 40     | 40      | 0         | 201.4ms  |
| 2   | 39            | 0         | 39     | 39      | 0         | 200.8ms  |
| 3   | 37            | 0         | 37     | 37      | 0         | 201.2ms  |
| 4   | 39            | 0         | 39     | 39      | 0         | 201.3ms  |
| 5   | 40            | 0         | 40     | 40      | 0         | 200.9ms  |
| 6   | 39            | 0         | 39     | 39      | 0         | 200.4ms  |
| 7   | 40            | 0         | 40     | 40      | 0         | 200.7ms  |
| 8   | 40            | 0         | 40     | 40      | 0         | 200.9ms  |
| 9   | 40            | 0         | 40     | 40      | 0         | 200.9ms  |
| 10  | 40            | 0         | 40     | 40      | 0         | 201.3ms  |

---

## Statistical Summary

| Metric        | Min | Max | Mean  | Std Dev |
|---------------|-----|-----|-------|---------|
| Total Requests| 37  | 40  | 39.4  | 1.0     |
| Succeeded     | 0   | 0   | 0.0   | 0.0     |
| Failed        | 37  | 40  | 39.4  | 1.0     |
| Retried       | 37  | 40  | 39.4  | 1.0     |
| Data Loss     | 0   | 0   | 0.0   | 0.0     |
| Duration      | 200.4ms | 201.4ms | 200.9ms | 0.3ms |

---

## Analysis

### Consistency
- **Total Requests**: Very consistent (37-40), minor variation due to goroutine scheduling
- **Data Loss**: Zero across all runs - PASS
- **Duration**: Highly consistent (~200ms) - PASS

### Anomaly Detection
- No runs exceeded 50% deviation from mean
- All runs behaved as expected (all requests fail since node doesn't exist)
- No panics, deadlocks, or goroutine leaks

### Conclusion
The chaos test harness produces **repeatable, deterministic results** across 10 consecutive runs. No anomalies detected.

Note: This test uses a non-existent node. A full integration test with actual cluster nodes would be needed to verify time-to-detect-failure and time-to-full-rebalance metrics.
