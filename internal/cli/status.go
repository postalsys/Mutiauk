package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/coinstash/mutiauk/internal/config"
	"github.com/coinstash/mutiauk/internal/daemon"
	"github.com/coinstash/mutiauk/internal/route"
	"github.com/coinstash/mutiauk/internal/socks5"
	"github.com/spf13/cobra"
)

// StatusResult holds the complete status information
type StatusResult struct {
	Daemon       DaemonStatus       `json:"daemon"`
	TUN          TUNStatus          `json:"tun"`
	SOCKS5       SOCKS5Status       `json:"socks5"`
	Routes       []RouteStatus      `json:"routes"`
	Connectivity ConnectivityStatus `json:"connectivity"`
}

// DaemonStatus holds daemon process status
type DaemonStatus struct {
	Running bool   `json:"running"`
	PID     int    `json:"pid,omitempty"`
	Message string `json:"message"`
}

// TUNStatus holds TUN interface status
type TUNStatus struct {
	Exists  bool   `json:"exists"`
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
	Message string `json:"message"`
}

// SOCKS5Status holds SOCKS5 server configuration status
type SOCKS5Status struct {
	Server string `json:"server"`
	Auth   bool   `json:"auth"`
}

// RouteStatus holds individual route status
type RouteStatus struct {
	Destination string `json:"destination"`
	Active      bool   `json:"active"`
	Comment     string `json:"comment,omitempty"`
}

// ConnectivityStatus holds connectivity test results
type ConnectivityStatus struct {
	SOCKS5Reachable bool   `json:"socks5_reachable"`
	SOCKS5Latency   string `json:"socks5_latency,omitempty"`
	SOCKS5Error     string `json:"socks5_error,omitempty"`
	TCPRouting      bool   `json:"tcp_routing"`
	TCPError        string `json:"tcp_error,omitempty"`
	UDPRouting      bool   `json:"udp_routing"`
	UDPError        string `json:"udp_error,omitempty"`
}

