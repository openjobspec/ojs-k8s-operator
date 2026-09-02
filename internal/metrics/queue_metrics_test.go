package metrics

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestDesiredReplicas(t *testing.T) {
	tests := []struct {
		depth, target int64
		min, max      int32
		want          int32
	}{
		{0, 10, 1, 10, 1},    // empty queue → min
		{5, 10, 1, 10, 1},    // below target → 1 (>= min)
		{10, 10, 1, 10, 1},   // exactly target → 1
		{25, 10, 1, 10, 3},   // 25 jobs / 10 per worker → 3
		{100, 10, 1, 10, 10}, // capped at max
		{1000, 10, 1, 5, 5},  // capped at max
		{50, 10, 3, 20, 5},   // 50/10 = 5
		{0, 10, 3, 20, 3},    // empty → min
		{math.MaxInt64, 1, 1, 100, 100},
	}
	for _, tt := range tests {
		got := DesiredReplicas(tt.depth, tt.target, tt.min, tt.max)
		if got != tt.want {
			t.Errorf("DesiredReplicas(%d, %d, %d, %d) = %d, want %d",
				tt.depth, tt.target, tt.min, tt.max, got, tt.want)
		}
	}
}

func TestCollectorPoll(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"queues": []map[string]any{
				{"name": "default", "pending": 15, "active": 3},
				{"name": "email", "pending": 42, "active": 8},
			},
		})
	}))
	defer server.Close()

	c := NewCollector(server.URL, "", 0)
	err := c.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}

	depth, ok := c.GetQueueDepth("default")
	if !ok {
		t.Fatal("expected default queue metrics")
	}
	if depth != 15 {
		t.Errorf("expected 15 pending, got %d", depth)
	}

	depth, ok = c.GetQueueDepth("email")
	if !ok {
		t.Fatal("expected email queue metrics")
	}
	if depth != 42 {
		t.Errorf("expected 42 pending, got %d", depth)
	}

	all := c.GetAllMetrics()
	if len(all) != 2 {
		t.Errorf("expected 2 queues, got %d", len(all))
	}
}

func TestCollectorPollError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer server.Close()

	c := NewCollector(server.URL, "", 0)
	err := c.Poll(context.Background())
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestCollectorPollWithAuth(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{"queues": []any{}})
	}))
	defer server.Close()

	c := NewCollector(server.URL, "my-key", 0)
	c.Poll(context.Background())

	if authHeader != "Bearer my-key" {
		t.Errorf("expected Bearer auth, got %q", authHeader)
	}
}

func TestGetQueueDepthNotFound(t *testing.T) {
	c := NewCollector("http://unused", "", 0)
	_, ok := c.GetQueueDepth("nonexistent")
	if ok {
		t.Error("expected not found")
	}
}

// TestGetAllMetrics_ReturnsDeepCopy verifies that mutating a *QueueMetrics
// returned from GetAllMetrics does not corrupt the collector's internal
// cache (i.e. no pointer aliasing between caller and collector state).
func TestGetAllMetrics_ReturnsDeepCopy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"queues": []map[string]any{
				{"name": "default", "pending": 15, "active": 3},
			},
		})
	}))
	defer server.Close()

	c := NewCollector(server.URL, "", 0)
	if err := c.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	first := c.GetAllMetrics()
	first["default"].Pending = 99999
	first["default"].Queue = "mutated"

	second := c.GetAllMetrics()
	if second["default"].Pending != 15 {
		t.Errorf("expected internal cache unaffected by external mutation, got Pending=%d", second["default"].Pending)
	}
	if second["default"].Queue != "default" {
		t.Errorf("expected internal cache unaffected by external mutation, got Queue=%q", second["default"].Queue)
	}

	depth, ok := c.GetQueueDepth("default")
	if !ok || depth != 15 {
		t.Errorf("GetQueueDepth after external mutation = (%d, %v), want (15, true)", depth, ok)
	}
}

