package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

// mockStateProvider implements StateProvider for testing
type mockStateProvider struct {
	status      *StatusResult
	routes      []RouteInfo
	configPath  string
	addRouteErr error
	removeErr   error
	traceResult *RouteTraceResult
	traceErr    error
}

func (m *mockStateProvider) GetStatus() *StatusResult {
	return m.status
}

func (m *mockStateProvider) GetRoutes() []RouteInfo {
	return m.routes
}

func (m *mockStateProvider) AddRoute(dest, comment string, persist bool) error {
	if m.addRouteErr != nil {
		return m.addRouteErr
	}
	m.routes = append(m.routes, RouteInfo{
		Destination: dest,
		Comment:     comment,
		Source:      "manual",
		Enabled:     true,
	})
	return nil
}

func (m *mockStateProvider) RemoveRoute(dest string, persist bool) error {
	if m.removeErr != nil {
		return m.removeErr
	}
	for i, r := range m.routes {
		if r.Destination == dest {
			m.routes = append(m.routes[:i], m.routes[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *mockStateProvider) TraceRoute(dest string) (*RouteTraceResult, error) {
	if m.traceErr != nil {
		return nil, m.traceErr
	}
	if m.traceResult != nil {
		return m.traceResult, nil
	}
	return &RouteTraceResult{
		Destination:  dest,
		ResolvedIP:   "10.0.0.1",
		MatchedRoute: "10.0.0.0/8",
		Interface:    "tun0",
		IsMutiauk:    true,
	}, nil
}

func (m *mockStateProvider) GetConfigPath() string {
	return m.configPath
}

// testServer creates a test server with a temporary socket
func testServer(t *testing.T, state StateProvider) (*Server, *Client, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "mutiauk-api-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	socketPath := filepath.Join(tmpDir, "test.sock")
	logger := zap.NewNop()

	server := NewServer(socketPath, state, logger)
	ctx, cancel := context.WithCancel(context.Background())

	if err := server.Start(ctx); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to start server: %v", err)
	}

	// Wait for socket to be ready
	time.Sleep(10 * time.Millisecond)

	client := NewClient(socketPath)
	client.SetTimeout(2 * time.Second)

	cleanup := func() {
		cancel()
		server.Stop()
		os.RemoveAll(tmpDir)
	}

	return server, client, cleanup
}

func TestClientIsRunning(t *testing.T) {
	state := &mockStateProvider{
		status: &StatusResult{
			Running:    true,
			PID:        12345,
			ConfigPath: "/etc/mutiauk/config.yaml",
		},
	}

	_, client, cleanup := testServer(t, state)
	defer cleanup()

	if !client.IsRunning() {
		t.Error("expected IsRunning to return true")
	}

	// Test with non-existent socket
	badClient := NewClient("/nonexistent/socket.sock")
	if badClient.IsRunning() {
		t.Error("expected IsRunning to return false for non-existent socket")
	}
}

func TestStatus(t *testing.T) {
	expected := &StatusResult{
		Running:      true,
		PID:          12345,
		ConfigPath:   "/etc/mutiauk/config.yaml",
		Uptime:       "1h30m",
		TUNName:      "tun0",
		TUNAddress:   "10.200.200.1/24",
		SOCKS5Server: "127.0.0.1:1080",
		SOCKS5Status: "connected",
		AutoRoutes: AutoRoutesStatus{
			Enabled: true,
			URL:     "http://localhost:8080",
			Count:   5,
		},
		RouteCount: RouteCountStatus{
			Config: 3,
			Auto:   5,
			Total:  8,
		},
	}

	state := &mockStateProvider{status: expected}
	_, client, cleanup := testServer(t, state)
	defer cleanup()

	result, err := client.Status()
	if err != nil {
		t.Fatalf("Status() failed: %v", err)
	}

	if result.PID != expected.PID {
		t.Errorf("PID = %d, want %d", result.PID, expected.PID)
	}
	if result.ConfigPath != expected.ConfigPath {
		t.Errorf("ConfigPath = %s, want %s", result.ConfigPath, expected.ConfigPath)
	}
	if result.TUNName != expected.TUNName {
		t.Errorf("TUNName = %s, want %s", result.TUNName, expected.TUNName)
	}
	if result.AutoRoutes.Count != expected.AutoRoutes.Count {
		t.Errorf("AutoRoutes.Count = %d, want %d", result.AutoRoutes.Count, expected.AutoRoutes.Count)
	}
}

func TestRouteList(t *testing.T) {
	routes := []RouteInfo{
		{
			Destination: "10.0.0.0/8",
			Interface:   "tun0",
			Comment:     "Private network",
			Source:      "config",
			Enabled:     true,
		},
		{
			Destination: "192.168.0.0/16",
			Interface:   "tun0",
			Source:      "auto",
			Enabled:     true,
		},
	}

	state := &mockStateProvider{routes: routes}
	_, client, cleanup := testServer(t, state)
	defer cleanup()

	result, err := client.RouteList()
	if err != nil {
		t.Fatalf("RouteList() failed: %v", err)
	}

	if len(result.Routes) != 2 {
		t.Errorf("len(Routes) = %d, want 2", len(result.Routes))
	}

	if result.Routes[0].Destination != "10.0.0.0/8" {
		t.Errorf("Routes[0].Destination = %s, want 10.0.0.0/8", result.Routes[0].Destination)
	}
}

func TestRouteAdd(t *testing.T) {
	state := &mockStateProvider{routes: []RouteInfo{}}
	_, client, cleanup := testServer(t, state)
	defer cleanup()

	result, err := client.RouteAdd("172.16.0.0/12", "Test route", true)
	if err != nil {
		t.Fatalf("RouteAdd() failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Success = false, want true")
	}
	if !result.Persisted {
		t.Errorf("Persisted = false, want true")
	}

	// Verify route was added to state
	if len(state.routes) != 1 {
		t.Errorf("len(state.routes) = %d, want 1", len(state.routes))
	}
	if state.routes[0].Destination != "172.16.0.0/12" {
		t.Errorf("route destination = %s, want 172.16.0.0/12", state.routes[0].Destination)
	}
}

func TestRouteAddError(t *testing.T) {
	state := &mockStateProvider{
		routes:      []RouteInfo{},
		addRouteErr: os.ErrPermission,
	}
	_, client, cleanup := testServer(t, state)
	defer cleanup()

	result, err := client.RouteAdd("10.0.0.0/8", "", false)
	if err != nil {
		t.Fatalf("RouteAdd() failed: %v", err)
	}

	if result.Success {
		t.Error("Success = true, want false")
	}
	if result.Message == "" {
		t.Error("Message should not be empty on error")
	}
}

func TestRouteRemove(t *testing.T) {
	state := &mockStateProvider{
		routes: []RouteInfo{
			{Destination: "10.0.0.0/8", Source: "config"},
			{Destination: "192.168.0.0/16", Source: "config"},
		},
	}
	_, client, cleanup := testServer(t, state)
	defer cleanup()

	result, err := client.RouteRemove("10.0.0.0/8", true)
	if err != nil {
		t.Fatalf("RouteRemove() failed: %v", err)
	}

	if !result.Success {
		t.Error("Success = false, want true")
	}

	// Verify route was removed
	if len(state.routes) != 1 {
		t.Errorf("len(state.routes) = %d, want 1", len(state.routes))
	}
	if state.routes[0].Destination != "192.168.0.0/16" {
		t.Errorf("remaining route = %s, want 192.168.0.0/16", state.routes[0].Destination)
	}
}

func TestRouteTrace(t *testing.T) {
	expected := &RouteTraceResult{
		Destination:  "10.0.0.1",
		ResolvedIP:   "10.0.0.1",
		MatchedRoute: "10.0.0.0/8",
		Interface:    "tun0",
		IsMutiauk:    true,
		MeshPath:     []string{"Agent-A", "Agent-B"},
		Origin:       "Agent-B",
		OriginID:     "abc123",
		HopCount:     1,
	}

	state := &mockStateProvider{traceResult: expected}
	_, client, cleanup := testServer(t, state)
	defer cleanup()

	result, err := client.RouteTrace("10.0.0.1")
	if err != nil {
		t.Fatalf("RouteTrace() failed: %v", err)
	}

	if result.MatchedRoute != expected.MatchedRoute {
		t.Errorf("MatchedRoute = %s, want %s", result.MatchedRoute, expected.MatchedRoute)
	}
	if result.Interface != expected.Interface {
		t.Errorf("Interface = %s, want %s", result.Interface, expected.Interface)
	}
	if !result.IsMutiauk {
		t.Error("IsMutiauk = false, want true")
	}
	if result.HopCount != expected.HopCount {
		t.Errorf("HopCount = %d, want %d", result.HopCount, expected.HopCount)
	}
}

func TestConfigPath(t *testing.T) {
	expected := "/etc/mutiauk/config.yaml"
	state := &mockStateProvider{configPath: expected}
	_, client, cleanup := testServer(t, state)
	defer cleanup()

	result, err := client.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath() failed: %v", err)
	}

	if result != expected {
		t.Errorf("ConfigPath = %s, want %s", result, expected)
	}
}

func TestMethodNotFound(t *testing.T) {
	state := &mockStateProvider{}
	_, client, cleanup := testServer(t, state)
	defer cleanup()

	resp, err := client.Call("nonexistent.method", nil)
	if err != nil {
		t.Fatalf("Call() failed: %v", err)
	}

	if resp.Error == nil {
		t.Error("expected error response for nonexistent method")
	}
	if resp.Error.Code != ErrCodeMethodNotFound {
		t.Errorf("Error.Code = %d, want %d", resp.Error.Code, ErrCodeMethodNotFound)
	}
}

func TestProtocolTypes(t *testing.T) {
	t.Run("Request", func(t *testing.T) {
		req := Request{
			Method: "status",
			Params: json.RawMessage(`{"key": "value"}`),
			ID:     42,
		}

		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}

		var decoded Request
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal failed: %v", err)
		}

		if decoded.Method != req.Method {
			t.Errorf("Method = %s, want %s", decoded.Method, req.Method)
		}
		if decoded.ID != req.ID {
			t.Errorf("ID = %d, want %d", decoded.ID, req.ID)
		}
	})

	t.Run("Response", func(t *testing.T) {
		resp := Response{
			Result: json.RawMessage(`{"status": "ok"}`),
			ID:     42,
		}

		data, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}

		var decoded Response
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal failed: %v", err)
		}

		if decoded.ID != resp.ID {
			t.Errorf("ID = %d, want %d", decoded.ID, resp.ID)
		}
		if decoded.Error != nil {
			t.Error("Error should be nil")
		}
	})

	t.Run("ErrorResponse", func(t *testing.T) {
		resp := Response{
			Error: &Error{
				Code:    ErrCodeMethodNotFound,
				Message: "method not found",
			},
			ID: 1,
		}

		data, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}

		var decoded Response
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("Unmarshal failed: %v", err)
		}

		if decoded.Error == nil {
			t.Fatal("Error should not be nil")
		}
		if decoded.Error.Code != ErrCodeMethodNotFound {
			t.Errorf("Error.Code = %d, want %d", decoded.Error.Code, ErrCodeMethodNotFound)
		}
	})
}