func newStatusCmd() *cobra.Command {
	var jsonOutput bool
	var skipConnTests bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show system status and connectivity tests",
		Long: `Show comprehensive status including:
- Daemon process status
- TUN interface status
- Configured routes
- SOCKS5 proxy connectivity tests

Use --skip-tests to skip connectivity tests (faster).
Use --json for machine-readable output.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			result := gatherStatus(cfg, skipConnTests)

			if jsonOutput {
				return printJSONStatus(result)
			}
			return printHumanStatus(result, cfg)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.Flags().BoolVar(&skipConnTests, "skip-tests", false, "Skip connectivity tests")

	return cmd
}

func gatherStatus(cfg *config.Config, skipConnTests bool) *StatusResult {
	result := &StatusResult{
		Daemon:       checkDaemonStatus(cfg),
		TUN:          checkTUNStatus(cfg),
		SOCKS5:       getSocks5Config(cfg),
		Routes:       checkRoutesStatus(cfg),
		Connectivity: ConnectivityStatus{},
	}

	if !skipConnTests {
		result.Connectivity = runConnectivityTests(cfg)
	}

	return result
}

func checkDaemonStatus(cfg *config.Config) DaemonStatus {
	status := DaemonStatus{
		Running: false,
		Message: "not running",
	}

	pid, err := daemon.ReadPIDFile(cfg.Daemon.PIDFile)
	if err != nil {
		return status
	}

	// Check if process is actually running
	process, err := os.FindProcess(pid)
	if err != nil {
		status.Message = "not running (stale PID file)"
		return status
	}

	// On Unix, FindProcess always succeeds, so we need to send signal 0
	if err := process.Signal(syscall.Signal(0)); err != nil {
		status.Message = "not running (stale PID file)"
		return status
	}

	status.Running = true
	status.PID = pid
	status.Message = fmt.Sprintf("running (PID %d)", pid)
	return status
}

func checkTUNStatus(cfg *config.Config) TUNStatus {
	status := TUNStatus{
		Name:    cfg.TUN.Name,
		Exists:  false,
		Message: "interface not found",
	}

	if runtime.GOOS != "linux" {
		status.Message = "TUN interface only available on Linux"
		return status
	}

	// Check if interface exists using ip command
	out, err := exec.Command("ip", "link", "show", cfg.TUN.Name).Output()
	if err != nil {
		return status
	}

	if len(out) > 0 {
		status.Exists = true
		status.Message = "up"
	}

	// Get IP address
	out, err = exec.Command("ip", "-4", "addr", "show", cfg.TUN.Name).Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "inet ") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					status.Address = parts[1]
				}
				break
			}
		}
	}

	return status
}

func getSocks5Config(cfg *config.Config) SOCKS5Status {
	return SOCKS5Status{
		Server: cfg.SOCKS5.Server,
		Auth:   cfg.HasAuth(),
	}
}

func checkRoutesStatus(cfg *config.Config) []RouteStatus {
	routes := make([]RouteStatus, 0)

	// Get managed routes from route manager
	log := GetLogger()
	mgr, err := route.NewManager(cfg.TUN.Name, log)
	if err != nil {
		// Can't check routes, return config routes as inactive
		for _, r := range cfg.Routes {
			if r.Enabled {
				routes = append(routes, RouteStatus{
					Destination: r.Destination,
					Active:      false,
					Comment:     r.Comment,
				})
			}
		}
		return routes
	}

	// Get actual routes
	activeRoutes, err := mgr.List()
	if err != nil {
		// Return config routes as inactive
		for _, r := range cfg.Routes {
			if r.Enabled {
				routes = append(routes, RouteStatus{
					Destination: r.Destination,
					Active:      false,
					Comment:     r.Comment,
				})
			}
		}
		return routes
	}

	// Create map of active routes
	activeMap := make(map[string]bool)
	for _, r := range activeRoutes {
		if r.Destination != nil {
			activeMap[r.Destination.String()] = true
		}
	}

	// Check each config route
	for _, r := range cfg.Routes {
		if r.Enabled {
			routes = append(routes, RouteStatus{
				Destination: r.Destination,
				Active:      activeMap[r.Destination],
				Comment:     r.Comment,
			})
		}
	}

	return routes
}

func runConnectivityTests(cfg *config.Config) ConnectivityStatus {
	status := ConnectivityStatus{}

	// Test 1: SOCKS5 server reachable (TCP connection)
	start := time.Now()
	conn, err := net.DialTimeout("tcp", cfg.SOCKS5.Server, 5*time.Second)
	if err != nil {
		status.SOCKS5Reachable = false
		status.SOCKS5Error = err.Error()
		return status // No point testing further if we can't reach SOCKS5
	}
	conn.Close()
	status.SOCKS5Reachable = true
	status.SOCKS5Latency = fmt.Sprintf("%dms", time.Since(start).Milliseconds())

	// Create SOCKS5 client for further tests
	var auth socks5.Authenticator
	if cfg.HasAuth() {
		auth = &socks5.UserPassAuth{
			Username: cfg.SOCKS5.Username,
			Password: cfg.SOCKS5.Password,
		}
	}
	client := socks5.NewClient(cfg.SOCKS5.Server, auth, cfg.SOCKS5.Timeout, cfg.SOCKS5.KeepAlive)

	// Test 2: TCP routing through SOCKS5
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tcpConn, err := client.Connect(ctx, "example.com:80")
	if err != nil {
		status.TCPRouting = false
		status.TCPError = err.Error()
	} else {
		tcpConn.Close()
		status.TCPRouting = true
	}

	// Test 3: UDP routing through SOCKS5
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()

	relay, err := client.UDPAssociate(ctx2, nil)
	if err != nil {
		status.UDPRouting = false
		status.UDPError = err.Error()
	} else {
		relay.Close()
		status.UDPRouting = true
	}

	return status
}

func printJSONStatus(result *StatusResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func printHumanStatus(result *StatusResult, cfg *config.Config) error {
	fmt.Println("Mutiauk Status")
	fmt.Println("==============")
	fmt.Println()

	// Daemon status
	fmt.Printf("Daemon:        %s\n", result.Daemon.Message)

	// TUN interface
	tunInfo := result.TUN.Name
	if result.TUN.Address != "" {
		tunInfo = fmt.Sprintf("%s (%s)", result.TUN.Name, result.TUN.Address)
	}
	if result.TUN.Exists {
		fmt.Printf("TUN Interface: %s\n", tunInfo)
	} else {
		fmt.Printf("TUN Interface: %s - %s\n", result.TUN.Name, result.TUN.Message)
	}

	// SOCKS5 server
	socks5Info := result.SOCKS5.Server
	if result.SOCKS5.Auth {
		socks5Info += " (with auth)"
	}
	fmt.Printf("SOCKS5 Server: %s\n", socks5Info)

	// Routes
	fmt.Println()
	fmt.Println("Routes:")
	if len(result.Routes) == 0 {
		fmt.Println("  (no routes configured)")
	} else {
		fmt.Printf("  %-20s  %s\n", "DESTINATION", "STATUS")
		for _, r := range result.Routes {
			status := "inactive"
			if r.Active {
				status = "active"
			}
			fmt.Printf("  %-20s  %s\n", r.Destination, status)
		}
	}

	// Connectivity tests
	fmt.Println()
	fmt.Println("Connectivity:")

	// SOCKS5 connection
	if result.Connectivity.SOCKS5Reachable {
		fmt.Printf("  SOCKS5 Connection: OK (%s)\n", result.Connectivity.SOCKS5Latency)
	} else if result.Connectivity.SOCKS5Error != "" {
		fmt.Printf("  SOCKS5 Connection: FAILED - %s\n", result.Connectivity.SOCKS5Error)
	} else {
		fmt.Println("  SOCKS5 Connection: (not tested)")
	}

	// TCP routing
	if result.Connectivity.TCPRouting {
		fmt.Println("  TCP Routing:       OK")
	} else if result.Connectivity.TCPError != "" {
		fmt.Printf("  TCP Routing:       FAILED - %s\n", result.Connectivity.TCPError)
	} else {
		fmt.Println("  TCP Routing:       (not tested)")
	}

	// UDP routing
	if result.Connectivity.UDPRouting {
		fmt.Println("  UDP Routing:       OK")
	} else if result.Connectivity.UDPError != "" {
		fmt.Printf("  UDP Routing:       FAILED - %s\n", result.Connectivity.UDPError)
	} else {
		fmt.Println("  UDP Routing:       (not tested)")
	}

	// Summary
	fmt.Println()
	if !result.Daemon.Running {
		fmt.Println("Note: Daemon is not running. Start with: sudo mutiauk daemon start")
	} else if !result.Connectivity.SOCKS5Reachable {
		fmt.Println("Warning: Cannot reach SOCKS5 server. Check if it is running.")
	} else if !result.Connectivity.TCPRouting {
		fmt.Println("Warning: TCP routing through SOCKS5 failed. Check SOCKS5 server configuration.")
	}

	return nil
}
