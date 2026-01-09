package autoroutes

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestClient_FetchDashboard(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name        string
		response    interface{}
		statusCode  int
		wantErr     bool
		wantRoutes  int
	}{
		{
			name: "success with routes",
			response: DashboardResponse{
				Routes: []DashboardRoute{
					{Network: "10.10.0.0/16", RouteType: "cidr", Origin: "agent-a", OriginID: "abc123"},
					{Network: "10.11.0.0/16", RouteType: "cidr", Origin: "agent-b", OriginID: "def456"},
				},
			},
			statusCode: http.StatusOK,
			wantErr:    false,
			wantRoutes: 2,
		},
		{
			name: "success empty routes",
			response: DashboardResponse{
				Routes: []DashboardRoute{},
			},
			statusCode: http.StatusOK,
			wantErr:    false,
			wantRoutes: 0,
		},
		{
			name:       "server error",
			response:   nil,
			statusCode: http.StatusInternalServerError,
			wantErr:    true,
		},
		{
			name:       "not found",
			response:   nil,
			statusCode: http.StatusNotFound,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/dashboard" {
					t.Errorf("expected path /api/dashboard, got %s", r.URL.Path)
				}
				if r.Method != http.MethodGet {
					t.Errorf("expected GET method, got %s", r.Method)
				}

				w.WriteHeader(tt.statusCode)
				if tt.response != nil {
					json.NewEncoder(w).Encode(tt.response)
				}
			}))
			defer server.Close()

			client := NewClient(server.URL, 5*time.Second, logger)
			resp, err := client.FetchDashboard(context.Background())

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(resp.Routes) != tt.wantRoutes {
				t.Errorf("expected %d routes, got %d", tt.wantRoutes, len(resp.Routes))
			}
		})
	}
}

func TestClient_FetchDashboard_InvalidJSON(t *testing.T) {
	logger := zap.NewNop()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second, logger)
	_, err := client.FetchDashboard(context.Background())

	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestClient_FetchDashboard_Timeout(t *testing.T) {
	logger := zap.NewNop()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, 50*time.Millisecond, logger)
	_, err := client.FetchDashboard(context.Background())

	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

func TestClient_FetchDashboard_ContextCancelled(t *testing.T) {
	logger := zap.NewNop()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second, logger)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := client.FetchDashboard(ctx)

	if err == nil {
		t.Error("expected context cancelled error, got nil")
	}
}

// --- LookupPath Tests ---

