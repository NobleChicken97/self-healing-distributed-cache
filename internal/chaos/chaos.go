// Package chaos provides a chaos test harness for verifying self-healing behavior.
//
// Failure criteria:
//   - A request "fails" if it times out (>500ms) or returns a server error (5xx)
//   - A request "retries" if the first attempt fails but a retry succeeds
//   - A request "succeeds" if it returns the expected result within the timeout
//   - Data integrity failure: a GET returns 404 for a key that was previously SET successfully
package chaos

import (
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics tracks the results of a chaos test run.
type Metrics struct {
	mu              sync.Mutex
	TotalRequests   int64
	Succeeded       int64
	Failed          int64
	Retried         int64
	DataLoss        int64 // Keys that were SET but later GET returned 404
	FailedRequests  []FailedRequest
	StartTime       time.Time
	EndTime         time.Time
}

// FailedRequest records details of a failed request for debugging.
type FailedRequest struct {
	Timestamp time.Time
	Operation string // "set" or "get"
	Key       string
	Error     string
	Duration  time.Duration
}

// TrafficGenerator continuously issues Set/Get requests against a cluster.
type TrafficGenerator struct {
	nodeAddrs     []string
	keys          []string
	interval      time.Duration
	timeout       time.Duration
	maxRetries    int
	client        *http.Client
	metrics       *Metrics
	stop          chan struct{}
	wg            sync.WaitGroup
	logger        *log.Logger
}

// TrafficConfig configures the traffic generator.
type TrafficConfig struct {
	NodeAddrs  []string
	NumKeys    int
	Interval   time.Duration
	Timeout    time.Duration
	MaxRetries int
	Logger     *log.Logger
}

// NewTrafficGenerator creates a traffic generator for the given nodes.
func NewTrafficGenerator(cfg TrafficConfig) *TrafficGenerator {
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Millisecond // 100 requests/second per goroutine
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 500 * time.Millisecond
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 1
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}

	// Generate key pool.
	keys := make([]string, cfg.NumKeys)
	for i := 0; i < cfg.NumKeys; i++ {
		keys[i] = fmt.Sprintf("chaos-key-%d", i)
	}

	return &TrafficGenerator{
		nodeAddrs:  cfg.NodeAddrs,
		keys:       keys,
		interval:   cfg.Interval,
		timeout:    cfg.Timeout,
		maxRetries: cfg.MaxRetries,
		client: &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     30 * time.Second,
			},
		},
		metrics: &Metrics{
			FailedRequests: make([]FailedRequest, 0),
		},
		stop:   make(chan struct{}),
		logger: cfg.Logger,
	}
}

// Start begins generating traffic. Returns immediately; use Stop to halt.
func (tg *TrafficGenerator) Start() {
	tg.metrics.StartTime = time.Now()
	tg.wg.Add(1)
	go tg.generate()
}

// Stop halts traffic generation and waits for completion.
func (tg *TrafficGenerator) Stop() {
	close(tg.stop)
	tg.wg.Wait()
	tg.metrics.EndTime = time.Now()
}

// GetMetrics returns a copy of the current metrics (safe for concurrent access).
func (tg *TrafficGenerator) GetMetrics() Metrics {
	tg.metrics.mu.Lock()
	defer tg.metrics.mu.Unlock()
	// Return a deep copy to avoid deadlock when Report() is called on the result.
	metricsCopy := *tg.metrics
	metricsCopy.FailedRequests = make([]FailedRequest, len(tg.metrics.FailedRequests))
	copy(metricsCopy.FailedRequests, tg.metrics.FailedRequests)
	return metricsCopy
}

// GetTotalRequests returns the total number of requests attempted (atomic).
func (tg *TrafficGenerator) GetTotalRequests() int64 {
	return atomic.LoadInt64(&tg.metrics.TotalRequests)
}

// GetSucceeded returns the number of successful requests (atomic).
func (tg *TrafficGenerator) GetSucceeded() int64 {
	return atomic.LoadInt64(&tg.metrics.Succeeded)
}

// GetFailed returns the number of failed requests (atomic).
func (tg *TrafficGenerator) GetFailed() int64 {
	return atomic.LoadInt64(&tg.metrics.Failed)
}

// GetRetried returns the number of retried requests (atomic).
func (tg *TrafficGenerator) GetRetried() int64 {
	return atomic.LoadInt64(&tg.metrics.Retried)
}

// GetDataLoss returns the number of data loss incidents (atomic).
func (tg *TrafficGenerator) GetDataLoss() int64 {
	return atomic.LoadInt64(&tg.metrics.DataLoss)
}

func (tg *TrafficGenerator) generate() {
	defer tg.wg.Done()
	ticker := time.NewTicker(tg.interval)
	defer ticker.Stop()

	for {
		select {
		case <-tg.stop:
			return
		case <-ticker.C:
			// Randomly choose SET or GET (70% GET, 30% SET for realistic read-heavy workload)
			if rand.Float64() < 0.3 {
				tg.doSet()
			} else {
				tg.doGet()
			}
		}
	}
}

