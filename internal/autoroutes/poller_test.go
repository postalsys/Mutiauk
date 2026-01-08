package autoroutes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/postalsys/mutiauk/internal/route"
	"go.uber.org/zap"
)

func TestPoller_Start(t *testing.T) {
	logger := zap.NewNop()

	// Setup mock server
	callCount := 0
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()

		json.NewEncoder(w).Encode(DashboardResponse{
			Routes: []DashboardRoute{
				{Network: "10.10.0.0/16", RouteType: "cidr", OriginID: "abc"},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second, logger)
	poller := NewPoller(client, 50*time.Millisecond, "tun0", logger)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var updateCount int
	var lastRoutes []route.Route
	onUpdate := func(routes []route.Route) {
		updateCount++
		lastRoutes = routes
	}

	// Run poller (blocks until context cancelled)
	poller.Start(ctx, onUpdate)

	// Should have been called multiple times
	if updateCount < 2 {
		t.Errorf("expected at least 2 updates, got %d", updateCount)
	}

	// Should have received routes
	if len(lastRoutes) != 1 {
		t.Errorf("expected 1 route, got %d", len(lastRoutes))
	}
}

func TestPoller_GetLastRoutes(t *testing.T) {
	logger := zap.NewNop()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(DashboardResponse{
			Routes: []DashboardRoute{
				{Network: "10.10.0.0/16", RouteType: "cidr", OriginID: "abc"},
				{Network: "10.11.0.0/16", RouteType: "cidr", OriginID: "def"},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second, logger)
	poller := NewPoller(client, 1*time.Hour, "tun0", logger) // Long interval

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		poller.Start(ctx, nil)
		close(done)
	}()

	// Wait a bit for initial fetch
	time.Sleep(50 * time.Millisecond)

	routes := poller.GetLastRoutes()
	if len(routes) != 2 {
		t.Errorf("expected 2 routes, got %d", len(routes))
	}

	// Verify it returns a copy
	routes[0].Comment = "modified"
	original := poller.GetLastRoutes()
	if original[0].Comment == "modified" {
		t.Error("GetLastRoutes should return a copy, not the original slice")
	}

	<-done
}

func TestPoller_ErrorKeepsLastRoutes(t *testing.T) {
	logger := zap.NewNop()

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// First call succeeds
			json.NewEncoder(w).Encode(DashboardResponse{
				Routes: []DashboardRoute{
					{Network: "10.10.0.0/16", RouteType: "cidr", OriginID: "abc"},
				},
			})
		} else {
			// Subsequent calls fail
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second, logger)
	poller := NewPoller(client, 30*time.Millisecond, "tun0", logger)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	var updateCount int
	onUpdate := func(routes []route.Route) {
		updateCount++
	}

	poller.Start(ctx, onUpdate)

	// Should only have called onUpdate once (on success)
	if updateCount != 1 {
		t.Errorf("expected 1 update (only success), got %d", updateCount)
	}

	// Should still have last successful routes
	routes := poller.GetLastRoutes()
	if len(routes) != 1 {
		t.Errorf("expected 1 route to be preserved, got %d", len(routes))
	}

	// Should have recorded an error
	if poller.GetLastError() == nil {
		t.Error("expected error to be recorded")
	}
}

func TestPoller_FiltersRoutes(t *testing.T) {
	logger := zap.NewNop()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(DashboardResponse{
			Routes: []DashboardRoute{
				{Network: "10.10.0.0/16", RouteType: "cidr", OriginID: "abc"},
				{Network: "example.com", RouteType: "domain", OriginID: "def"},
				{Network: "0.0.0.0/0", RouteType: "cidr", OriginID: "ghi"},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second, logger)
	poller := NewPoller(client, 1*time.Hour, "tun0", logger)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var receivedRoutes []route.Route
	onUpdate := func(routes []route.Route) {
		receivedRoutes = routes
	}

	done := make(chan struct{})
	go func() {
		poller.Start(ctx, onUpdate)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	// Should only have the valid CIDR route (filtered out domain and default)
	if len(receivedRoutes) != 1 {
		t.Errorf("expected 1 route after filtering, got %d", len(receivedRoutes))
	}

	if receivedRoutes[0].Destination.String() != "10.10.0.0/16" {
		t.Errorf("expected 10.10.0.0/16, got %s", receivedRoutes[0].Destination.String())
	}
}

func TestPoller_DeduplicatesRoutes(t *testing.T) {
	logger := zap.NewNop()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(DashboardResponse{
			Routes: []DashboardRoute{
				{Network: "10.10.0.0/16", RouteType: "cidr", OriginID: "zzz"},
				{Network: "10.10.0.0/16", RouteType: "cidr", OriginID: "aaa"},
				{Network: "10.10.0.0/16", RouteType: "cidr", OriginID: "mmm"},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second, logger)
	poller := NewPoller(client, 1*time.Hour, "tun0", logger)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var receivedRoutes []route.Route
	onUpdate := func(routes []route.Route) {
		receivedRoutes = routes
	}

	done := make(chan struct{})
	go func() {
		poller.Start(ctx, onUpdate)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	// Should have deduplicated to 1 route
	if len(receivedRoutes) != 1 {
		t.Errorf("expected 1 route after deduplication, got %d", len(receivedRoutes))
	}
}
