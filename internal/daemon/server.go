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

// routeState manages both config and auto routes with unified locking
type routeState struct {
	mu         sync.RWMutex
	autoRoutes []route.Route
}

func (rs *routeState) setAutoRoutes(routes []route.Route) {
	rs.mu.Lock()
	rs.autoRoutes = routes
	rs.mu.Unlock()
}

func (rs *routeState) getAutoRoutes() []route.Route {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	if rs.autoRoutes == nil {
		return nil
	}
	result := make([]route.Route, len(rs.autoRoutes))
	copy(result, rs.autoRoutes)
	return result
}

func (rs *routeState) autoRouteCount() int {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return len(rs.autoRoutes)
}

// networkStack holds all network-related components
type networkStack struct {
	tunDev          tun.Device
	netStack        *stack.Stack
	natTable        *nat.Table
	socks5Client    *socks5.Client
	tcpForwarder    *proxy.TCPForwarder
	udpForwarder    *proxy.UDPForwarder
	rawUDPForwarder *proxy.RawUDPForwarder
}

// Server is the main daemon server
type Server struct {
	cfg        *config.Config
	configPath string
	logger     *zap.Logger

	mu        sync.RWMutex
	running   bool
	startTime time.Time
	cancelFn  context.CancelFunc

	network    networkStack
	routeMgr   *route.Manager
	routeState routeState
	apiServer  *api.Server
	autoPoller *autoroutes.Poller
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
	if err := s.setRunning(true); err != nil {
		return err
	}
	defer s.setRunning(false)

	ctx, cancel := context.WithCancel(ctx)
	s.cancelFn = cancel
	defer cancel()

	if err := s.writePIDFile(); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}
	defer s.removePIDFile()

	if err := s.initialize(); err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}
	defer s.cleanup()

	s.startServices(ctx)

	s.logger.Info("daemon started",
		zap.String("tun", s.cfg.TUN.Name),
		zap.String("socks5", s.cfg.SOCKS5.Server),
		zap.Bool("autoroutes", s.cfg.AutoRoutes.Enabled),
	)

	return s.network.netStack.Run(ctx)
}

// setRunning atomically sets the running state
func (s *Server) setRunning(running bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if running && s.running {
		return fmt.Errorf("server already running")
	}
	s.running = running
	if running {
		s.startTime = time.Now()
	}
	return nil
}

// startServices starts API server, routes, autoroutes poller, and NAT GC
func (s *Server) startServices(ctx context.Context) {
	if s.cfg.Daemon.SocketPath != "" {
		s.apiServer = api.NewServer(s.cfg.Daemon.SocketPath, s, s.logger)
		if err := s.apiServer.Start(ctx); err != nil {
			s.logger.Warn("failed to start API server", zap.Error(err))
		}
	}

	if err := s.syncRoutes(); err != nil {
		s.logger.Warn("failed to apply some routes", zap.Error(err))
	}

	if s.cfg.AutoRoutes.Enabled {
		s.startAutoRoutes(ctx)
	}

	s.network.natTable.StartGC(ctx, nat.GCConfig{
		Interval: s.cfg.NAT.GCInterval,
		Logger:   s.logger,
	})
}

// initialize sets up all components
func (s *Server) initialize() error {
	tunCfg, err := s.parseTUNConfig()
	if err != nil {
		return err
	}

	if err := s.initNetwork(tunCfg); err != nil {
		return err
	}

	s.routeMgr, err = route.NewManager(s.cfg.TUN.Name, s.logger)
	if err != nil {
		return fmt.Errorf("failed to create route manager: %w", err)
	}

	return nil
}

// parseTUNConfig parses TUN configuration from config
func (s *Server) parseTUNConfig() (tun.Config, error) {
	tunCfg := tun.Config{
		Name:    s.cfg.TUN.Name,
		MTU:     s.cfg.TUN.MTU,
		Persist: false,
	}

	if s.cfg.TUN.Address != "" {
		ip, ipNet, err := net.ParseCIDR(s.cfg.TUN.Address)
		if err != nil {
			return tunCfg, fmt.Errorf("invalid TUN address: %w", err)
		}
		tunCfg.Address = ip
		tunCfg.Netmask = ipNet.Mask
	}

	if s.cfg.TUN.Address6 != "" {
		ip, ipNet, err := net.ParseCIDR(s.cfg.TUN.Address6)
		if err != nil {
			return tunCfg, fmt.Errorf("invalid TUN IPv6 address: %w", err)
		}
		tunCfg.Address6 = ip
		tunCfg.Netmask6 = ipNet.Mask
	}

	return tunCfg, nil
}

