package autoroutes

import (
	"context"
	"encoding/json"
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
