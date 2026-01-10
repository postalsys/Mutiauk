package cli

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/postalsys/mutiauk/internal/config"
)

func TestConfigRoutesToRoutes(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *config.Config
		wantLen     int
		wantErr     bool
		errContains string
	}{
		{
			name: "empty routes",
			cfg: &config.Config{
				TUN:    config.TUNConfig{Name: "tun0"},
				Routes: []config.RouteConfig{},
			},
			wantLen: 0,
			wantErr: false,
		},
		{
			name: "single enabled route",
			cfg: &config.Config{
				TUN: config.TUNConfig{Name: "tun0"},
				Routes: []config.RouteConfig{
					{Destination: "10.0.0.0/8", Comment: "Test", Enabled: true},
				},
			},
			wantLen: 1,
			wantErr: false,
		},
		{
			name: "multiple routes with disabled",
			cfg: &config.Config{
				TUN: config.TUNConfig{Name: "tun0"},
				Routes: []config.RouteConfig{
					{Destination: "10.0.0.0/8", Comment: "A", Enabled: true},
					{Destination: "172.16.0.0/12", Comment: "B", Enabled: false},
					{Destination: "192.168.0.0/16", Comment: "C", Enabled: true},
				},
			},
			wantLen: 2, // Only enabled routes
			wantErr: false,
		},
		{
			name: "all disabled routes",
			cfg: &config.Config{
				TUN: config.TUNConfig{Name: "tun0"},
				Routes: []config.RouteConfig{
					{Destination: "10.0.0.0/8", Comment: "A", Enabled: false},
					{Destination: "172.16.0.0/12", Comment: "B", Enabled: false},
				},
			},
			wantLen: 0,
			wantErr: false,
		},
		{
			name: "invalid CIDR",
			cfg: &config.Config{
				TUN: config.TUNConfig{Name: "tun0"},
				Routes: []config.RouteConfig{
					{Destination: "invalid", Comment: "Bad", Enabled: true},
				},
			},
			wantLen:     0,
			wantErr:     true,
			errContains: "invalid route in config",
		},
		{
			name: "IPv6 route",
			cfg: &config.Config{
				TUN: config.TUNConfig{Name: "tun0"},
				Routes: []config.RouteConfig{
					{Destination: "fd00::/8", Comment: "IPv6", Enabled: true},
				},
			},
			wantLen: 1,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routes, err := configRoutesToRoutes(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Errorf("configRoutesToRoutes() expected error, got nil")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("configRoutesToRoutes() error = %v, want containing %v", err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("configRoutesToRoutes() unexpected error: %v", err)
				return
			}
			if len(routes) != tt.wantLen {
				t.Errorf("configRoutesToRoutes() got %d routes, want %d", len(routes), tt.wantLen)
			}
			// Verify interface name is set correctly
			for _, r := range routes {
				if r.Interface != tt.cfg.TUN.Name {
					t.Errorf("route interface = %s, want %s", r.Interface, tt.cfg.TUN.Name)
				}
			}
		})
	}
}

func TestConfigRoutesToRoutesAll(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *config.Config
		wantLen     int
		wantErr     bool
		errContains string
	}{
		{
			name: "empty routes",
			cfg: &config.Config{
				TUN:    config.TUNConfig{Name: "tun0"},
				Routes: []config.RouteConfig{},
			},
			wantLen: 0,
			wantErr: false,
		},
		{
			name: "includes disabled routes",
			cfg: &config.Config{
				TUN: config.TUNConfig{Name: "tun0"},
				Routes: []config.RouteConfig{
					{Destination: "10.0.0.0/8", Comment: "A", Enabled: true},
					{Destination: "172.16.0.0/12", Comment: "B", Enabled: false},
					{Destination: "192.168.0.0/16", Comment: "C", Enabled: true},
				},
			},
			wantLen: 3, // All routes including disabled
			wantErr: false,
		},
		{
			name: "all disabled routes",
			cfg: &config.Config{
				TUN: config.TUNConfig{Name: "tun0"},
				Routes: []config.RouteConfig{
					{Destination: "10.0.0.0/8", Comment: "A", Enabled: false},
					{Destination: "172.16.0.0/12", Comment: "B", Enabled: false},
				},
			},
			wantLen: 2, // All routes
			wantErr: false,
		},
		{
			name: "invalid CIDR",
			cfg: &config.Config{
				TUN: config.TUNConfig{Name: "tun0"},
				Routes: []config.RouteConfig{
					{Destination: "not-a-cidr", Comment: "Bad", Enabled: false},
				},
			},
			wantLen:     0,
			wantErr:     true,
			errContains: "invalid route in config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routes, err := configRoutesToRoutesAll(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Errorf("configRoutesToRoutesAll() expected error, got nil")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("configRoutesToRoutesAll() error = %v, want containing %v", err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("configRoutesToRoutesAll() unexpected error: %v", err)
				return
			}
			if len(routes) != tt.wantLen {
				t.Errorf("configRoutesToRoutesAll() got %d routes, want %d", len(routes), tt.wantLen)
			}
		})
	}
}

