package audit

import (
	"fmt"
	"math"
	"testing"
	"time"

	"selfhealingcache/internal/chaos"
)

// TestChaosRepeatability runs the chaos test harness 10 times
// and reports results for each run individually.
func TestChaosRepeatability(t *testing.T) {
	const numRuns = 10

	type runResult struct {
		Run           int
		TotalRequests int64
		Succeeded     int64
		Failed        int64
		Retried       int64
		DataLoss      int64
		Duration      time.Duration
	}

	results := make([]runResult, numRuns)

	for i := 0; i < numRuns; i++ {
		start := time.Now()

		// Create traffic generator with a non-existent node (all requests will fail)
		tg := chaos.NewTrafficGenerator(chaos.TrafficConfig{
			NodeAddrs:  []string{"127.0.0.1:19999"},
			NumKeys:    10,
			Interval:   5 * time.Millisecond,
			Timeout:    50 * time.Millisecond,
			MaxRetries: 1,
		})

		tg.Start()
		time.Sleep(200 * time.Millisecond)
		tg.Stop()

		duration := time.Since(start)
		metrics := tg.GetMetrics()

		results[i] = runResult{
			Run:           i + 1,
			TotalRequests: metrics.TotalRequests,
			Succeeded:     metrics.Succeeded,
			Failed:        metrics.Failed,
			Retried:       metrics.Retried,
			DataLoss:      metrics.DataLoss,
			Duration:      duration,
		}

		t.Logf("Run %d: total=%d succeeded=%d failed=%d retried=%d data_loss=%d duration=%v",
			i+1, metrics.TotalRequests, metrics.Succeeded, metrics.Failed,
			metrics.Retried, metrics.DataLoss, duration)
	}

	// Calculate statistics
	var minTotal, maxTotal, sumTotal int64 = math.MaxInt64, 0, 0
	var minSucceeded, maxSucceeded, sumSucceeded int64 = math.MaxInt64, 0, 0
	var minFailed, maxFailed, sumFailed int64 = math.MaxInt64, 0, 0
	var minRetried, maxRetried, sumRetried int64 = math.MaxInt64, 0, 0
	var minDataLoss, maxDataLoss, sumDataLoss int64 = math.MaxInt64, 0, 0

	for _, r := range results {
		// Total requests
		if r.TotalRequests < minTotal {
			minTotal = r.TotalRequests
		}
		if r.TotalRequests > maxTotal {
			maxTotal = r.TotalRequests
		}
		sumTotal += r.TotalRequests

		// Succeeded
		if r.Succeeded < minSucceeded {
			minSucceeded = r.Succeeded
		}
		if r.Succeeded > maxSucceeded {
			maxSucceeded = r.Succeeded
		}
		sumSucceeded += r.Succeeded

		// Failed
		if r.Failed < minFailed {
			minFailed = r.Failed
		}
		if r.Failed > maxFailed {
			maxFailed = r.Failed
		}
		sumFailed += r.Failed

		// Retried
		if r.Retried < minRetried {
			minRetried = r.Retried
		}
		if r.Retried > maxRetried {
			maxRetried = r.Retried
		}
		sumRetried += r.Retried

		// Data loss
		if r.DataLoss < minDataLoss {
			minDataLoss = r.DataLoss
		}
		if r.DataLoss > maxDataLoss {
			maxDataLoss = r.DataLoss
		}
		sumDataLoss += r.DataLoss
	}

	// Check for anomalies
	// All requests should fail since the node doesn't exist
	for _, r := range results {
		if r.Succeeded > 0 {
			t.Errorf("Run %d: expected 0 succeeded (node doesn't exist), got %d", r.Run, r.Succeeded)
		}
		if r.DataLoss > 0 {
			t.Errorf("Run %d: expected 0 data loss, got %d", r.Run, r.DataLoss)
		}
	}

	// Report summary statistics
	_ = fmt.Sprintf("Summary across %d runs:", numRuns)
	t.Logf("Total Requests: min=%d max=%d avg=%.1f", minTotal, maxTotal, float64(sumTotal)/float64(numRuns))
	t.Logf("Failed:         min=%d max=%d avg=%.1f", minFailed, maxFailed, float64(sumFailed)/float64(numRuns))
	t.Logf("Retried:        min=%d max=%d avg=%.1f", minRetried, maxRetried, float64(sumRetried)/float64(numRuns))
	t.Logf("Data Loss:      min=%d max=%d avg=%.1f", minDataLoss, maxDataLoss, float64(sumDataLoss)/float64(numRuns))

	// Flag any runs that differ significantly from the mean
	meanTotal := float64(sumTotal) / float64(numRuns)
	for _, r := range results {
		deviation := math.Abs(float64(r.TotalRequests)-meanTotal) / meanTotal
		if deviation > 0.5 { // More than 50% deviation
			t.Logf("WARNING: Run %d has %.1f%% deviation from mean total requests", r.Run, deviation*100)
		}
	}
}
