package metrics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDesiredReplicas(t *testing.T) {
	tests := []struct {
		depth, target int64
		min, max      int32
		want          int32
	}{
		{0, 10, 1, 10, 1},      // empty queue → min
		{5, 10, 1, 10, 1},      // below target → 1 (>= min)
		{10, 10, 1, 10, 1},     // exactly target → 1
		{25, 10, 1, 10, 3},     // 25 jobs / 10 per worker → 3
		{100, 10, 1, 10, 10},   // capped at max
		{1000, 10, 1, 5, 5},    // capped at max
		{50, 10, 3, 20, 5},     // 50/10 = 5
		{0, 10, 3, 20, 3},      // empty → min
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