func TestClient_LookupPath(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name       string
		routes     []DashboardRoute
		lookupIP   string
		wantMatch  bool
		wantNet    string
		wantOrigin string
	}{
		{
			name: "exact match",
			routes: []DashboardRoute{
				{Network: "10.10.0.0/16", RouteType: "cidr", Origin: "agent-a", OriginID: "abc123"},
			},
			lookupIP:   "10.10.5.5",
			wantMatch:  true,
			wantNet:    "10.10.0.0/16",
			wantOrigin: "agent-a",
		},
		{
			name: "longest prefix match",
			routes: []DashboardRoute{
				{Network: "10.0.0.0/8", RouteType: "cidr", Origin: "agent-a", OriginID: "abc"},
				{Network: "10.10.0.0/16", RouteType: "cidr", Origin: "agent-b", OriginID: "def"},
				{Network: "10.10.5.0/24", RouteType: "cidr", Origin: "agent-c", OriginID: "ghi"},
			},
			lookupIP:   "10.10.5.100",
			wantMatch:  true,
			wantNet:    "10.10.5.0/24",
			wantOrigin: "agent-c",
		},
		{
			name: "no match",
			routes: []DashboardRoute{
				{Network: "10.10.0.0/16", RouteType: "cidr", Origin: "agent-a", OriginID: "abc"},
			},
			lookupIP:  "192.168.1.1",
			wantMatch: false,
		},
		{
			name: "skips domain routes",
			routes: []DashboardRoute{
				{Network: "example.com", RouteType: "domain", Origin: "agent-a", OriginID: "abc"},
				{Network: "10.10.0.0/16", RouteType: "cidr", Origin: "agent-b", OriginID: "def"},
			},
			lookupIP:   "10.10.1.1",
			wantMatch:  true,
			wantNet:    "10.10.0.0/16",
			wantOrigin: "agent-b",
		},
		{
			name: "skips invalid CIDR",
			routes: []DashboardRoute{
				{Network: "invalid-cidr", RouteType: "cidr", Origin: "agent-a", OriginID: "abc"},
				{Network: "10.10.0.0/16", RouteType: "cidr", Origin: "agent-b", OriginID: "def"},
			},
			lookupIP:   "10.10.1.1",
			wantMatch:  true,
			wantNet:    "10.10.0.0/16",
			wantOrigin: "agent-b",
		},
		{
			name:      "empty routes",
			routes:    []DashboardRoute{},
			lookupIP:  "10.10.1.1",
			wantMatch: false,
		},
		{
			name: "IPv6 match",
			routes: []DashboardRoute{
				{Network: "2001:db8::/32", RouteType: "cidr", Origin: "agent-a", OriginID: "abc"},
			},
			lookupIP:   "2001:db8::1",
			wantMatch:  true,
			wantNet:    "2001:db8::/32",
			wantOrigin: "agent-a",
		},
		{
			name: "preserves path info",
			routes: []DashboardRoute{
				{
					Network:     "10.10.0.0/16",
					RouteType:   "cidr",
					Origin:      "agent-a",
					OriginID:    "abc123",
					HopCount:    2,
					PathDisplay: []string{"agent-local", "agent-a"},
					PathIDs:     []string{"local", "abc123"},
				},
			},
			lookupIP:   "10.10.1.1",
			wantMatch:  true,
			wantNet:    "10.10.0.0/16",
			wantOrigin: "agent-a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(DashboardResponse{Routes: tt.routes})
			}))
			defer server.Close()

			client := NewClient(server.URL, 5*time.Second, logger)

			ip := net.ParseIP(tt.lookupIP)
			result, err := client.LookupPath(context.Background(), ip)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantMatch {
				if result == nil {
					t.Fatal("expected match, got nil")
				}
				if result.Network != tt.wantNet {
					t.Errorf("Network = %v, want %v", result.Network, tt.wantNet)
				}
				if result.Origin != tt.wantOrigin {
					t.Errorf("Origin = %v, want %v", result.Origin, tt.wantOrigin)
				}
			} else {
				if result != nil {
					t.Errorf("expected no match, got %+v", result)
				}
			}
		})
	}
}

func TestClient_LookupPath_PreservesPathInfo(t *testing.T) {
	logger := zap.NewNop()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(DashboardResponse{
			Routes: []DashboardRoute{
				{
					Network:     "10.10.0.0/16",
					RouteType:   "cidr",
					Origin:      "agent-target",
					OriginID:    "target123",
					HopCount:    3,
					PathDisplay: []string{"local-agent", "relay-agent", "agent-target"},
					PathIDs:     []string{"local", "relay", "target123"},
				},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second, logger)
	result, err := client.LookupPath(context.Background(), net.ParseIP("10.10.5.5"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected result, got nil")
	}

	if result.HopCount != 3 {
		t.Errorf("HopCount = %d, want 3", result.HopCount)
	}

	if len(result.PathDisplay) != 3 {
		t.Errorf("PathDisplay length = %d, want 3", len(result.PathDisplay))
	}

	if len(result.PathIDs) != 3 {
		t.Errorf("PathIDs length = %d, want 3", len(result.PathIDs))
	}

	if result.OriginID != "target123" {
		t.Errorf("OriginID = %s, want target123", result.OriginID)
	}
}

func TestClient_LookupPath_APIError(t *testing.T) {
	logger := zap.NewNop()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.URL, 5*time.Second, logger)
	_, err := client.LookupPath(context.Background(), net.ParseIP("10.10.1.1"))

	if err == nil {
		t.Error("expected error for API failure, got nil")
	}
}

func TestNewClient(t *testing.T) {
	logger := zap.NewNop()

	client := NewClient("http://localhost:3000", 10*time.Second, logger)

	if client == nil {
		t.Fatal("NewClient returned nil")
	}

	if client.baseURL != "http://localhost:3000" {
		t.Errorf("baseURL = %s, want http://localhost:3000", client.baseURL)
	}

	if client.httpClient == nil {
		t.Error("httpClient is nil")
	}

	if client.httpClient.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", client.httpClient.Timeout)
	}
}
