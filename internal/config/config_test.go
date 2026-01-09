package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestLoad(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		content := `
tun:
  name: tun0
  mtu: 1400
  address: 10.200.200.1/24

socks5:
  server: 127.0.0.1:1080
  timeout: 30s

routes:
  - destination: 10.0.0.0/8
    comment: "Private network"
    enabled: true
`
		path := writeTempConfig(t, content)
		defer os.Remove(path)

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if cfg.TUN.Name != "tun0" {
			t.Errorf("TUN.Name = %v, want tun0", cfg.TUN.Name)
		}
		if cfg.TUN.MTU != 1400 {
			t.Errorf("TUN.MTU = %v, want 1400", cfg.TUN.MTU)
		}
		if cfg.SOCKS5.Server != "127.0.0.1:1080" {
			t.Errorf("SOCKS5.Server = %v", cfg.SOCKS5.Server)
		}
		if len(cfg.Routes) != 1 {
			t.Errorf("len(Routes) = %v, want 1", len(cfg.Routes))
		}
	})

	t.Run("file not found", func(t *testing.T) {
		_, err := Load("/nonexistent/path/config.yaml")
		if err == nil {
			t.Error("expected error for nonexistent file")
		}
	})

	t.Run("invalid YAML", func(t *testing.T) {
		content := `
tun:
  name: [invalid yaml
  mtu: not a number
`
		path := writeTempConfig(t, content)
		defer os.Remove(path)

		_, err := Load(path)
		if err == nil {
			t.Error("expected error for invalid YAML")
		}
	})
}

