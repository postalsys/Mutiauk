package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/postalsys/mutiauk/internal/api"
	"github.com/postalsys/mutiauk/internal/autoroutes"
	"github.com/postalsys/mutiauk/internal/config"
	"github.com/postalsys/mutiauk/internal/nat"
	"github.com/postalsys/mutiauk/internal/proxy"
	"github.com/postalsys/mutiauk/internal/route"
	"github.com/postalsys/mutiauk/internal/socks5"
	"github.com/postalsys/mutiauk/internal/stack"
	"github.com/postalsys/mutiauk/internal/tun"
	"go.uber.org/zap"
)

// Server is the main daemon server
type Server struct {
	cfg        *config.Config
	configPath string // Path to config file we were started with
	logger     *zap.Logger
	startTime  time.Time

	tunDev          tun.Device
	netStack        *stack.Stack
	natTable        *nat.Table
	routeMgr        *route.Manager
	socks5Client    *socks5.Client
	tcpForwarder    *proxy.TCPForwarder
	udpForwarder    *proxy.UDPForwarder
	rawUDPForwarder *proxy.RawUDPForwarder

	// API server for CLI communication
	apiServer *api.Server

	// Autoroutes polling
	autoPoller   *autoroutes.Poller
	autoRoutes   []route.Route
	autoRoutesMu sync.RWMutex

	mu       sync.RWMutex
	running  bool
	cancelFn context.CancelFunc
}

// New creates a new daemon server
func New(cfg *config.Config, configPath string, logger *zap.Logger) (*Server, error) {
	return &Server{
		cfg:        cfg,
		configPath: configPath,
		logger:     logger,
	}, nil
}

// Run starts the daemon server
func (s *Server) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}
	s.running = true
	s.startTime = time.Now()
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	// Create cancellable context
	ctx, cancel := context.WithCancel(ctx)
	s.cancelFn = cancel
	defer cancel()

	// Write PID file
	if err := s.writePIDFile(); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}
	defer s.removePIDFile()

	// Initialize components
	if err := s.initialize(); err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}
	defer s.cleanup()

	// Start API server for CLI communication
	if s.cfg.Daemon.SocketPath != "" {
		s.apiServer = api.NewServer(s.cfg.Daemon.SocketPath, s, s.logger)
		if err := s.apiServer.Start(ctx); err != nil {
			s.logger.Warn("failed to start API server", zap.Error(err))
		}
	}

	// Apply routes from config
	if err := s.applyRoutes(); err != nil {
		s.logger.Warn("failed to apply some routes", zap.Error(err))
	}

	// Start autoroutes polling if enabled
	if s.cfg.AutoRoutes.Enabled {
		s.startAutoRoutes(ctx)
	}

	// Start NAT garbage collection
	s.natTable.StartGC(ctx, nat.GCConfig{
		Interval: s.cfg.NAT.GCInterval,
		Logger:   s.logger,
	})

	s.logger.Info("daemon started",
		zap.String("tun", s.cfg.TUN.Name),
		zap.String("socks5", s.cfg.SOCKS5.Server),
		zap.Bool("autoroutes", s.cfg.AutoRoutes.Enabled),
	)

	// Run the network stack (blocks until context cancelled)
	return s.netStack.Run(ctx)
}