func TestStatusResult(t *testing.T) {
	status := StatusResult{
		Running:      true,
		PID:          1234,
		ConfigPath:   "/etc/mutiauk/config.yaml",
		Uptime:       "2h",
		TUNName:      "tun0",
		TUNAddress:   "10.200.200.1/24",
		SOCKS5Server: "127.0.0.1:1080",
		SOCKS5Status: "connected",
		AutoRoutes: AutoRoutesStatus{
			Enabled: true,
			URL:     "http://localhost:8080",
			Count:   3,
		},
		RouteCount: RouteCountStatus{
			Config: 2,
			Auto:   3,
			Total:  5,
		},
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded StatusResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.PID != status.PID {
		t.Errorf("PID = %d, want %d", decoded.PID, status.PID)
	}
	if decoded.AutoRoutes.Count != status.AutoRoutes.Count {
		t.Errorf("AutoRoutes.Count = %d, want %d", decoded.AutoRoutes.Count, status.AutoRoutes.Count)
	}
}

func TestRouteInfo(t *testing.T) {
	route := RouteInfo{
		Destination: "10.0.0.0/8",
		Interface:   "tun0",
		Gateway:     "",
		Comment:     "Private network",
		Source:      "config",
		Enabled:     true,
	}

	data, err := json.Marshal(route)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded RouteInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Destination != route.Destination {
		t.Errorf("Destination = %s, want %s", decoded.Destination, route.Destination)
	}
	if decoded.Source != route.Source {
		t.Errorf("Source = %s, want %s", decoded.Source, route.Source)
	}
}

func TestConcurrentRequests(t *testing.T) {
	state := &mockStateProvider{
		status: &StatusResult{
			Running: true,
			PID:     12345,
		},
		routes: []RouteInfo{
			{Destination: "10.0.0.0/8"},
		},
	}

	_, client, cleanup := testServer(t, state)
	defer cleanup()

	// Run multiple concurrent requests
	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := client.Status()
			done <- err
		}()
	}

	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent request failed: %v", err)
		}
	}
}
