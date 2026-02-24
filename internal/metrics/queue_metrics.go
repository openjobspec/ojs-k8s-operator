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
	Queue     string `json:"queue"`
	Pending   int64  `json:"pending"`
	Active    int64  `json:"active"`
	Total     int64  `json:"total"`
	Timestamp time.Time `json:"timestamp"`
}

// Collector polls OJS queue stats and caches the results.
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

// GetAllMetrics returns metrics for all queues.
func (c *Collector) GetAllMetrics() map[string]*QueueMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make(map[string]*QueueMetrics, len(c.metrics))
	for k, v := range c.metrics {
		result[k] = v
	}
	return result
}

// Poll fetches the latest queue stats from the OJS backend.
func (c *Collector) Poll(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.ojsURL+"/ojs/v1/queues", nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("polling queues: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Queues []struct {
			Name    string `json:"name"`
			Pending int64  `json:"pending"`
			Active  int64  `json:"active"`
		} `json:"queues"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for _, q := range result.Queues {
		c.metrics[q.Name] = &QueueMetrics{
			Queue:     q.Name,
			Pending:   q.Pending,
			Active:    q.Active,
			Total:     q.Pending + q.Active,
			Timestamp: now,
		}
	}
	return nil
}

// DesiredReplicas calculates the desired replica count based on queue depth.
func DesiredReplicas(queueDepth int64, targetPerWorker int64, min, max int32) int32 {
	if targetPerWorker <= 0 {
		targetPerWorker = 10
	}
	desired := int32(queueDepth / targetPerWorker)
	if queueDepth%targetPerWorker > 0 {
		desired++
	}
	if desired < min {
		desired = min
	}
	if desired > max {
		desired = max
	}
	return desired
}
