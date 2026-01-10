package cli

import (
	"fmt"
	"net"

	"github.com/postalsys/mutiauk/internal/api"
	"github.com/postalsys/mutiauk/internal/config"
	"github.com/postalsys/mutiauk/internal/route"
)

// loadConfig loads the configuration file from the global cfgFile path.
func loadConfig() (*config.Config, error) {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return cfg, nil
}

// newRouteManager creates a route manager for the configured TUN interface.
func newRouteManager(cfg *config.Config) (*route.Manager, error) {
	mgr, err := route.NewManager(cfg.TUN.Name, GetLogger())
	if err != nil {
		return nil, fmt.Errorf("failed to create route manager: %w", err)
	}
	return mgr, nil
}

// configRoutesToRoutes converts config routes to route.Route slice,
// filtering only enabled routes.
func configRoutesToRoutes(cfg *config.Config) ([]route.Route, error) {
	var routes []route.Route
	for _, r := range cfg.Routes {
		if !r.Enabled {
			continue
		}
		_, ipNet, err := net.ParseCIDR(r.Destination)
		if err != nil {
			return nil, fmt.Errorf("invalid route in config: %s: %w", r.Destination, err)
		}
		routes = append(routes, route.Route{
			Destination: ipNet,
			Interface:   cfg.TUN.Name,
			Comment:     r.Comment,
			Enabled:     r.Enabled,
		})
	}
	return routes, nil
}

// configRoutesToRoutesAll converts all config routes to route.Route slice,
// including disabled routes.
func configRoutesToRoutesAll(cfg *config.Config) ([]route.Route, error) {
	var routes []route.Route
	for _, r := range cfg.Routes {
		_, ipNet, err := net.ParseCIDR(r.Destination)
		if err != nil {
			return nil, fmt.Errorf("invalid route in config: %s: %w", r.Destination, err)
		}
		routes = append(routes, route.Route{
			Destination: ipNet,
			Comment:     r.Comment,
			Enabled:     r.Enabled,
		})
	}
	return routes, nil
}

// DaemonAPIClient wraps the API client with helper methods for daemon interaction.
type DaemonAPIClient struct {
	client  *api.Client
	running bool
}

// newDaemonAPIClient creates a new daemon API client from the config.
func newDaemonAPIClient(cfg *config.Config) *DaemonAPIClient {
	client := api.NewClient(cfg.Daemon.SocketPath)
	return &DaemonAPIClient{
		client:  client,
		running: client.IsRunning(),
	}
}

// IsRunning returns true if the daemon is running and accessible.
func (d *DaemonAPIClient) IsRunning() bool {
	return d.running
}

// RouteAddResult contains the result of adding a route via daemon API.
type RouteAddResult struct {
	Success   bool
	Persisted bool
}

// AddRoute attempts to add a route via the daemon API.
// Returns nil, nil if the daemon is not running (caller should fall back to direct operation).
func (d *DaemonAPIClient) AddRoute(cidr, comment string, persist bool) (*RouteAddResult, error) {
	if !d.running {
		return nil, nil
	}
	result, err := d.client.RouteAdd(cidr, comment, persist)
	if err != nil {
		return nil, fmt.Errorf("failed to add route via daemon: %w", err)
	}
	if !result.Success {
		return nil, fmt.Errorf("failed to add route: %s", result.Message)
	}
	return &RouteAddResult{
		Success:   true,
		Persisted: result.Persisted,
	}, nil
}

// RouteRemoveResult contains the result of removing a route via daemon API.
type RouteRemoveResult struct {
	Success   bool
	Persisted bool
}

// RemoveRoute attempts to remove a route via the daemon API.
// Returns nil, nil if the daemon is not running (caller should fall back to direct operation).
func (d *DaemonAPIClient) RemoveRoute(cidr string, persist bool) (*RouteRemoveResult, error) {
	if !d.running {
		return nil, nil
	}
	result, err := d.client.RouteRemove(cidr, persist)
	if err != nil {
		return nil, fmt.Errorf("failed to remove route via daemon: %w", err)
	}
	if !result.Success {
		return nil, fmt.Errorf("failed to remove route: %s", result.Message)
	}
	return &RouteRemoveResult{
		Success:   true,
		Persisted: result.Persisted,
	}, nil
}
