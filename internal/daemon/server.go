package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"

	"github.com/coinstash/mutiauk/internal/config"
	"github.com/coinstash/mutiauk/internal/nat"
	"github.com/coinstash/mutiauk/internal/proxy"
	"github.com/coinstash/mutiauk/internal/route"
	"github.com/coinstash/mutiauk/internal/socks5"
	"github.com/coinstash/mutiauk/internal/stack"
	"github.com/coinstash/mutiauk/internal/tun"
	"go.uber.org/zap"
)

// Server is the main daemon server
type Server struct {
	cfg    *config.Config
	logger *zap.Logger

	tunDev       tun.Device
	netStack     *stack.Stack
	natTable     *nat.Table
	routeMgr     *route.Manager
	socks5Client *socks5.Client
	tcpForwarder *proxy.TCPForwarder
	udpForwarder *proxy.UDPForwarder

	mu       sync.RWMutex
	running  bool
	cancelFn context.CancelFunc
}

// New creates a new daemon server
func New(cfg *config.Config, logger *zap.Logger) (*Server, error) {
	return &Server{
		cfg:    cfg,
		logger: logger,
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

	// Apply routes from config
	if err := s.applyRoutes(); err != nil {
		s.logger.Warn("failed to apply some routes", zap.Error(err))
	}

	// Start NAT garbage collection
	s.natTable.StartGC(ctx, nat.GCConfig{
		Interval: s.cfg.NAT.GCInterval,
		Logger:   s.logger,
	})

	s.logger.Info("daemon started",
		zap.String("tun", s.cfg.TUN.Name),
		zap.String("socks5", s.cfg.SOCKS5.Server),
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

	// Create network stack
	stackCfg := stack.Config{
		MTU:         s.cfg.TUN.MTU,
		TCPHandler:  s.tcpForwarder,
		UDPHandler:  s.udpForwarder,
		Logger:      s.logger,
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

// applyRoutes applies routes from configuration
func (s *Server) applyRoutes() error {
	var desired []route.Route

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

		desired = append(desired, route.Route{
			Destination: ipNet,
			Interface:   s.cfg.TUN.Name,
			Comment:     r.Comment,
			Enabled:     true,
		})
	}

	return s.routeMgr.Sync(desired)
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

	// Reapply routes
	if err := s.applyRoutes(); err != nil {
		s.logger.Warn("failed to reapply routes", zap.Error(err))
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