// initNetwork initializes all network components
func (s *Server) initNetwork(tunCfg tun.Config) error {
	var err error

	s.network.tunDev, err = tun.New(tunCfg)
	if err != nil {
		return fmt.Errorf("failed to create TUN device: %w", err)
	}
	s.logger.Info("TUN device created", zap.String("name", s.network.tunDev.Name()))

	s.network.natTable = nat.NewTable(nat.Config{
		MaxEntries: s.cfg.NAT.TableSize,
		TCPTTL:     s.cfg.NAT.TCPTimeout,
		UDPTTL:     s.cfg.NAT.UDPTimeout,
	})

	s.network.socks5Client = s.createSOCKS5Client()
	s.network.tcpForwarder = proxy.NewTCPForwarder(s.network.socks5Client, s.network.natTable, s.logger)
	s.network.udpForwarder = proxy.NewUDPForwarder(s.network.socks5Client, s.network.natTable, s.logger)
	s.network.rawUDPForwarder = proxy.NewRawUDPForwarder(s.network.socks5Client, s.logger)

	stackCfg := stack.Config{
		MTU:           s.cfg.TUN.MTU,
		TCPHandler:    s.network.tcpForwarder,
		UDPHandler:    s.network.udpForwarder,
		RawUDPHandler: s.network.rawUDPForwarder,
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

	s.network.netStack, err = stack.New(s.network.tunDev, stackCfg)
	if err != nil {
		return fmt.Errorf("failed to create network stack: %w", err)
	}

	return nil
}

// createSOCKS5Client creates a SOCKS5 client from current config
func (s *Server) createSOCKS5Client() *socks5.Client {
	auth := socks5.NewAuthenticator(s.cfg.SOCKS5.Username, s.cfg.SOCKS5.Password)
	return socks5.NewClient(
		s.cfg.SOCKS5.Server,
		auth,
		s.cfg.SOCKS5.Timeout,
		s.cfg.SOCKS5.KeepAlive,
	)
}

// cleanup closes all components
func (s *Server) cleanup() {
	if s.apiServer != nil {
		s.apiServer.Stop()
	}
	if s.network.netStack != nil {
		s.network.netStack.Close()
	}
	if s.network.tunDev != nil {
		s.network.tunDev.Close()
	}
	if s.network.natTable != nil {
		s.network.natTable.Clear()
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
	s.routeState.setAutoRoutes(routes)

	if err := s.syncRoutes(); err != nil {
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

// syncRoutes synchronizes all routes (config + auto) with the system
func (s *Server) syncRoutes() error {
	configRoutes := s.getConfigRoutes()
	autoRoutes := s.routeState.getAutoRoutes()

	if len(autoRoutes) == 0 {
		return s.routeMgr.Sync(configRoutes)
	}

	combined := autoroutes.MergeRoutes(configRoutes, autoRoutes)

	s.logger.Debug("applying routes",
		zap.Int("config_routes", len(configRoutes)),
		zap.Int("auto_routes", len(autoRoutes)),
		zap.Int("combined", len(combined)),
	)

	return s.routeMgr.Sync(combined)
}

// Reload applies a new configuration
func (s *Server) Reload(cfg *config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.socks5ConfigChanged(cfg) {
		s.network.socks5Client = socks5.NewClient(
			cfg.SOCKS5.Server,
			socks5.NewAuthenticator(cfg.SOCKS5.Username, cfg.SOCKS5.Password),
			cfg.SOCKS5.Timeout,
			cfg.SOCKS5.KeepAlive,
		)
		s.network.tcpForwarder.UpdateClient(s.network.socks5Client)
		s.network.udpForwarder.UpdateClient(s.network.socks5Client)
		s.logger.Info("SOCKS5 client updated", zap.String("server", cfg.SOCKS5.Server))
	}

	s.cfg = cfg

	if err := s.syncRoutes(); err != nil {
		s.logger.Warn("failed to reapply routes", zap.Error(err))
	}

	return nil
}

// socks5ConfigChanged checks if SOCKS5 configuration has changed
func (s *Server) socks5ConfigChanged(cfg *config.Config) bool {
	return cfg.SOCKS5.Server != s.cfg.SOCKS5.Server ||
		cfg.SOCKS5.Username != s.cfg.SOCKS5.Username ||
		cfg.SOCKS5.Password != s.cfg.SOCKS5.Password
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

	autoRouteCount := s.routeState.autoRouteCount()

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

	for _, r := range s.cfg.Routes {
		routes = append(routes, api.RouteInfo{
			Destination: r.Destination,
			Interface:   s.cfg.TUN.Name,
			Comment:     r.Comment,
			Source:      "config",
			Enabled:     r.Enabled,
		})
	}

	for _, r := range s.routeState.getAutoRoutes() {
		routes = append(routes, api.RouteInfo{
			Destination: r.Destination.String(),
			Interface:   s.cfg.TUN.Name,
			Comment:     r.Comment,
			Source:      "auto",
			Enabled:     true,
		})
	}

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
