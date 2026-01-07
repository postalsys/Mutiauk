// Package wizard provides an interactive setup wizard for Mutiauk.
package wizard

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/postalsys/mutiauk/internal/config"
	"github.com/postalsys/mutiauk/internal/service"
	"github.com/postalsys/mutiauk/internal/wizard/prompt"
	"gopkg.in/yaml.v3"
)

// Wizard handles the interactive setup process.
type Wizard struct {
	configPath string
}

// New creates a new setup wizard.
func New() *Wizard {
	return &Wizard{}
}

// Run executes the setup wizard and returns the generated config path.
func (w *Wizard) Run() (string, error) {
	prompt.PrintBanner("Mutiauk Setup Wizard", "TUN-based SOCKS5 Proxy Agent")

	fmt.Println("This wizard will help you configure Mutiauk.")
	fmt.Println("Press Enter to accept default values shown in [brackets].")
	fmt.Println()

	// Step 1: Config file location
	configPath, err := w.askConfigPath()
	if err != nil {
		return "", err
	}
	w.configPath = configPath

	// Step 2: TUN interface configuration
	tunConfig, err := w.askTUNConfig()
	if err != nil {
		return "", err
	}

	// Step 3: SOCKS5 proxy configuration
	socks5Config, err := w.askSOCKS5Config()
	if err != nil {
		return "", err
	}

	// Step 4: Routes configuration
	routes, err := w.askRoutes()
	if err != nil {
		return "", err
	}

	// Build and write config
	cfg := w.buildConfig(tunConfig, socks5Config, routes)
	if err := w.writeConfig(cfg, configPath); err != nil {
		return "", err
	}

	prompt.PrintSuccess(fmt.Sprintf("Configuration saved to %s", configPath))

	// Print summary
	w.printSummary(cfg, configPath)

	// Step 5: Service installation
	if err := w.askServiceInstall(configPath); err != nil {
		return "", err
	}

	return configPath, nil
}

