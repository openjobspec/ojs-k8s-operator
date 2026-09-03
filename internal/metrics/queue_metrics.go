// Package metrics provides OJS queue depth metrics for Kubernetes HPA integration.
//
// It polls OJS backend queue stats and exposes them as custom metrics that
// can be consumed by the Kubernetes Horizontal Pod Autoscaler.
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// QueueMetrics holds the latest queue depth metrics.
type QueueMetrics struct {
	Queue     string    `json:"queue"`
	Pending   int64     `json:"pending"`
	Active    int64     `json:"active"`
	Total     int64     `json:"total"`
	Timestamp time.Time `json:"timestamp"`
}

// Collector polls OJS queue stats and caches the results.
//
// The cache (metrics) is replaced wholesale on every successful Poll rather
// than merged in place: this guarantees that queues which disappear from the
// upstream response (deleted, renamed, or no longer reported) are dropped
// from the cache instead of lingering forever with stale data -- which would
// otherwise leak memory and, if wired to a Prometheus registry, leave behind
// stale time series. All accessors return deep copies of cached entries so
// callers can never mutate collector-owned state.
type Collector struct {
	mu       sync.RWMutex
	ojsURL   string
	apiKey   string
	client   *http.Client
	metrics  map[string]*QueueMetrics
	interval time.Duration
}

// NewCollector creates a queue metrics collector.
func NewCollector(ojsURL string, apiKey string, interval time.Duration) *Collector {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Collector{
		ojsURL:   ojsURL,
		apiKey:   apiKey,
		client:   &http.Client{Timeout: 10 * time.Second},
		metrics:  make(map[string]*QueueMetrics),
		interval: interval,
	}
}

// GetQueueDepth returns the current pending job count for a queue.
func (c *Collector) GetQueueDepth(queue string) (int64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.metrics[queue]
	if !ok {
		return 0, false
	}
	return m.Pending, true
}

// GetAllMetrics returns a deep copy of the metrics for all queues. The
// returned map and its *QueueMetrics values are independent of the
// collector's internal cache, so callers may freely read or even mutate
// them without risk of corrupting collector state or racing with a
// concurrent Poll.
func (c *Collector) GetAllMetrics() map[string]*QueueMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make(map[string]*QueueMetrics, len(c.metrics))
	for k, v := range c.metrics {
		copied := *v
		result[k] = &copied
	}
	return result
}

// queueStat is the wire representation of a single queue's stats in the
// /ojs/v1/queues response.
type queueStat struct {
	Name    string `json:"name"`
	Pending int64  `json:"pending"`
	Active  int64  `json:"active"`
}

// fetchQueueStats performs the HTTP round-trip to the OJS backend's queue
// stats endpoint and decodes the response.
func (c *Collector) fetchQueueStats(ctx context.Context) ([]queueStat, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ojsURL+"/ojs/v1/queues", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("polling queues: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Queues []queueStat `json:"queues"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return result.Queues, nil
}

// Poll fetches the latest queue stats from the OJS backend and atomically
// replaces the cached metrics, so queues no longer reported upstream are
// removed rather than retained indefinitely.
func (c *Collector) Poll(ctx context.Context) error {
	queues, err := c.fetchQueueStats(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	fresh := make(map[string]*QueueMetrics, len(queues))
	for _, q := range queues {
		fresh[q.Name] = &QueueMetrics{
			Queue:     q.Name,
			Pending:   q.Pending,
			Active:    q.Active,
			Total:     q.Pending + q.Active,
			Timestamp: now,
		}
	}

	// Swap the whole cache under the lock. This is both simpler and
	// correctly evicts stale queues (see the Collector doc comment) compared
	// to mutating the existing map key-by-key.
	c.mu.Lock()
	c.metrics = fresh
	c.mu.Unlock()

	return nil
}

// DesiredReplicas calculates the desired replica count based on queue depth.
func DesiredReplicas(queueDepth int64, targetPerWorker int64, min, max int32) int32 {
	if targetPerWorker <= 0 {
		targetPerWorker = 10
	}
	desired := queueDepth / targetPerWorker
	if queueDepth%targetPerWorker > 0 {
		desired++
	}
	if desired < int64(min) {
		desired = int64(min)
	}
	if desired > int64(max) {
		desired = int64(max)
	}
	return int32(desired) // #nosec G115 -- bounded by int32 min and max above.
}
