package chaos

import (
	"testing"
	"time"
)

func TestTrafficGeneratorBasics(t *testing.T) {
	// This test verifies the traffic generator can be created and started/stopped.
	// It doesn't actually connect to any nodes (they don't exist), so all requests
	// will fail, but we verify the generator works correctly.

	tg := NewTrafficGenerator(TrafficConfig{
		NodeAddrs:  []string{"127.0.0.1:19999"}, // Non-existent node
		NumKeys:    10,
		Interval:   5 * time.Millisecond,
		Timeout:    50 * time.Millisecond,
		MaxRetries: 0,
	})

	tg.Start()
	time.Sleep(200 * time.Millisecond)
	tg.Stop()

	// Access metrics directly to avoid mutex issues.
	total := tg.GetTotalRequests()
	succeeded := tg.GetSucceeded()
	failed := tg.GetFailed()

	if total == 0 {
		t.Fatal("expected some requests to be attempted")
	}

	t.Logf("Metrics: total=%d succeeded=%d failed=%d", total, succeeded, failed)
}

func TestMetricsReport(t *testing.T) {
	m := &Metrics{
		TotalRequests: 1000,
		Succeeded:     950,
		Failed:        50,
		Retried:       10,
		DataLoss:      0,
		StartTime:     time.Now().Add(-10 * time.Second),
		EndTime:       time.Now(),
	}

	report := m.Report()
	if report == "" {
		t.Fatal("expected non-empty report")
	}
	t.Logf("Report:\n%s", report)
}

func TestPercentageCalculation(t *testing.T) {
	tests := []struct {
		part    int64
		total   int64
		wantMin float64
		wantMax float64
	}{
		{50, 100, 49.9, 50.1},
		{0, 100, 0, 0},
		{100, 100, 99.9, 100.1},
		{0, 0, 0, 0},
	}

	for _, tt := range tests {
		got := percentage(tt.part, tt.total)
		if got < tt.wantMin || got > tt.wantMax {
			t.Errorf("percentage(%d, %d) = %f, want between %f and %f",
				tt.part, tt.total, got, tt.wantMin, tt.wantMax)
		}
	}
}