// TestGetAllMetrics_ReturnedMapIsIndependent verifies that adding/removing
// entries from a returned map does not affect subsequent reads.
func TestGetAllMetrics_ReturnedMapIsIndependent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"queues": []map[string]any{
				{"name": "default", "pending": 1, "active": 0},
			},
		})
	}))
	defer server.Close()

	c := NewCollector(server.URL, "", 0)
	if err := c.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	got := c.GetAllMetrics()
	delete(got, "default")
	got["injected"] = &QueueMetrics{Queue: "injected", Pending: 42}

	fresh := c.GetAllMetrics()
	if _, ok := fresh["default"]; !ok {
		t.Error("expected internal cache to still contain 'default' after caller deleted it from their copy")
	}
	if _, ok := fresh["injected"]; ok {
		t.Error("expected internal cache to be unaffected by entries injected into the caller's copy")
	}
}

// TestPoll_EvictsStaleQueues verifies that a queue reported in an earlier
// Poll but absent from a later one is removed from the cache, rather than
// lingering with stale data indefinitely.
func TestPoll_EvictsStaleQueues(t *testing.T) {
	var queues []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"queues": queues})
	}))
	defer server.Close()

	c := NewCollector(server.URL, "", 0)

	queues = []map[string]any{
		{"name": "default", "pending": 5, "active": 1},
		{"name": "transient", "pending": 7, "active": 2},
	}
	if err := c.Poll(context.Background()); err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	if all := c.GetAllMetrics(); len(all) != 2 {
		t.Fatalf("expected 2 queues after first poll, got %d", len(all))
	}

	// "transient" disappears from the upstream response.
	queues = []map[string]any{
		{"name": "default", "pending": 6, "active": 1},
	}
	if err := c.Poll(context.Background()); err != nil {
		t.Fatalf("second Poll: %v", err)
	}

	all := c.GetAllMetrics()
	if len(all) != 1 {
		t.Fatalf("expected stale queue to be evicted, got %d queues: %+v", len(all), all)
	}
	if _, ok := all["transient"]; ok {
		t.Error("expected 'transient' queue to be evicted from cache")
	}
	if _, ok := c.GetQueueDepth("transient"); ok {
		t.Error("expected GetQueueDepth('transient') to report not-found after eviction")
	}
	if depth, ok := c.GetQueueDepth("default"); !ok || depth != 6 {
		t.Errorf("GetQueueDepth('default') = (%d, %v), want (6, true)", depth, ok)
	}
}

// TestPoll_FailedPollDoesNotEvictExistingData verifies that a failed Poll
// (e.g. transient upstream error) leaves the previously cached metrics
// intact rather than clearing them.
func TestPoll_FailedPollDoesNotEvictExistingData(t *testing.T) {
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"queues": []map[string]any{{"name": "default", "pending": 3, "active": 0}},
		})
	}))
	defer server.Close()

	c := NewCollector(server.URL, "", 0)
	if err := c.Poll(context.Background()); err != nil {
		t.Fatalf("first Poll: %v", err)
	}

	fail = true
	if err := c.Poll(context.Background()); err == nil {
		t.Fatal("expected second Poll to fail")
	}

	depth, ok := c.GetQueueDepth("default")
	if !ok || depth != 3 {
		t.Errorf("expected cached metrics preserved after failed poll, got (%d, %v)", depth, ok)
	}
}

// TestCollector_ConcurrentPollAndReadRace exercises Poll racing against
// concurrent GetAllMetrics/GetQueueDepth readers; run with -race to detect
// any data races in the collector's locking.
func TestCollector_ConcurrentPollAndReadRace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"queues": []map[string]any{
				{"name": "default", "pending": 1, "active": 0},
				{"name": "email", "pending": 2, "active": 1},
			},
		})
	}))
	defer server.Close()

	c := NewCollector(server.URL, "", 0)

	const iterations = 50
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = c.Poll(context.Background())
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			all := c.GetAllMetrics()
			for k := range all {
				// Mutate the caller's copy to prove it can't race with Poll.
				all[k].Pending++
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			c.GetQueueDepth("default")
			c.GetQueueDepth("email")
		}
	}()

	wg.Wait()
}
