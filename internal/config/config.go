package config

import (
	"fmt"
	"net"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the main configuration structure
type Config struct {
	Daemon  DaemonConfig  `yaml:"daemon"`
	TUN     TUNConfig     `yaml:"tun"`
	SOCKS5  SOCKS5Config  `yaml:"socks5"`
	Routes  []RouteConfig `yaml:"routes"`
	NAT     NATConfig     `yaml:"nat"`
	Logging LoggingConfig `yaml:"logging"`
}

// DaemonConfig holds daemon-specific configuration
type DaemonConfig struct {
	PIDFile    string `yaml:"pid_file"`
	SocketPath string `yaml:"socket_path"`
	HealthPort int    `yaml:"health_port"`
}

// TUNConfig holds TUN interface configuration
type TUNConfig struct {
	Name     string `yaml:"name"`
	MTU      int    `yaml:"mtu"`
	Address  string `yaml:"address"`   // IPv4 CIDR, e.g., "10.200.200.1/24"
	Address6 string `yaml:"address6"`  // IPv6 CIDR, e.g., "fd00:200::1/64"
}

// SOCKS5Config holds SOCKS5 proxy configuration
type SOCKS5Config struct {
	Server    string        `yaml:"server"`
	Username  string        `yaml:"username"`
	Password  string        `yaml:"password"`
	Timeout   time.Duration `yaml:"timeout"`
	KeepAlive time.Duration `yaml:"keepalive"`
}

// RouteConfig holds a single route configuration
type RouteConfig struct {
	Destination string `yaml:"destination"`
	Comment     string `yaml:"comment"`
	Enabled     bool   `yaml:"enabled"`
}

// NATConfig holds NAT table configuration
type NATConfig struct {
	TableSize   int           `yaml:"table_size"`
	TCPTimeout  time.Duration `yaml:"tcp_timeout"`
	UDPTimeout  time.Duration `yaml:"udp_timeout"`
	GCInterval  time.Duration `yaml:"gc_interval"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level      string `yaml:"level"`
	Format     string `yaml:"format"`
	Output     string `yaml:"output"`
	MaxSize    int    `yaml:"max_size"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAge     int    `yaml:"max_age"`
}

// Load reads configuration from a YAML file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	cfg.setDefaults()
	return cfg, nil
}

// setDefaults applies default values to unset fields
func (c *Config) setDefaults() {
	// Daemon defaults
	if c.Daemon.PIDFile == "" {
		c.Daemon.PIDFile = "/var/run/mutiauk.pid"
	}
	if c.Daemon.SocketPath == "" {
		c.Daemon.SocketPath = "/var/run/mutiauk.sock"
	}

	// TUN defaults
	if c.TUN.Name == "" {
		c.TUN.Name = "tun0"
	}
	if c.TUN.MTU == 0 {
		c.TUN.MTU = 1400
	}
	if c.TUN.Address == "" {
		c.TUN.Address = "10.200.200.1/24"
	}

	// SOCKS5 defaults
	if c.SOCKS5.Server == "" {
		c.SOCKS5.Server = "127.0.0.1:1080"
	}
	if c.SOCKS5.Timeout == 0 {
		c.SOCKS5.Timeout = 30 * time.Second
	}
	if c.SOCKS5.KeepAlive == 0 {
		c.SOCKS5.KeepAlive = 60 * time.Second
	}

	// NAT defaults
	if c.NAT.TableSize == 0 {
		c.NAT.TableSize = 65536
	}
	if c.NAT.TCPTimeout == 0 {
		c.NAT.TCPTimeout = time.Hour
	}
	if c.NAT.UDPTimeout == 0 {
		c.NAT.UDPTimeout = 5 * time.Minute
	}
	if c.NAT.GCInterval == 0 {
		c.NAT.GCInterval = time.Minute
	}

	// Logging defaults
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}

	// Set default enabled state for routes
	for i := range c.Routes {
		// Routes are enabled by default if not explicitly set
		// YAML will set Enabled to false if not present, so we handle this differently
		_ = i // suppress unused variable warning
	}
}

// Validate checks the configuration for errors
func (c *Config) Validate() error {
	// Validate TUN config
	if c.TUN.Name == "" {
		return fmt.Errorf("tun.name is required")
	}
	if c.TUN.MTU < 576 || c.TUN.MTU > 65535 {
		return fmt.Errorf("tun.mtu must be between 576 and 65535")
	}

	// Validate TUN IPv4 address
	if c.TUN.Address != "" {
		ip, ipNet, err := net.ParseCIDR(c.TUN.Address)
		if err != nil {
			return fmt.Errorf("invalid tun.address: %w", err)
		}
		if ip.To4() == nil {
			return fmt.Errorf("tun.address must be an IPv4 address")
		}
		_ = ipNet // used for validation
	}

	// Validate TUN IPv6 address
	if c.TUN.Address6 != "" {
		ip, ipNet, err := net.ParseCIDR(c.TUN.Address6)
		if err != nil {
			return fmt.Errorf("invalid tun.address6: %w", err)
		}
		if ip.To4() != nil {
			return fmt.Errorf("tun.address6 must be an IPv6 address")
		}
		_ = ipNet // used for validation
	}

	// Validate SOCKS5 config
	if c.SOCKS5.Server == "" {
		return fmt.Errorf("socks5.server is required")
	}
	host, port, err := net.SplitHostPort(c.SOCKS5.Server)
	if err != nil {
		return fmt.Errorf("invalid socks5.server format: %w", err)
	}
	if host == "" || port == "" {
		return fmt.Errorf("socks5.server must include host and port")
	}

	// Validate routes
	for i, r := range c.Routes {
		if r.Destination == "" {
			return fmt.Errorf("routes[%d].destination is required", i)
		}
		_, _, err := net.ParseCIDR(r.Destination)
		if err != nil {
			return fmt.Errorf("invalid routes[%d].destination: %w", i, err)
		}
	}

	return nil
}

// GetTUNIPv4 returns the parsed TUN IPv4 address and network
func (c *Config) GetTUNIPv4() (net.IP, *net.IPNet, error) {
	if c.TUN.Address == "" {
		return nil, nil, nil
	}
	return net.ParseCIDR(c.TUN.Address)
}

// GetTUNIPv6 returns the parsed TUN IPv6 address and network
func (c *Config) GetTUNIPv6() (net.IP, *net.IPNet, error) {
	if c.TUN.Address6 == "" {
		return nil, nil, nil
	}
	return net.ParseCIDR(c.TUN.Address6)
}

// HasAuth returns true if SOCKS5 authentication is configured
func (c *Config) HasAuth() bool {
	return c.SOCKS5.Username != "" && c.SOCKS5.Password != ""
}
