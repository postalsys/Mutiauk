package route

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/vishvananda/netlink"
	"go.uber.org/zap"
)

// Route represents a routing entry
type Route struct {
	Destination *net.IPNet `json:"destination"`
	Gateway     net.IP     `json:"gateway,omitempty"`
	Interface   string     `json:"interface"`
	Metric      int        `json:"metric,omitempty"`
	Comment     string     `json:"comment,omitempty"`
	Enabled     bool       `json:"enabled"`
}

// Manager handles route operations
type Manager struct {
	mu        sync.RWMutex
	tunName   string
	stateFile string
	link      netlink.Link
	logger    *zap.Logger
}

// NewManager creates a new route manager
func NewManager(tunName string, logger *zap.Logger) (*Manager, error) {
	m := &Manager{
		tunName:   tunName,
		stateFile: filepath.Join("/var/lib/mutiauk", "routes.json"),
		logger:    logger,
	}

	// Try to get the link (may not exist yet if daemon not running)
	link, err := netlink.LinkByName(tunName)
	if err == nil {
		m.link = link
	}

	return m, nil
}

// SetStateFile sets the state file path
func (m *Manager) SetStateFile(path string) {
	m.stateFile = path
}

// refreshLink refreshes the link reference
func (m *Manager) refreshLink() error {
	link, err := netlink.LinkByName(m.tunName)
	if err != nil {
		return fmt.Errorf("failed to get link %s: %w", m.tunName, err)
	}
	m.link = link
	return nil
}

// Add adds a route (idempotent)
func (m *Manager) Add(r Route) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.refreshLink(); err != nil {
		return err
	}

	// Check if route already exists
	routes, err := netlink.RouteList(m.link, netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}

	for _, existing := range routes {
		if existing.Dst != nil && existing.Dst.String() == r.Destination.String() {
			// Route already exists
			m.logger.Debug("route already exists", zap.String("destination", r.Destination.String()))
			return nil
		}
	}

	// Add the route
	nlRoute := &netlink.Route{
		LinkIndex: m.link.Attrs().Index,
		Dst:       r.Destination,
		Scope:     netlink.SCOPE_LINK,
	}

	if r.Gateway != nil {
		nlRoute.Gw = r.Gateway
		nlRoute.Scope = netlink.SCOPE_UNIVERSE
	}

	if r.Metric > 0 {
		nlRoute.Priority = r.Metric
	}

	if err := netlink.RouteAdd(nlRoute); err != nil {
		return fmt.Errorf("failed to add route: %w", err)
	}

	m.logger.Info("added route",
		zap.String("destination", r.Destination.String()),
		zap.String("interface", m.tunName),
	)

	// Save state
	return m.saveState(r, true)
}

// Remove removes a route (idempotent)
func (m *Manager) Remove(r Route) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.refreshLink(); err != nil {
		// If link doesn't exist, route is effectively removed
		m.logger.Debug("link not found, route considered removed")
		return m.saveState(r, false)
	}

	nlRoute := &netlink.Route{
		LinkIndex: m.link.Attrs().Index,
		Dst:       r.Destination,
	}

	if err := netlink.RouteDel(nlRoute); err != nil {
		// Check if route doesn't exist (not an error for idempotent operation)
		if os.IsNotExist(err) || err.Error() == "no such process" {
			m.logger.Debug("route already removed", zap.String("destination", r.Destination.String()))
			return m.saveState(r, false)
		}
		return fmt.Errorf("failed to remove route: %w", err)
	}

	m.logger.Info("removed route", zap.String("destination", r.Destination.String()))
	return m.saveState(r, false)
}

// List returns routes managed by this agent
func (m *Manager) List() ([]Route, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Load state file
	state, err := m.loadState()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var routes []Route
	for _, r := range state {
		r.Interface = m.tunName
		routes = append(routes, r)
	}

	return routes, nil
}

// ListKernel returns all routes for the TUN interface from the kernel
func (m *Manager) ListKernel() ([]Route, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.refreshLink(); err != nil {
		return nil, nil // No link, no routes
	}

	nlRoutes, err := netlink.RouteList(m.link, netlink.FAMILY_ALL)
	if err != nil {
		return nil, fmt.Errorf("failed to list routes: %w", err)
	}

	var routes []Route
	for _, nlr := range nlRoutes {
		if nlr.Dst == nil {
			continue
		}
		routes = append(routes, Route{
			Destination: nlr.Dst,
			Gateway:     nlr.Gw,
			Interface:   m.tunName,
			Metric:      nlr.Priority,
			Enabled:     true,
		})
	}

	return routes, nil
}

// State represents saved route state
type State map[string]Route

// saveState saves route state to file
func (m *Manager) saveState(r Route, add bool) error {
	state, err := m.loadState()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if state == nil {
		state = make(State)
	}

	key := r.Destination.String()
	if add {
		r.Interface = m.tunName
		r.Enabled = true
		state[key] = r
	} else {
		delete(state, key)
	}

	// Ensure directory exists
	dir := filepath.Dir(m.stateFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(m.stateFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// loadState loads route state from file
func (m *Manager) loadState() (State, error) {
	data, err := os.ReadFile(m.stateFile)
	if err != nil {
		return nil, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	return state, nil
}

// Sync synchronizes routes to match desired state
func (m *Manager) Sync(desired []Route) error {
	plan, err := m.Diff(desired)
	if err != nil {
		return err
	}
	return m.Apply(plan)
}