func TestDefaults(t *testing.T) {
	content := `
socks5:
  server: proxy:1080
`
	path := writeTempConfig(t, content)
	defer os.Remove(path)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Check daemon defaults
	if cfg.Daemon.PIDFile != "/var/run/mutiauk.pid" {
		t.Errorf("Daemon.PIDFile = %v", cfg.Daemon.PIDFile)
	}
	if cfg.Daemon.SocketPath != "/var/run/mutiauk.sock" {
		t.Errorf("Daemon.SocketPath = %v", cfg.Daemon.SocketPath)
	}

	// Check TUN defaults
	if cfg.TUN.Name != "tun0" {
		t.Errorf("TUN.Name = %v, want tun0", cfg.TUN.Name)
	}
	if cfg.TUN.MTU != 1400 {
		t.Errorf("TUN.MTU = %v, want 1400", cfg.TUN.MTU)
	}
	if cfg.TUN.Address != "10.200.200.1/24" {
		t.Errorf("TUN.Address = %v", cfg.TUN.Address)
	}

	// Check SOCKS5 defaults
	if cfg.SOCKS5.Timeout != 30*time.Second {
		t.Errorf("SOCKS5.Timeout = %v", cfg.SOCKS5.Timeout)
	}
	if cfg.SOCKS5.KeepAlive != 60*time.Second {
		t.Errorf("SOCKS5.KeepAlive = %v", cfg.SOCKS5.KeepAlive)
	}

	// Check NAT defaults
	if cfg.NAT.TableSize != 65536 {
		t.Errorf("NAT.TableSize = %v", cfg.NAT.TableSize)
	}
	if cfg.NAT.TCPTimeout != time.Hour {
		t.Errorf("NAT.TCPTimeout = %v", cfg.NAT.TCPTimeout)
	}
	if cfg.NAT.UDPTimeout != 5*time.Minute {
		t.Errorf("NAT.UDPTimeout = %v", cfg.NAT.UDPTimeout)
	}
	if cfg.NAT.GCInterval != time.Minute {
		t.Errorf("NAT.GCInterval = %v", cfg.NAT.GCInterval)
	}

	// Check Logging defaults
	if cfg.Logging.Level != "info" {
		t.Errorf("Logging.Level = %v", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "json" {
		t.Errorf("Logging.Format = %v", cfg.Logging.Format)
	}

	// Check AutoRoutes defaults
	if cfg.AutoRoutes.PollInterval != 30*time.Second {
		t.Errorf("AutoRoutes.PollInterval = %v", cfg.AutoRoutes.PollInterval)
	}
	if cfg.AutoRoutes.Timeout != 10*time.Second {
		t.Errorf("AutoRoutes.Timeout = %v", cfg.AutoRoutes.Timeout)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid minimal config",
			config: `
tun:
  name: tun0
  mtu: 1400
  address: 10.200.200.1/24
socks5:
  server: 127.0.0.1:1080
`,
			wantErr: false,
		},
		// Note: empty tun.name gets default "tun0", so we can't test "missing TUN name"
		// The validation would only fail if defaults weren't applied
		{
			name: "MTU too low",
			config: `
tun:
  name: tun0
  mtu: 100
socks5:
  server: 127.0.0.1:1080
`,
			wantErr: true,
			errMsg:  "tun.mtu must be between 576 and 65535",
		},
		{
			name: "MTU too high",
			config: `
tun:
  name: tun0
  mtu: 100000
socks5:
  server: 127.0.0.1:1080
`,
			wantErr: true,
			errMsg:  "tun.mtu must be between 576 and 65535",
		},
		{
			name: "invalid TUN address",
			config: `
tun:
  name: tun0
  mtu: 1400
  address: not-a-cidr
socks5:
  server: 127.0.0.1:1080
`,
			wantErr: true,
			errMsg:  "invalid tun.address",
		},
		{
			name: "IPv6 in TUN address field",
			config: `
tun:
  name: tun0
  mtu: 1400
  address: "fd00::1/64"
socks5:
  server: 127.0.0.1:1080
`,
			wantErr: true,
			errMsg:  "tun.address must be an IPv4 address",
		},
		{
			name: "IPv4 in TUN address6 field",
			config: `
tun:
  name: tun0
  mtu: 1400
  address: 10.200.200.1/24
  address6: "10.0.0.1/24"
socks5:
  server: 127.0.0.1:1080
`,
			wantErr: true,
			errMsg:  "tun.address6 must be an IPv6 address",
		},
		// Note: empty socks5.server gets default "127.0.0.1:1080", so we can't test "missing SOCKS5 server"
		// The validation would only fail if defaults weren't applied
		{
			name: "invalid SOCKS5 server format",
			config: `
tun:
  name: tun0
  mtu: 1400
socks5:
  server: "invalid-no-port"
`,
			wantErr: true,
			errMsg:  "invalid socks5.server format",
		},
		{
			name: "invalid route destination",
			config: `
tun:
  name: tun0
  mtu: 1400
socks5:
  server: 127.0.0.1:1080
routes:
  - destination: not-a-cidr
    enabled: true
`,
			wantErr: true,
			errMsg:  "invalid routes[0].destination",
		},
		{
			name: "empty route destination",
			config: `
tun:
  name: tun0
  mtu: 1400
socks5:
  server: 127.0.0.1:1080
routes:
  - destination: ""
    enabled: true
`,
			wantErr: true,
			errMsg:  "routes[0].destination is required",
		},
		{
			name: "autoroutes enabled without URL",
			config: `
tun:
  name: tun0
  mtu: 1400
socks5:
  server: 127.0.0.1:1080
autoroutes:
  enabled: true
  url: ""
`,
			wantErr: true,
			errMsg:  "autoroutes.url is required when enabled",
		},
		{
			name: "autoroutes poll interval too short",
			config: `
tun:
  name: tun0
  mtu: 1400
socks5:
  server: 127.0.0.1:1080
autoroutes:
  enabled: true
  url: http://localhost:8080
  poll_interval: 100ms
`,
			wantErr: true,
			errMsg:  "autoroutes.poll_interval must be at least 1s",
		},
		{
			name: "autoroutes timeout too short",
			config: `
tun:
  name: tun0
  mtu: 1400
socks5:
  server: 127.0.0.1:1080
autoroutes:
  enabled: true
  url: http://localhost:8080
  poll_interval: 30s
  timeout: 100ms
`,
			wantErr: true,
			errMsg:  "autoroutes.timeout must be at least 1s",
		},
		{
			name: "valid config with autoroutes",
			config: `
tun:
  name: tun0
  mtu: 1400
socks5:
  server: 127.0.0.1:1080
autoroutes:
  enabled: true
  url: http://localhost:8080
  poll_interval: 30s
  timeout: 10s
`,
			wantErr: false,
		},
		{
			name: "valid config with IPv6",
			config: `
tun:
  name: tun0
  mtu: 1400
  address: 10.200.200.1/24
  address6: "fd00:200::1/64"
socks5:
  server: 127.0.0.1:1080
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempConfig(t, tt.config)
			defer os.Remove(path)

			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			err = cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errMsg != "" {
				if err == nil || !containsString(err.Error(), tt.errMsg) {
					t.Errorf("error = %v, want to contain %q", err, tt.errMsg)
				}
			}
		})
	}
}

func TestGetTUNIPv4(t *testing.T) {
	t.Run("with address", func(t *testing.T) {
		cfg := &Config{
			TUN: TUNConfig{Address: "10.200.200.1/24"},
		}

		ip, ipNet, err := cfg.GetTUNIPv4()
		if err != nil {
			t.Fatalf("GetTUNIPv4() error = %v", err)
		}
		if ip.String() != "10.200.200.1" {
			t.Errorf("IP = %v", ip)
		}
		if ipNet.String() != "10.200.200.0/24" {
			t.Errorf("IPNet = %v", ipNet)
		}
	})

	t.Run("empty address", func(t *testing.T) {
		cfg := &Config{
			TUN: TUNConfig{Address: ""},
		}

		ip, ipNet, err := cfg.GetTUNIPv4()
		if err != nil {
			t.Fatalf("GetTUNIPv4() error = %v", err)
		}
		if ip != nil || ipNet != nil {
			t.Error("expected nil for empty address")
		}
	})
}

func TestGetTUNIPv6(t *testing.T) {
	t.Run("with address", func(t *testing.T) {
		cfg := &Config{
			TUN: TUNConfig{Address6: "fd00:200::1/64"},
		}

		ip, ipNet, err := cfg.GetTUNIPv6()
		if err != nil {
			t.Fatalf("GetTUNIPv6() error = %v", err)
		}
		if ip.String() != "fd00:200::1" {
			t.Errorf("IP = %v", ip)
		}
		if ipNet.String() != "fd00:200::/64" {
			t.Errorf("IPNet = %v", ipNet)
		}
	})

	t.Run("empty address", func(t *testing.T) {
		cfg := &Config{
			TUN: TUNConfig{Address6: ""},
		}

		ip, ipNet, err := cfg.GetTUNIPv6()
		if err != nil {
			t.Fatalf("GetTUNIPv6() error = %v", err)
		}
		if ip != nil || ipNet != nil {
			t.Error("expected nil for empty address")
		}
	})
}

func TestHasAuth(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		want     bool
	}{
		{"both set", "user", "pass", true},
		{"username only", "user", "", false},
		{"password only", "", "pass", false},
		{"neither set", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				SOCKS5: SOCKS5Config{
					Username: tt.username,
					Password: tt.password,
				},
			}
			if got := cfg.HasAuth(); got != tt.want {
				t.Errorf("HasAuth() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSave(t *testing.T) {
	cfg := &Config{
		TUN: TUNConfig{
			Name:    "tun0",
			MTU:     1400,
			Address: "10.200.200.1/24",
		},
		SOCKS5: SOCKS5Config{
			Server:  "127.0.0.1:1080",
			Timeout: 30 * time.Second,
		},
		Routes: []RouteConfig{
			{
				Destination: "10.0.0.0/8",
				Comment:     "Private network",
				Enabled:     true,
			},
		},
	}

	tmpDir, err := os.MkdirTemp("", "config-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	path := filepath.Join(tmpDir, "config.yaml")

	// Save config
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("config file not created")
	}

	// Load and verify
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.TUN.Name != cfg.TUN.Name {
		t.Errorf("TUN.Name = %v, want %v", loaded.TUN.Name, cfg.TUN.Name)
	}
	if loaded.SOCKS5.Server != cfg.SOCKS5.Server {
		t.Errorf("SOCKS5.Server = %v, want %v", loaded.SOCKS5.Server, cfg.SOCKS5.Server)
	}
	if len(loaded.Routes) != len(cfg.Routes) {
		t.Errorf("len(Routes) = %v, want %v", len(loaded.Routes), len(cfg.Routes))
	}
	if loaded.Routes[0].Destination != cfg.Routes[0].Destination {
		t.Errorf("Routes[0].Destination = %v", loaded.Routes[0].Destination)
	}
}

func TestSaveInvalidPath(t *testing.T) {
	cfg := &Config{}
	err := cfg.Save("/nonexistent/directory/config.yaml")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

// --- Watcher Tests ---

func TestNewWatcher(t *testing.T) {
	content := `
tun:
  name: tun0
  mtu: 1400
socks5:
  server: 127.0.0.1:1080
`
	path := writeTempConfig(t, content)
	defer os.Remove(path)

	logger := zap.NewNop()

	watcher, err := NewWatcher(path, logger, nil)
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	defer watcher.Close()

	cfg := watcher.Get()
	if cfg == nil {
		t.Error("Get() returned nil")
	}
	if cfg.TUN.Name != "tun0" {
		t.Errorf("TUN.Name = %v", cfg.TUN.Name)
	}
}

func TestNewWatcherInvalidConfig(t *testing.T) {
	_, err := NewWatcher("/nonexistent/config.yaml", zap.NewNop(), nil)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestWatcherGet(t *testing.T) {
	content := `
tun:
  name: test-tun
  mtu: 1500
socks5:
  server: proxy:1080
`
	path := writeTempConfig(t, content)
	defer os.Remove(path)

	watcher, err := NewWatcher(path, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	defer watcher.Close()

	// Get should be thread-safe
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			cfg := watcher.Get()
			if cfg.TUN.Name != "test-tun" {
				t.Errorf("TUN.Name = %v", cfg.TUN.Name)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestWatcherFileChange(t *testing.T) {
	content := `
tun:
  name: tun0
  mtu: 1400
socks5:
  server: 127.0.0.1:1080
`
	path := writeTempConfig(t, content)
	defer os.Remove(path)

	reloadCalled := make(chan bool, 1)
	onChangeFn := func(cfg *Config) error {
		reloadCalled <- true
		return nil
	}

	watcher, err := NewWatcher(path, zap.NewNop(), onChangeFn)
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	defer watcher.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go watcher.Watch(ctx)

	// Wait a bit for watcher to start
	time.Sleep(100 * time.Millisecond)

	// Modify the file
	newContent := `
tun:
  name: tun1
  mtu: 1500
socks5:
  server: 127.0.0.1:1080
`
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Wait for reload with timeout
	select {
	case <-reloadCalled:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for reload callback")
	}

	// Verify the config was updated
	cfg := watcher.Get()
	if cfg.TUN.Name != "tun1" {
		t.Errorf("TUN.Name = %v, want tun1", cfg.TUN.Name)
	}
}

func TestWatcherClose(t *testing.T) {
	content := `
tun:
  name: tun0
  mtu: 1400
socks5:
  server: 127.0.0.1:1080
`
	path := writeTempConfig(t, content)
	defer os.Remove(path)

	watcher, err := NewWatcher(path, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}

	if err := watcher.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

// --- Helper Functions ---

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	if _, err := tmpFile.WriteString(content); err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("failed to write temp file: %v", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("failed to close temp file: %v", err)
	}

	return tmpFile.Name()
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