func (tg *TrafficGenerator) doSet() {
	key := tg.keys[rand.Intn(len(tg.keys))]
	value := fmt.Sprintf("value-%d", time.Now().UnixNano())
	nodeAddr := tg.nodeAddrs[rand.Intn(len(tg.nodeAddrs))]

	body := fmt.Sprintf(`{"key":"%s","value":"%s","ttl_ms":0}`, key, value)
	url := fmt.Sprintf("http://%s/set", nodeAddr)

	atomic.AddInt64(&tg.metrics.TotalRequests, 1)

	start := time.Now()
	success := false
	var lastErr string

	for attempt := 0; attempt <= tg.maxRetries; attempt++ {
		if attempt > 0 {
			atomic.AddInt64(&tg.metrics.Retried, 1)
		}

		resp, err := tg.client.Post(url, "application/json", stringReader(body))
		if err != nil {
			lastErr = err.Error()
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			success = true
			break
		}
		lastErr = fmt.Sprintf("status=%d body=%s", resp.StatusCode, string(respBody))
	}

	duration := time.Since(start)

	if success {
		atomic.AddInt64(&tg.metrics.Succeeded, 1)
	} else {
		atomic.AddInt64(&tg.metrics.Failed, 1)
		tg.recordFailedRequest("set", key, lastErr, duration)
	}
}

func (tg *TrafficGenerator) doGet() {
	key := tg.keys[rand.Intn(len(tg.keys))]
	nodeAddr := tg.nodeAddrs[rand.Intn(len(tg.nodeAddrs))]

	url := fmt.Sprintf("http://%s/get?key=%s", nodeAddr, url.QueryEscape(key))

	atomic.AddInt64(&tg.metrics.TotalRequests, 1)

	start := time.Now()
	success := false
	var lastErr string

	for attempt := 0; attempt <= tg.maxRetries; attempt++ {
		if attempt > 0 {
			atomic.AddInt64(&tg.metrics.Retried, 1)
		}

		resp, err := tg.client.Get(url)
		if err != nil {
			lastErr = err.Error()
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			success = true
			break
		}
		if resp.StatusCode == http.StatusNotFound {
			// Data integrity issue: key was SET but GET returns 404.
			// This counts as data loss.
			atomic.AddInt64(&tg.metrics.DataLoss, 1)
			lastErr = "data loss: key not found"
			break
		}
		lastErr = fmt.Sprintf("status=%d body=%s", resp.StatusCode, string(respBody))
	}

	duration := time.Since(start)

	if success {
		atomic.AddInt64(&tg.metrics.Succeeded, 1)
	} else {
		atomic.AddInt64(&tg.metrics.Failed, 1)
		tg.recordFailedRequest("get", key, lastErr, duration)
	}
}

func (tg *TrafficGenerator) recordFailedRequest(op, key, err string, d time.Duration) {
	tg.metrics.mu.Lock()
	defer tg.metrics.mu.Unlock()
	tg.metrics.FailedRequests = append(tg.metrics.FailedRequests, FailedRequest{
		Timestamp: time.Now(),
		Operation: op,
		Key:       key,
		Error:     err,
		Duration:  d,
	})
}

func stringReader(s string) *stringReaderType {
	return &stringReaderType{s: s}
}

type stringReaderType struct {
	s string
	i int
}

func (r *stringReaderType) Read(p []byte) (n int, err error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n = copy(p, r.s[r.i:])
	r.i += n
	return
}

// Report generates a human-readable summary of the test results.
func (m *Metrics) Report() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	duration := m.EndTime.Sub(m.StartTime)
	if duration <= 0 {
		duration = time.Since(m.StartTime)
	}

	var sb strings.Builder
	sb.WriteString("=== Chaos Test Report ===\n")
	sb.WriteString(fmt.Sprintf("Duration:        %v\n", duration))
	sb.WriteString(fmt.Sprintf("Total Requests:  %d\n", m.TotalRequests))
	sb.WriteString(fmt.Sprintf("Succeeded:       %d (%.1f%%)\n", m.Succeeded, percentage(m.Succeeded, m.TotalRequests)))
	sb.WriteString(fmt.Sprintf("Failed:          %d (%.1f%%)\n", m.Failed, percentage(m.Failed, m.TotalRequests)))
	sb.WriteString(fmt.Sprintf("Retried:         %d\n", m.Retried))
	sb.WriteString(fmt.Sprintf("Data Loss:       %d\n", m.DataLoss))
	if m.TotalRequests > 0 {
		sb.WriteString(fmt.Sprintf("Throughput:      %.1f req/sec\n", float64(m.TotalRequests)/duration.Seconds()))
	}
	sb.WriteString("\n")

	if len(m.FailedRequests) > 0 {
		sb.WriteString("Failed Requests (first 10):\n")
		limit := 10
		if len(m.FailedRequests) < limit {
			limit = len(m.FailedRequests)
		}
		for i := 0; i < limit; i++ {
			fr := m.FailedRequests[i]
			sb.WriteString(fmt.Sprintf("  [%s] %s %s: %s (%v)\n", fr.Timestamp.Format("15:04:05.000"), fr.Operation, fr.Key, fr.Error, fr.Duration))
		}
	}

	return sb.String()
}

func percentage(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}
