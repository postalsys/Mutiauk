// Package wizard provides an interactive setup wizard for Mutiauk.
package wizard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/postalsys/mutiauk/internal/config"
	"github.com/postalsys/mutiauk/internal/wizard/prompt"
	"gopkg.in/yaml.v3"
)

// Wizard handles the interactive setup process.
type Wizard struct {
	configPath         string
	dashboardVerifier  *DashboardVerifier
	serviceInstaller   *ServiceInstaller
}

// New creates a new setup wizard.
func New() *Wizard {
	return &Wizard{
		dashboardVerifier: NewDashboardVerifier(),
	}
}

// Run executes the setup wizard and returns the generated config path.
func (w *Wizard) Run() (string, error) {
	prompt.PrintBanner("Mutiauk Setup Wizard", "TUN-based SOCKS5 Proxy Agent")

	fmt.Println("This wizard will help you configure Mutiauk.")
	fmt.Println("Press Enter to accept default values shown in [brackets].")
	fmt.Println()

	configPath, err := w.askConfigPath()
	if err != nil {
		return "", err
	}
	w.configPath = configPath

	tunConfig, err := w.askTUNConfig()
	if err != nil {
		return "", err
	}

	socks5Config, err := w.askSOCKS5Config()
	if err != nil {
		return "", err
	}

	autoRoutesConfig, useAutoRoutes, err := w.askAutoRoutes()
	if err != nil {
		return "", err
	}

	var routes []config.RouteConfig
	if !useAutoRoutes {
		routes, err = w.askRoutes()
		if err != nil {
			return "", err
		}
	}

	cfg := w.buildConfig(tunConfig, socks5Config, autoRoutesConfig, routes)
	if err := w.writeConfig(cfg, configPath); err != nil {
		return "", err
	}

	prompt.PrintSuccess(fmt.Sprintf("Configuration saved to %s", configPath))
	w.printSummary(cfg, configPath)

	w.serviceInstaller = NewServiceInstaller(configPath)
	if err := w.serviceInstaller.Run(); err != nil {
		return "", err
	}

	return configPath, nil
}

func (w *Wizard) askConfigPath() (string, error) {
	prompt.PrintHeader("Configuration File", "Where should the configuration file be saved?")

	defaultPath := "/etc/mutiauk/config.yaml"

	configPath, err := prompt.ReadLineValidated("Config file path", defaultPath,
		Chain(Required("config path"), YAMLExtension()))
	if err != nil {
		return "", err
	}

	if err := w.ensureDirectoryExists(configPath); err != nil {
		return "", err
	}

	if err := w.checkFileOverwrite(configPath); err != nil {
		return "", err
	}

	return configPath, nil
}

func (w *Wizard) ensureDirectoryExists(filePath string) error {
	dir := filepath.Dir(filePath)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		return nil
	}

	create, err := prompt.Confirm(fmt.Sprintf("Directory %s does not exist. Create it?", dir), true)
	if err != nil {
		return err
	}

	if !create {
		return nil
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	prompt.PrintSuccess(fmt.Sprintf("Created directory %s", dir))
	return nil
}

func (w *Wizard) checkFileOverwrite(filePath string) error {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil
	}

	overwrite, err := prompt.Confirm("Config file already exists. Overwrite?", false)
	if err != nil {
		return err
	}

	if !overwrite {
		return fmt.Errorf("setup cancelled")
	}

	return nil
}