func TestConfigRoutesToRoutes_PreservesMetadata(t *testing.T) {
	cfg := &config.Config{
		TUN: config.TUNConfig{Name: "tun-test"},
		Routes: []config.RouteConfig{
			{Destination: "10.0.0.0/8", Comment: "Private A", Enabled: true},
		},
	}

	routes, err := configRoutesToRoutes(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	if r.Comment != "Private A" {
		t.Errorf("comment = %q, want %q", r.Comment, "Private A")
	}
	if r.Interface != "tun-test" {
		t.Errorf("interface = %q, want %q", r.Interface, "tun-test")
	}
	if !r.Enabled {
		t.Errorf("enabled = false, want true")
	}

	expectedCIDR := "10.0.0.0/8"
	if r.Destination.String() != expectedCIDR {
		t.Errorf("destination = %s, want %s", r.Destination.String(), expectedCIDR)
	}
}

func TestConfigRoutesToRoutesAll_PreservesEnabledState(t *testing.T) {
	cfg := &config.Config{
		TUN: config.TUNConfig{Name: "tun0"},
		Routes: []config.RouteConfig{
			{Destination: "10.0.0.0/8", Comment: "Enabled", Enabled: true},
			{Destination: "172.16.0.0/12", Comment: "Disabled", Enabled: false},
		},
	}

	routes, err := configRoutesToRoutesAll(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}

	// First route should be enabled
	if !routes[0].Enabled {
		t.Errorf("routes[0].Enabled = false, want true")
	}
	// Second route should be disabled
	if routes[1].Enabled {
		t.Errorf("routes[1].Enabled = true, want false")
	}
}

func TestDaemonAPIClient_NotRunning(t *testing.T) {
	// Create a client with a non-existent socket
	cfg := &config.Config{
		Daemon: config.DaemonConfig{
			SocketPath: "/nonexistent/socket.sock",
		},
	}

	client := newDaemonAPIClient(cfg)

	if client.IsRunning() {
		t.Error("IsRunning() = true for non-existent socket, want false")
	}

	// AddRoute should return nil, nil when not running
	result, err := client.AddRoute("10.0.0.0/8", "test", false)
	if err != nil {
		t.Errorf("AddRoute() error = %v, want nil", err)
	}
	if result != nil {
		t.Errorf("AddRoute() result = %v, want nil", result)
	}

	// RemoveRoute should return nil, nil when not running
	removeResult, err := client.RemoveRoute("10.0.0.0/8", false)
	if err != nil {
		t.Errorf("RemoveRoute() error = %v, want nil", err)
	}
	if removeResult != nil {
		t.Errorf("RemoveRoute() result = %v, want nil", removeResult)
	}
}

func TestLoadConfig(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test-config.yaml")

	configContent := `
tun:
  name: tun-test
  mtu: 1400
  address: 10.200.200.1/24
socks5:
  server: 127.0.0.1:1080
routes:
  - destination: 10.0.0.0/8
    comment: Test
    enabled: true
`
	if err := os.WriteFile(tmpFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	// Save original cfgFile and restore after test
	originalCfgFile := cfgFile
	defer func() { cfgFile = originalCfgFile }()

	// Test loading valid config
	cfgFile = tmpFile
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg.TUN.Name != "tun-test" {
		t.Errorf("TUN.Name = %s, want tun-test", cfg.TUN.Name)
	}
	if cfg.SOCKS5.Server != "127.0.0.1:1080" {
		t.Errorf("SOCKS5.Server = %s, want 127.0.0.1:1080", cfg.SOCKS5.Server)
	}

	// Test loading non-existent config
	cfgFile = "/nonexistent/config.yaml"
	_, err = loadConfig()
	if err == nil {
		t.Error("loadConfig() expected error for non-existent file")
	}
}

func TestRouteAddResult(t *testing.T) {
	result := &RouteAddResult{
		Success:   true,
		Persisted: true,
	}

	if !result.Success {
		t.Error("Success = false, want true")
	}
	if !result.Persisted {
		t.Error("Persisted = false, want true")
	}
}

func TestRouteRemoveResult(t *testing.T) {
	result := &RouteRemoveResult{
		Success:   true,
		Persisted: false,
	}

	if !result.Success {
		t.Error("Success = false, want true")
	}
	if result.Persisted {
		t.Error("Persisted = true, want false")
	}
}

func TestConfigRoutesToRoutes_IPv4AndIPv6(t *testing.T) {
	cfg := &config.Config{
		TUN: config.TUNConfig{Name: "tun0"},
		Routes: []config.RouteConfig{
			{Destination: "10.0.0.0/8", Comment: "IPv4", Enabled: true},
			{Destination: "192.168.1.0/24", Comment: "IPv4 small", Enabled: true},
			{Destination: "fd00::/8", Comment: "IPv6 ULA", Enabled: true},
			{Destination: "2001:db8::/32", Comment: "IPv6 doc", Enabled: true},
		},
	}

	routes, err := configRoutesToRoutes(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 4 {
		t.Fatalf("expected 4 routes, got %d", len(routes))
	}

	// Verify IPv4 routes
	if routes[0].Destination.IP.To4() == nil {
		t.Error("routes[0] should be IPv4")
	}
	if routes[1].Destination.IP.To4() == nil {
		t.Error("routes[1] should be IPv4")
	}

	// Verify IPv6 routes
	if routes[2].Destination.IP.To4() != nil {
		t.Error("routes[2] should be IPv6")
	}
	if routes[3].Destination.IP.To4() != nil {
		t.Error("routes[3] should be IPv6")
	}
}

func TestConfigRoutesToRoutes_HostRoutes(t *testing.T) {
	cfg := &config.Config{
		TUN: config.TUNConfig{Name: "tun0"},
		Routes: []config.RouteConfig{
			{Destination: "192.168.1.100/32", Comment: "Single host", Enabled: true},
			{Destination: "2001:db8::1/128", Comment: "Single IPv6 host", Enabled: true},
		},
	}

	routes, err := configRoutesToRoutes(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}

	// Verify /32 mask
	ones, _ := routes[0].Destination.Mask.Size()
	if ones != 32 {
		t.Errorf("routes[0] mask size = %d, want 32", ones)
	}

	// Verify /128 mask
	ones, _ = routes[1].Destination.Mask.Size()
	if ones != 128 {
		t.Errorf("routes[1] mask size = %d, want 128", ones)
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && searchString(s, substr)))
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Verify Route struct has expected fields populated
func TestConfigRoutesToRoutes_RouteStructure(t *testing.T) {
	_, ipNet, _ := net.ParseCIDR("10.0.0.0/8")

	cfg := &config.Config{
		TUN: config.TUNConfig{Name: "tun-verify"},
		Routes: []config.RouteConfig{
			{Destination: "10.0.0.0/8", Comment: "Verify structure", Enabled: true},
		},
	}

	routes, err := configRoutesToRoutes(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	r := routes[0]

	// Verify all fields
	if r.Destination.String() != ipNet.String() {
		t.Errorf("Destination = %s, want %s", r.Destination.String(), ipNet.String())
	}
	if r.Interface != "tun-verify" {
		t.Errorf("Interface = %s, want tun-verify", r.Interface)
	}
	if r.Comment != "Verify structure" {
		t.Errorf("Comment = %s, want 'Verify structure'", r.Comment)
	}
	if !r.Enabled {
		t.Error("Enabled = false, want true")
	}
}