// initialize sets up all components
func (s *Server) initialize() error {
	var err error

	// Parse TUN addresses
	tunCfg := tun.Config{
		Name:    s.cfg.TUN.Name,
		MTU:     s.cfg.TUN.MTU,
		Persist: false,
	}

	if s.cfg.TUN.Address != "" {
		ip, ipNet, err := net.ParseCIDR(s.cfg.TUN.Address)
		if err != nil {
			return fmt.Errorf("invalid TUN address: %w", err)
		}
		tunCfg.Address = ip
		tunCfg.Netmask = ipNet.Mask
	}

	if s.cfg.TUN.Address6 != "" {
		ip, ipNet, err := net.ParseCIDR(s.cfg.TUN.Address6)
		if err != nil {
			return fmt.Errorf("invalid TUN IPv6 address: %w", err)
		}
		tunCfg.Address6 = ip
		tunCfg.Netmask6 = ipNet.Mask
	}

	// Create TUN device
	s.tunDev, err = tun.New(tunCfg)
	if err != nil {
		return fmt.Errorf("failed to create TUN device: %w", err)
	}
	s.logger.Info("TUN device created", zap.String("name", s.tunDev.Name()))

	// Create NAT table
	s.natTable = nat.NewTable(nat.Config{
		MaxEntries: s.cfg.NAT.TableSize,
		TCPTTL:     s.cfg.NAT.TCPTimeout,
		UDPTTL:     s.cfg.NAT.UDPTimeout,
	})

	// Create SOCKS5 client
	auth := socks5.NewAuthenticator(s.cfg.SOCKS5.Username, s.cfg.SOCKS5.Password)
	s.socks5Client = socks5.NewClient(
		s.cfg.SOCKS5.Server,
		auth,
		s.cfg.SOCKS5.Timeout,
		s.cfg.SOCKS5.KeepAlive,
	)

	// Create forwarders
	s.tcpForwarder = proxy.NewTCPForwarder(s.socks5Client, s.natTable, s.logger)
	s.udpForwarder = proxy.NewUDPForwarder(s.socks5Client, s.natTable, s.logger)
	s.rawUDPForwarder = proxy.NewRawUDPForwarder(s.socks5Client, s.logger)

	// Create network stack
	stackCfg := stack.Config{
		MTU:           s.cfg.TUN.MTU,
		TCPHandler:    s.tcpForwarder,
		UDPHandler:    s.udpForwarder,
		RawUDPHandler: s.rawUDPForwarder,
		Logger:        s.logger,
	}

	if tunCfg.Address != nil {
		stackCfg.IPv4Addr = tunCfg.Address
		stackCfg.IPv4Mask = tunCfg.Netmask
	}
	if tunCfg.Address6 != nil {
		stackCfg.IPv6Addr = tunCfg.Address6
		stackCfg.IPv6Mask = tunCfg.Netmask6
	}

	s.netStack, err = stack.New(s.tunDev, stackCfg)
	if err != nil {
		return fmt.Errorf("failed to create network stack: %w", err)
	}

	// Create route manager
	s.routeMgr, err = route.NewManager(s.cfg.TUN.Name, s.logger)
	if err != nil {
		return fmt.Errorf("failed to create route manager: %w", err)
	}

	return nil
}

// cleanup closes all components
func (s *Server) cleanup() {
	if s.apiServer != nil {
		s.apiServer.Stop()
	}
	if s.netStack != nil {
		s.netStack.Close()
	}
	if s.tunDev != nil {
		s.tunDev.Close()
	}
	if s.natTable != nil {
		s.natTable.Clear()
	}
}

// startAutoRoutes initializes and starts the autoroutes poller
func (s *Server) startAutoRoutes(ctx context.Context) {
	client := autoroutes.NewClient(
		s.cfg.AutoRoutes.URL,
		s.cfg.AutoRoutes.Timeout,
		s.logger,
	)
	s.autoPoller = autoroutes.NewPoller(
		client,
		s.cfg.AutoRoutes.PollInterval,
		s.cfg.TUN.Name,
		s.logger,
	)

	s.logger.Info("starting autoroutes poller",
		zap.String("url", s.cfg.AutoRoutes.URL),
		zap.Duration("interval", s.cfg.AutoRoutes.PollInterval),
	)

	go s.autoPoller.Start(ctx, s.onAutoRoutesUpdate)
}

// onAutoRoutesUpdate is called when new autoroutes are fetched
func (s *Server) onAutoRoutesUpdate(routes []route.Route) {
	s.autoRoutesMu.Lock()
	s.autoRoutes = routes
	s.autoRoutesMu.Unlock()

	if err := s.applyAllRoutes(); err != nil {
		s.logger.Warn("failed to apply autoroutes", zap.Error(err))
	}
}

// getConfigRoutes returns routes from configuration
func (s *Server) getConfigRoutes() []route.Route {
	var routes []route.Route

	for _, r := range s.cfg.Routes {
		if !r.Enabled {
			continue
		}

		_, ipNet, err := net.ParseCIDR(r.Destination)
		if err != nil {
			s.logger.Warn("invalid route destination",
				zap.String("destination", r.Destination),
				zap.Error(err),
			)
			continue
		}

		routes = append(routes, route.Route{
			Destination: ipNet,
			Interface:   s.cfg.TUN.Name,
			Comment:     r.Comment,
			Enabled:     true,
		})
	}

	return routes
}

