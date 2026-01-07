package stack

import (
	"context"
	"fmt"
	"net"

	"github.com/coinstash/mutiauk/internal/tun"
	"go.uber.org/zap"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/fdbased"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

// TCPHandler handles TCP connections
type TCPHandler interface {
	HandleTCP(ctx context.Context, conn net.Conn, srcAddr, dstAddr net.Addr) error
}

// UDPHandler handles UDP packets
type UDPHandler interface {
	HandleUDP(ctx context.Context, conn net.PacketConn, srcAddr, dstAddr net.Addr) error
}

// Config holds stack configuration
type Config struct {
	MTU        int
	IPv4Addr   net.IP
	IPv4Mask   net.IPMask
	IPv6Addr   net.IP
	IPv6Mask   net.IPMask
	TCPHandler TCPHandler
	UDPHandler UDPHandler
	Logger     *zap.Logger
}

// Stack wraps gVisor's network stack
type Stack struct {
	stack      *stack.Stack
	tunDev     tun.Device
	linkEP     stack.LinkEndpoint
	cfg        Config
	logger     *zap.Logger
}

const (
	nicID = 1
)

// New creates a new network stack
func New(tunDev tun.Device, cfg Config) (*Stack, error) {
	if cfg.MTU == 0 {
		cfg.MTU = 1400
	}

	s := &Stack{
		tunDev: tunDev,
		cfg:    cfg,
		logger: cfg.Logger,
	}

	// Create the gVisor stack
	opts := stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	}

	s.stack = stack.New(opts)

	// Create link endpoint from TUN device
	linkEP, err := fdbased.New(&fdbased.Options{
		FDs: []int{tunDev.File()},
		MTU: uint32(cfg.MTU),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create link endpoint: %w", err)
	}
	s.linkEP = linkEP

	// Create NIC
	if err := s.stack.CreateNIC(nicID, linkEP); err != nil {
		return nil, fmt.Errorf("failed to create NIC: %s", err)
	}

	// Add IPv4 address
	if cfg.IPv4Addr != nil {
		addr := tcpip.AddrFrom4Slice(cfg.IPv4Addr.To4())
		ones, _ := cfg.IPv4Mask.Size()
		protoAddr := tcpip.ProtocolAddress{
			Protocol:          ipv4.ProtocolNumber,
			AddressWithPrefix: tcpip.AddressWithPrefix{Address: addr, PrefixLen: ones},
		}
		if err := s.stack.AddProtocolAddress(nicID, protoAddr, stack.AddressProperties{}); err != nil {
			return nil, fmt.Errorf("failed to add IPv4 address: %s", err)
		}
	}

	// Add IPv6 address
	if cfg.IPv6Addr != nil {
		addr := tcpip.AddrFrom16Slice(cfg.IPv6Addr.To16())
		ones, _ := cfg.IPv6Mask.Size()
		protoAddr := tcpip.ProtocolAddress{
			Protocol:          ipv6.ProtocolNumber,
			AddressWithPrefix: tcpip.AddressWithPrefix{Address: addr, PrefixLen: ones},
		}
		if err := s.stack.AddProtocolAddress(nicID, protoAddr, stack.AddressProperties{}); err != nil {
			return nil, fmt.Errorf("failed to add IPv6 address: %s", err)
		}
	}

	// Set default routes (catch-all)
	s.stack.SetRouteTable([]tcpip.Route{
		{
			Destination: header.IPv4EmptySubnet,
			NIC:         nicID,
		},
		{
			Destination: header.IPv6EmptySubnet,
			NIC:         nicID,
		},
	})

	// Enable forwarding
	s.stack.SetForwardingDefaultAndAllNICs(ipv4.ProtocolNumber, true)
	s.stack.SetForwardingDefaultAndAllNICs(ipv6.ProtocolNumber, true)

	return s, nil
}

// Run starts the network stack and handles connections
func (s *Stack) Run(ctx context.Context) error {
	// Set up TCP forwarder
	tcpForwarder := tcp.NewForwarder(s.stack, 0, 65535, func(r *tcp.ForwarderRequest) {
		s.handleTCPForward(ctx, r)
	})
	s.stack.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpForwarder.HandlePacket)

	// Set up UDP forwarder
	udpForwarder := udp.NewForwarder(s.stack, func(r *udp.ForwarderRequest) bool {
		s.handleUDPForward(ctx, r)
		return true // handled
	})
	s.stack.SetTransportProtocolHandler(udp.ProtocolNumber, udpForwarder.HandlePacket)

	s.logger.Info("network stack started")

	// Wait for context cancellation
	<-ctx.Done()

	return ctx.Err()
}