func (w *Wizard) askTUNConfig() (config.TUNConfig, error) {
	prompt.PrintHeader("TUN Interface", "Configure the virtual network interface.")

	cfg := config.TUNConfig{}

	name, err := prompt.ReadLineValidated("Interface name", "tun0",
		Chain(Required("interface name"), MaxLength(15)))
	if err != nil {
		return cfg, err
	}
	cfg.Name = name

	mtuStr, err := prompt.ReadLineValidated("MTU", "1400", IntRange(576, 65535, "MTU"))
	if err != nil {
		return cfg, err
	}
	fmt.Sscanf(mtuStr, "%d", &cfg.MTU)

	address, err := prompt.ReadLineValidated("IPv4 address (CIDR)", "10.200.200.1/24",
		Chain(Required("IPv4 address"), IPv4CIDR()))
	if err != nil {
		return cfg, err
	}
	cfg.Address = address

	useIPv6, err := prompt.Confirm("Configure IPv6 address?", false)
	if err != nil {
		return cfg, err
	}

	if useIPv6 {
		address6, err := prompt.ReadLineValidated("IPv6 address (CIDR)", "fd00:200::1/64", IPv6CIDR())
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

	server, err := prompt.ReadLineValidated("SOCKS5 server address", "127.0.0.1:1080",
		Chain(Required("server address"), HostPort()))
	if err != nil {
		return cfg, err
	}
	cfg.Server = server

	useAuth, err := prompt.Confirm("Does the SOCKS5 server require authentication?", false)
	if err != nil {
		return cfg, err
	}

	if useAuth {
		username, err := prompt.ReadLineValidated("Username", "", Required("username"))
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

func (w *Wizard) askAutoRoutes() (config.AutoRoutesConfig, bool, error) {
	prompt.PrintHeader("Automatic Routes", "Fetch routes automatically from Muti Metroo dashboard.")

	cfg := config.AutoRoutesConfig{}

	fmt.Println("Autoroutes automatically fetches and maintains routes from a Muti Metroo")
	fmt.Println("dashboard. This eliminates the need to manually manage routes.")
	fmt.Println()

	useAutoRoutes, err := prompt.Confirm("Use automatic routes from Muti Metroo dashboard?", false)
	if err != nil {
		return cfg, false, err
	}

	if !useAutoRoutes {
		return cfg, false, nil
	}

	dashboardURL, err := prompt.ReadLineValidated("Muti Metroo dashboard URL", "http://localhost:3000",
		Chain(Required("URL"), HTTPOrHTTPSURL()))
	if err != nil {
		return cfg, false, err
	}

	dashboardURL = strings.TrimSuffix(dashboardURL, "/")

	useAutoRoutes, err = w.verifyDashboardAndPrompt(dashboardURL)
	if err != nil {
		return cfg, false, err
	}

	if !useAutoRoutes {
		return cfg, false, nil
	}

	pollIntervalStr, err := prompt.ReadLineValidated("Poll interval", "30s", Duration())
	if err != nil {
		return cfg, false, err
	}

	pollInterval, _ := time.ParseDuration(pollIntervalStr)

	cfg.Enabled = true
	cfg.URL = dashboardURL
	cfg.PollInterval = pollInterval
	cfg.Timeout = 10 * time.Second

	return cfg, true, nil
}

func (w *Wizard) verifyDashboardAndPrompt(dashboardURL string) (bool, error) {
	fmt.Println()
	prompt.PrintInfo("Verifying dashboard API...")

	if err := w.dashboardVerifier.Verify(dashboardURL); err == nil {
		prompt.PrintSuccess("Dashboard API is accessible")
		return true, nil
	}

	connErr := &DashboardConnectionError{BaseURL: dashboardURL, Err: nil}
	prompt.PrintError(fmt.Sprintf("Failed to connect to dashboard API"))
	fmt.Println()
	fmt.Println("The dashboard API at " + dashboardURL + "/api/dashboard is not responding.")
	fmt.Println("Possible causes:")
	for _, cause := range connErr.PossibleCauses() {
		fmt.Printf("  - %s\n", cause)
	}
	fmt.Println()

	retryChoice, err := prompt.Select("What would you like to do?", []string{
		"Retry connection",
		"Continue anyway (routes will be fetched when daemon starts)",
		"Skip autoroutes and configure manual routes",
	}, 0)
	if err != nil {
		return false, err
	}

	switch retryChoice {
	case 0:
		return w.verifyDashboardAndPrompt(dashboardURL)
	case 1:
		prompt.PrintWarning("Proceeding without verification. Ensure the dashboard is running before starting the daemon.")
		return true, nil
	default:
		return false, nil
	}
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

	usePresets, err := prompt.Confirm("Use common private network routes (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)?", true)
	if err != nil {
		return nil, err
	}

	if usePresets {
		routes = append(routes, defaultPrivateRoutes()...)
		prompt.PrintSuccess("Added standard private network routes")
	}

	routes, err = w.addCustomRoutes(routes, usePresets)
	if err != nil {
		return nil, err
	}

	if len(routes) == 0 {
		prompt.PrintWarning("No routes configured. Mutiauk will not forward any traffic.")
	}

	return routes, nil
}

func defaultPrivateRoutes() []config.RouteConfig {
	return []config.RouteConfig{
		{Destination: "10.0.0.0/8", Comment: "Private class A", Enabled: true},
		{Destination: "172.16.0.0/12", Comment: "Private class B", Enabled: true},
		{Destination: "192.168.0.0/16", Comment: "Private class C", Enabled: true},
	}
}

func (w *Wizard) addCustomRoutes(routes []config.RouteConfig, hasPresets bool) ([]config.RouteConfig, error) {
	for {
		defaultAdd := !hasPresets && len(routes) == 0
		addCustom, err := prompt.Confirm("Add a custom route?", defaultAdd)
		if err != nil {
			return nil, err
		}

		if !addCustom {
			break
		}

		route, err := w.askSingleRoute()
		if err != nil {
			return nil, err
		}

		routes = append(routes, route)
		prompt.PrintSuccess(fmt.Sprintf("Added route: %s", route.Destination))
	}

	return routes, nil
}

func (w *Wizard) askSingleRoute() (config.RouteConfig, error) {
	cidr, err := prompt.ReadLineValidated("Destination CIDR", "",
		Chain(Required("CIDR"), CIDR()))
	if err != nil {
		return config.RouteConfig{}, err
	}

	comment, err := prompt.ReadLine("Comment (optional)", "")
	if err != nil {
		return config.RouteConfig{}, err
	}

	return config.RouteConfig{
		Destination: cidr,
		Comment:     comment,
		Enabled:     true,
	}, nil
}

func (w *Wizard) buildConfig(tunCfg config.TUNConfig, socks5Cfg config.SOCKS5Config, autoRoutesCfg config.AutoRoutesConfig, routes []config.RouteConfig) *config.Config {
	cfg := &config.Config{
		Daemon: config.DaemonConfig{
			PIDFile:    "/var/run/mutiauk.pid",
			SocketPath: "/var/run/mutiauk.sock",
		},
		TUN:        tunCfg,
		SOCKS5:     socks5Cfg,
		AutoRoutes: autoRoutesCfg,
		Routes:     routes,
		NAT: config.NATConfig{
			TableSize:  65536,
			TCPTimeout: time.Hour,
			UDPTimeout: 5 * time.Minute,
			GCInterval: time.Minute,
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}

	if cfg.SOCKS5.Timeout == 0 {
		cfg.SOCKS5.Timeout = 30 * time.Second
	}
	if cfg.SOCKS5.KeepAlive == 0 {
		cfg.SOCKS5.KeepAlive = time.Minute
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

	w.printRouteSummary(cfg)

	fmt.Println()
	fmt.Println("To start Mutiauk manually:")
	fmt.Printf("  sudo mutiauk daemon start -c %s\n", configPath)
	fmt.Println()
}

func (w *Wizard) printRouteSummary(cfg *config.Config) {
	if cfg.AutoRoutes.Enabled {
		fmt.Printf("  Autoroutes:      enabled\n")
		fmt.Printf("                   URL: %s\n", cfg.AutoRoutes.URL)
		fmt.Printf("                   Poll interval: %s\n", cfg.AutoRoutes.PollInterval)
		return
	}

	if len(cfg.Routes) == 0 {
		fmt.Printf("  Routes:          none configured\n")
		return
	}

	fmt.Printf("  Routes:          %d configured\n", len(cfg.Routes))
	for _, r := range cfg.Routes {
		comment := ""
		if r.Comment != "" {
			comment = fmt.Sprintf(" (%s)", r.Comment)
		}
		fmt.Printf("                   - %s%s\n", r.Destination, comment)
	}
}