// applyRoutes applies routes from configuration only (no autoroutes)
func (s *Server) applyRoutes() error {
	return s.routeMgr.Sync(s.getConfigRoutes())
}

// applyAllRoutes applies both config routes and autoroutes
func (s *Server) applyAllRoutes() error {
	configRoutes := s.getConfigRoutes()

	s.autoRoutesMu.RLock()
	autoRoutesCopy := make([]route.Route, len(s.autoRoutes))
	copy(autoRoutesCopy, s.autoRoutes)
	s.autoRoutesMu.RUnlock()

	// Merge routes: config routes take precedence
	combined := autoroutes.MergeRoutes(configRoutes, autoRoutesCopy)

	s.logger.Debug("applying routes",
		zap.Int("config_routes", len(configRoutes)),
		zap.Int("auto_routes", len(autoRoutesCopy)),
		zap.Int("combined", len(combined)),
	)

	return s.routeMgr.Sync(combined)
}

// Reload applies a new configuration
func (s *Server) Reload(cfg *config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update SOCKS5 client if server changed
	if cfg.SOCKS5.Server != s.cfg.SOCKS5.Server ||
		cfg.SOCKS5.Username != s.cfg.SOCKS5.Username ||
		cfg.SOCKS5.Password != s.cfg.SOCKS5.Password {

		auth := socks5.NewAuthenticator(cfg.SOCKS5.Username, cfg.SOCKS5.Password)
		s.socks5Client = socks5.NewClient(
			cfg.SOCKS5.Server,
			auth,
			cfg.SOCKS5.Timeout,
			cfg.SOCKS5.KeepAlive,
		)
		s.tcpForwarder.UpdateClient(s.socks5Client)
		s.udpForwarder.UpdateClient(s.socks5Client)
		s.logger.Info("SOCKS5 client updated", zap.String("server", cfg.SOCKS5.Server))
	}

	// Update config
	s.cfg = cfg

	// Reapply routes (includes autoroutes if enabled)
	if s.autoPoller != nil {
		if err := s.applyAllRoutes(); err != nil {
			s.logger.Warn("failed to reapply routes", zap.Error(err))
		}
	} else {
		if err := s.applyRoutes(); err != nil {
			s.logger.Warn("failed to reapply routes", zap.Error(err))
		}
	}

	return nil
}

// Stop stops the daemon server
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancelFn != nil {
		s.cancelFn()
	}
	return nil
}

// writePIDFile writes the PID to a file
func (s *Server) writePIDFile() error {
	pid := os.Getpid()
	return os.WriteFile(s.cfg.Daemon.PIDFile, []byte(strconv.Itoa(pid)), 0644)
}

// removePIDFile removes the PID file
func (s *Server) removePIDFile() {
	os.Remove(s.cfg.Daemon.PIDFile)
}

// ReadPIDFile reads the PID from a file
func ReadPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(string(data))
}

// StateProvider interface implementation for API server

// GetStatus returns the daemon status
func (s *Server) GetStatus() *api.StatusResult {
	s.mu.RLock()
	running := s.running
	startTime := s.startTime
	s.mu.RUnlock()

	s.autoRoutesMu.RLock()
	autoRouteCount := len(s.autoRoutes)
	s.autoRoutesMu.RUnlock()

	result := &api.StatusResult{
		Running:      running,
		PID:          os.Getpid(),
		ConfigPath:   s.configPath,
		TUNName:      s.cfg.TUN.Name,
		TUNAddress:   s.cfg.TUN.Address,
		SOCKS5Server: s.cfg.SOCKS5.Server,
		SOCKS5Status: "configured",
	}

	if running {
		result.Uptime = time.Since(startTime).Round(time.Second).String()
	}

	result.AutoRoutes.Enabled = s.cfg.AutoRoutes.Enabled
	if s.cfg.AutoRoutes.Enabled {
		result.AutoRoutes.URL = s.cfg.AutoRoutes.URL
		result.AutoRoutes.Count = autoRouteCount
	}

	result.RouteCount.Config = len(s.cfg.Routes)
	result.RouteCount.Auto = autoRouteCount
	result.RouteCount.Total = result.RouteCount.Config + result.RouteCount.Auto

	return result
}