// handleTCPForward handles a forwarded TCP connection
func (s *Stack) handleTCPForward(ctx context.Context, r *tcp.ForwarderRequest) {
	id := r.ID()

	// Create endpoint
	var wq waiter.Queue
	ep, tcpErr := r.CreateEndpoint(&wq)
	if tcpErr != nil {
		s.logger.Error("failed to create TCP endpoint",
			zap.String("src", fmt.Sprintf("%s:%d", id.RemoteAddress, id.RemotePort)),
			zap.String("dst", fmt.Sprintf("%s:%d", id.LocalAddress, id.LocalPort)),
			zap.String("error", tcpErr.String()),
		)
		r.Complete(true)
		return
	}
	r.Complete(false)

	// Convert to net.Conn
	conn := gonet.NewTCPConn(&wq, ep)

	srcAddr := &net.TCPAddr{
		IP:   net.IP(id.RemoteAddress.AsSlice()),
		Port: int(id.RemotePort),
	}
	dstAddr := &net.TCPAddr{
		IP:   net.IP(id.LocalAddress.AsSlice()),
		Port: int(id.LocalPort),
	}

	s.logger.Debug("TCP connection",
		zap.String("src", srcAddr.String()),
		zap.String("dst", dstAddr.String()),
	)

	if s.cfg.TCPHandler != nil {
		go func() {
			if err := s.cfg.TCPHandler.HandleTCP(ctx, conn, srcAddr, dstAddr); err != nil {
				s.logger.Debug("TCP handler error",
					zap.String("src", srcAddr.String()),
					zap.String("dst", dstAddr.String()),
					zap.Error(err),
				)
			}
		}()
	} else {
		conn.Close()
	}
}

// handleUDPForward handles a forwarded UDP packet
func (s *Stack) handleUDPForward(ctx context.Context, r *udp.ForwarderRequest) {
	id := r.ID()

	// Create endpoint
	var wq waiter.Queue
	ep, tcpErr := r.CreateEndpoint(&wq)
	if tcpErr != nil {
		s.logger.Error("failed to create UDP endpoint",
			zap.String("src", fmt.Sprintf("%s:%d", id.RemoteAddress, id.RemotePort)),
			zap.String("dst", fmt.Sprintf("%s:%d", id.LocalAddress, id.LocalPort)),
			zap.String("error", tcpErr.String()),
		)
		return
	}

	// Convert to net.PacketConn
	conn := gonet.NewUDPConn(&wq, ep)

	srcAddr := &net.UDPAddr{
		IP:   net.IP(id.RemoteAddress.AsSlice()),
		Port: int(id.RemotePort),
	}
	dstAddr := &net.UDPAddr{
		IP:   net.IP(id.LocalAddress.AsSlice()),
		Port: int(id.LocalPort),
	}

	s.logger.Debug("UDP packet",
		zap.String("src", srcAddr.String()),
		zap.String("dst", dstAddr.String()),
	)

	if s.cfg.UDPHandler != nil {
		go func() {
			if err := s.cfg.UDPHandler.HandleUDP(ctx, conn, srcAddr, dstAddr); err != nil {
				s.logger.Debug("UDP handler error",
					zap.String("src", srcAddr.String()),
					zap.String("dst", dstAddr.String()),
					zap.Error(err),
				)
			}
		}()
	} else {
		conn.Close()
	}
}

// Close shuts down the stack
func (s *Stack) Close() error {
	if s.stack != nil {
		s.stack.Close()
	}
	return nil
}

// Stats returns stack statistics
func (s *Stack) Stats() tcpip.Stats {
	return s.stack.Stats()
}