func (w *Wizard) askConfigPath() (string, error) {
	prompt.PrintHeader("Configuration File", "Where should the configuration file be saved?")

	defaultPath := "/etc/mutiauk/config.yaml"

	configPath, err := prompt.ReadLineValidated("Config file path", defaultPath, func(s string) error {
		if s == "" {
			return fmt.Errorf("config path is required")
		}
		if !strings.HasSuffix(s, ".yaml") && !strings.HasSuffix(s, ".yml") {
			return fmt.Errorf("config file should have .yaml or .yml extension")
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	// Check if directory exists, offer to create it
	dir := filepath.Dir(configPath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		create, err := prompt.Confirm(fmt.Sprintf("Directory %s does not exist. Create it?", dir), true)
		if err != nil {
			return "", err
		}
		if create {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return "", fmt.Errorf("failed to create directory: %w", err)
			}
			prompt.PrintSuccess(fmt.Sprintf("Created directory %s", dir))
		}
	}

	// Check if file exists, warn about overwrite
	if _, err := os.Stat(configPath); err == nil {
		overwrite, err := prompt.Confirm("Config file already exists. Overwrite?", false)
		if err != nil {
			return "", err
		}
		if !overwrite {
			return "", fmt.Errorf("setup cancelled")
		}
	}

	return configPath, nil
}

func (w *Wizard) askTUNConfig() (config.TUNConfig, error) {
	prompt.PrintHeader("TUN Interface", "Configure the virtual network interface.")

	cfg := config.TUNConfig{}

	// Interface name
	name, err := prompt.ReadLineValidated("Interface name", "tun0", func(s string) error {
		if s == "" {
			return fmt.Errorf("interface name is required")
		}
		if len(s) > 15 {
			return fmt.Errorf("interface name must be 15 characters or less")
		}
		return nil
	})
	if err != nil {
		return cfg, err
	}
	cfg.Name = name

	// MTU
	mtuStr, err := prompt.ReadLineValidated("MTU", "1400", func(s string) error {
		var mtu int
		if _, err := fmt.Sscanf(s, "%d", &mtu); err != nil {
			return fmt.Errorf("invalid MTU: must be a number")
		}
		if mtu < 576 || mtu > 65535 {
			return fmt.Errorf("MTU must be between 576 and 65535")
		}
		return nil
	})
	if err != nil {
		return cfg, err
	}
	fmt.Sscanf(mtuStr, "%d", &cfg.MTU)

	// IPv4 address
	address, err := prompt.ReadLineValidated("IPv4 address (CIDR)", "10.200.200.1/24", func(s string) error {
		if s == "" {
			return fmt.Errorf("IPv4 address is required")
		}
		ip, _, err := net.ParseCIDR(s)
		if err != nil {
			return fmt.Errorf("invalid CIDR format: %v", err)
		}
		if ip.To4() == nil {
			return fmt.Errorf("must be an IPv4 address")
		}
		return nil
	})
	if err != nil {
		return cfg, err
	}
	cfg.Address = address

	// Optional IPv6
	useIPv6, err := prompt.Confirm("Configure IPv6 address?", false)
	if err != nil {
		return cfg, err
	}
	if useIPv6 {
		address6, err := prompt.ReadLineValidated("IPv6 address (CIDR)", "fd00:200::1/64", func(s string) error {
			ip, _, err := net.ParseCIDR(s)
			if err != nil {
				return fmt.Errorf("invalid CIDR format: %v", err)
			}
			if ip.To4() != nil {
				return fmt.Errorf("must be an IPv6 address")
			}
			return nil
		})
		if err != nil {
			return cfg, err
		}
		cfg.Address6 = address6
	}

	return cfg, nil
}

func (w *Wizard) askSOCKS5Config() (config.SOCKS5Config, error) {
	prompt.PrintHeader("SOCKS5 Proxy", "Configure the upstream SOCKS5 proxy server.")

	cfg := config.SOCKS5Config{}

	// Server address
	server, err := prompt.ReadLineValidated("SOCKS5 server address", "127.0.0.1:1080", func(s string) error {
		if s == "" {
			return fmt.Errorf("server address is required")
		}
		host, port, err := net.SplitHostPort(s)
		if err != nil {
			return fmt.Errorf("invalid address format (use host:port)")
		}
		if host == "" || port == "" {
			return fmt.Errorf("both host and port are required")
		}
		return nil
	})
	if err != nil {
		return cfg, err
	}
	cfg.Server = server

	// Authentication
	useAuth, err := prompt.Confirm("Does the SOCKS5 server require authentication?", false)
	if err != nil {
		return cfg, err
	}
	if useAuth {
		username, err := prompt.ReadLineValidated("Username", "", func(s string) error {
			if s == "" {
				return fmt.Errorf("username is required")
			}
			return nil
		})
		if err != nil {
			return cfg, err
		}
		cfg.Username = username

		password, err := prompt.ReadPassword("Password")
		if err != nil {
			return cfg, err
		}
		cfg.Password = password
	}

	return cfg, nil
}

func (w *Wizard) askRoutes() ([]config.RouteConfig, error) {
	prompt.PrintHeader("Routes", "Configure which traffic to route through the SOCKS5 proxy.")

	fmt.Println("Enter destination CIDRs to route through the proxy.")
	fmt.Println("Common examples:")
	fmt.Println("  10.0.0.0/8       - Private class A networks")
	fmt.Println("  192.168.0.0/16   - Private class C networks")
	fmt.Println("  172.16.0.0/12    - Private class B networks")
	fmt.Println()

	var routes []config.RouteConfig

	// Offer common presets
	usePresets, err := prompt.Confirm("Use common private network routes (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)?", true)
	if err != nil {
		return nil, err
	}

	if usePresets {
		routes = append(routes,
			config.RouteConfig{Destination: "10.0.0.0/8", Comment: "Private class A", Enabled: true},
			config.RouteConfig{Destination: "172.16.0.0/12", Comment: "Private class B", Enabled: true},
			config.RouteConfig{Destination: "192.168.0.0/16", Comment: "Private class C", Enabled: true},
		)
		prompt.PrintSuccess("Added standard private network routes")
	}

	// Add custom routes
	addMore := true
	for addMore {
		addCustom, err := prompt.Confirm("Add a custom route?", !usePresets && len(routes) == 0)
		if err != nil {
			return nil, err
		}
		if !addCustom {
			break
		}

		cidr, err := prompt.ReadLineValidated("Destination CIDR", "", func(s string) error {
			if s == "" {
				return fmt.Errorf("CIDR is required")
			}
			_, _, err := net.ParseCIDR(s)
			if err != nil {
				return fmt.Errorf("invalid CIDR format: %v", err)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}

		comment, err := prompt.ReadLine("Comment (optional)", "")
		if err != nil {
			return nil, err
		}

		routes = append(routes, config.RouteConfig{
			Destination: cidr,
			Comment:     comment,
			Enabled:     true,
		})

		prompt.PrintSuccess(fmt.Sprintf("Added route: %s", cidr))

		addMore, err = prompt.Confirm("Add another route?", false)
		if err != nil {
			return nil, err
		}
	}

	if len(routes) == 0 {
		prompt.PrintWarning("No routes configured. Mutiauk will not forward any traffic.")
	}

	return routes, nil
}

func (w *Wizard) buildConfig(tunCfg config.TUNConfig, socks5Cfg config.SOCKS5Config, routes []config.RouteConfig) *config.Config {
	cfg := &config.Config{
		Daemon: config.DaemonConfig{
			PIDFile:    "/var/run/mutiauk.pid",
			SocketPath: "/var/run/mutiauk.sock",
		},
		TUN:    tunCfg,
		SOCKS5: socks5Cfg,
		Routes: routes,
		NAT: config.NATConfig{
			TableSize:  65536,
			TCPTimeout: 3600000000000,  // 1 hour in nanoseconds
			UDPTimeout: 300000000000,   // 5 minutes in nanoseconds
			GCInterval: 60000000000,    // 1 minute in nanoseconds
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}

	// Set defaults
	if cfg.SOCKS5.Timeout == 0 {
		cfg.SOCKS5.Timeout = 30000000000 // 30 seconds
	}
	if cfg.SOCKS5.KeepAlive == 0 {
		cfg.SOCKS5.KeepAlive = 60000000000 // 60 seconds
	}

	return cfg
}

func (w *Wizard) writeConfig(cfg *config.Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	header := `# Mutiauk Configuration
# Generated by setup wizard
#
# Documentation: https://mutimetroo.com/mutiauk/
#
# To start Mutiauk:
#   sudo mutiauk daemon start -c ` + path + `
#
# To install as a service:
#   sudo mutiauk service install -c ` + path + `
#

`

	content := header + string(data)

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func (w *Wizard) printSummary(cfg *config.Config, configPath string) {
	prompt.PrintHeader("Configuration Summary", "")

	fmt.Printf("  Config file:     %s\n", configPath)
	fmt.Printf("  TUN interface:   %s\n", cfg.TUN.Name)
	fmt.Printf("  TUN address:     %s\n", cfg.TUN.Address)
	if cfg.TUN.Address6 != "" {
		fmt.Printf("  TUN IPv6:        %s\n", cfg.TUN.Address6)
	}
	fmt.Printf("  MTU:             %d\n", cfg.TUN.MTU)
	fmt.Printf("  SOCKS5 server:   %s\n", cfg.SOCKS5.Server)
	if cfg.SOCKS5.Username != "" {
		fmt.Printf("  SOCKS5 auth:     yes (user: %s)\n", cfg.SOCKS5.Username)
	}
	fmt.Printf("  Routes:          %d configured\n", len(cfg.Routes))
	for _, r := range cfg.Routes {
		comment := ""
		if r.Comment != "" {
			comment = fmt.Sprintf(" (%s)", r.Comment)
		}
		fmt.Printf("                   - %s%s\n", r.Destination, comment)
	}

	fmt.Println()
	fmt.Println("To start Mutiauk manually:")
	fmt.Printf("  sudo mutiauk daemon start -c %s\n", configPath)
	fmt.Println()
}

func (w *Wizard) askServiceInstall(configPath string) error {
	prompt.PrintHeader("Service Installation", "Install Mutiauk as a system service.")

	if runtime.GOOS != "linux" {
		prompt.PrintInfo("Service installation is only available on Linux.")
		return nil
	}

	if !service.IsRoot() {
		prompt.PrintWarning("Not running as root. Service installation requires root privileges.")
		fmt.Println()
		fmt.Println("To install as a service later, run:")
		fmt.Printf("  sudo mutiauk service install -c %s\n", configPath)
		return nil
	}

	install, err := prompt.Confirm("Install Mutiauk as a systemd service?", true)
	if err != nil {
		return err
	}

	if !install {
		fmt.Println()
		fmt.Println("To install as a service later, run:")
		fmt.Printf("  sudo mutiauk service install -c %s\n", configPath)
		return nil
	}

	// Check if already installed
	if service.IsInstalled("mutiauk") {
		prompt.PrintWarning("Service is already installed. Uninstall first to reinstall.")
		return nil
	}

	// Install the service
	fmt.Println()
	prompt.PrintInfo("Installing systemd service...")

	cfg := service.DefaultConfig(configPath)
	if err := service.Install(cfg); err != nil {
		prompt.PrintError(fmt.Sprintf("Failed to install service: %v", err))
		fmt.Println()
		fmt.Println("You can try installing manually:")
		fmt.Printf("  sudo mutiauk service install -c %s\n", configPath)
		return nil // Don't fail the wizard for service install failure
	}

	prompt.PrintSuccess("Service installed and started!")
	fmt.Println()
	fmt.Println("Service management commands:")
	fmt.Println("  sudo systemctl status mutiauk    # Check status")
	fmt.Println("  sudo systemctl stop mutiauk      # Stop service")
	fmt.Println("  sudo systemctl restart mutiauk   # Restart service")
	fmt.Println("  sudo journalctl -u mutiauk -f    # View logs")

	return nil
}