// GetRoutes returns the list of active routes with source info
func (s *Server) GetRoutes() []api.RouteInfo {
	var routes []api.RouteInfo

	// Config routes
	for _, r := range s.cfg.Routes {
		routes = append(routes, api.RouteInfo{
			Destination: r.Destination,
			Interface:   s.cfg.TUN.Name,
			Comment:     r.Comment,
			Source:      "config",
			Enabled:     r.Enabled,
		})
	}

	// Auto routes
	s.autoRoutesMu.RLock()
	for _, r := range s.autoRoutes {
		routes = append(routes, api.RouteInfo{
			Destination: r.Destination.String(),
			Interface:   s.cfg.TUN.Name,
			Comment:     r.Comment,
			Source:      "auto",
			Enabled:     true,
		})
	}
	s.autoRoutesMu.RUnlock()

	return routes
}

// AddRoute adds a route and optionally persists to config
func (s *Server) AddRoute(dest, comment string, persist bool) error {
	_, ipNet, err := net.ParseCIDR(dest)
	if err != nil {
		return fmt.Errorf("invalid CIDR: %w", err)
	}

	r := route.Route{
		Destination: ipNet,
		Interface:   s.cfg.TUN.Name,
		Comment:     comment,
		Enabled:     true,
	}

	if err := s.routeMgr.Add(r); err != nil {
		return fmt.Errorf("failed to add route: %w", err)
	}

	if persist {
		s.cfg.Routes = append(s.cfg.Routes, config.RouteConfig{
			Destination: dest,
			Comment:     comment,
			Enabled:     true,
		})
		if err := s.cfg.Save(s.configPath); err != nil {
			return fmt.Errorf("route added but failed to save config: %w", err)
		}
	}

	return nil
}

// RemoveRoute removes a route and optionally persists to config
func (s *Server) RemoveRoute(dest string, persist bool) error {
	_, ipNet, err := net.ParseCIDR(dest)
	if err != nil {
		return fmt.Errorf("invalid CIDR: %w", err)
	}

	r := route.Route{
		Destination: ipNet,
	}

	if err := s.routeMgr.Remove(r); err != nil {
		return fmt.Errorf("failed to remove route: %w", err)
	}

	if persist {
		// Remove from config
		for i, rc := range s.cfg.Routes {
			if rc.Destination == dest {
				s.cfg.Routes = append(s.cfg.Routes[:i], s.cfg.Routes[i+1:]...)
				break
			}
		}
		if err := s.cfg.Save(s.configPath); err != nil {
			return fmt.Errorf("route removed but failed to save config: %w", err)
		}
	}

	return nil
}

// TraceRoute traces routing for a destination
func (s *Server) TraceRoute(dest string) (*api.RouteTraceResult, error) {
	lookup, err := route.ResolveAndLookup(dest, s.cfg.TUN.Name)
	if err != nil {
		return nil, err
	}

	result := &api.RouteTraceResult{
		Destination:  dest,
		ResolvedIP:   lookup.ResolvedIP.String(),
		MatchedRoute: lookup.MatchedRoute.String(),
		Interface:    lookup.Interface,
		IsMutiauk:    lookup.IsMutiauk,
	}

	if lookup.Gateway != nil && !lookup.Gateway.IsUnspecified() {
		result.Gateway = lookup.Gateway.String()
	}

	// If routed through Mutiauk and autoroutes enabled, lookup mesh path
	if lookup.IsMutiauk && s.cfg.AutoRoutes.URL != "" {
		client := autoroutes.NewClient(
			s.cfg.AutoRoutes.URL,
			s.cfg.AutoRoutes.Timeout,
			s.logger,
		)

		path, err := client.LookupPath(context.Background(), lookup.ResolvedIP)
		if err != nil {
			s.logger.Debug("failed to lookup mesh path", zap.Error(err))
		} else if path != nil {
			result.MeshPath = path.PathDisplay
			result.Origin = path.Origin
			result.OriginID = path.OriginID
			result.HopCount = path.HopCount
		}
	}

	return result, nil
}

// GetConfigPath returns the config file path
func (s *Server) GetConfigPath() string {
	return s.configPath
}

// formatUptime formats duration as human-readable string
func formatUptime(d time.Duration) string {
	d = d.Round(time.Second)

	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	sec := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, sec)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, sec)
	}
	return fmt.Sprintf("%ds", sec)
}
