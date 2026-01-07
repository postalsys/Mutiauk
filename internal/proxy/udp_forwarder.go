package proxy

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/coinstash/mutiauk/internal/nat"
	"github.com/coinstash/mutiauk/internal/socks5"
	"go.uber.org/zap"
)

// UDPForwarder forwards UDP packets through SOCKS5 UDP ASSOCIATE
type UDPForwarder struct {
	mu       sync.RWMutex
	client   *socks5.Client
	natTable *nat.Table
	logger   *zap.Logger

	// Relay pool for reusing UDP ASSOCIATE sessions
	relayPool map[string]*relayEntry
	poolMu    sync.Mutex

	bufferSize  int
	relayTTL    time.Duration
}

type relayEntry struct {
	relay    *socks5.UDPRelay
	lastUsed time.Time
}

// NewUDPForwarder creates a new UDP forwarder
func NewUDPForwarder(client *socks5.Client, natTable *nat.Table, logger *zap.Logger) *UDPForwarder {
	f := &UDPForwarder{
		client:     client,
		natTable:   natTable,
		logger:     logger,
		relayPool:  make(map[string]*relayEntry),
		bufferSize: 65535,
		relayTTL:   5 * time.Minute,
	}

	// Start relay pool cleanup goroutine
	go f.cleanupRelayPool()

	return f
}

// UpdateClient updates the SOCKS5 client
func (f *UDPForwarder) UpdateClient(client *socks5.Client) {
	f.mu.Lock()
	f.client = client
	f.mu.Unlock()

	// Clear relay pool on client change
	f.poolMu.Lock()
	for _, entry := range f.relayPool {
		entry.relay.Close()
	}
	f.relayPool = make(map[string]*relayEntry)
	f.poolMu.Unlock()
}

// HandleUDP handles a UDP packet from the TUN interface
func (f *UDPForwarder) HandleUDP(ctx context.Context, conn net.PacketConn, srcAddr, dstAddr net.Addr) error {
	defer conn.Close()

	f.mu.RLock()
	client := f.client
	f.mu.RUnlock()

	srcUDP := srcAddr.(*net.UDPAddr)
	dstUDP := dstAddr.(*net.UDPAddr)

	srcAddrPort := netip.MustParseAddrPort(srcUDP.String())
	dstAddrPort := netip.MustParseAddrPort(dstUDP.String())

	// Get or create a relay for this source
	relay, err := f.getOrCreateRelay(ctx, client, srcUDP)
	if err != nil {
		f.logger.Error("failed to get UDP relay",
			zap.String("src", srcAddr.String()),
			zap.Error(err),
		)
		return fmt.Errorf("failed to get UDP relay: %w", err)
	}

	// Create NAT entry
	entry := &nat.Entry{
		Protocol: "udp",
		SrcAddr:  srcAddrPort,
		DstAddr:  dstAddrPort,
		UDPRelay: relay,
		State:    nat.ConnStateEstablished,
	}

	if err := f.natTable.Insert(entry); err != nil {
		f.logger.Debug("NAT entry already exists", zap.Error(err))
	}
	defer f.natTable.Remove("udp", srcAddrPort, dstAddrPort)

	f.logger.Debug("UDP forwarding started",
		zap.String("src", srcAddr.String()),
		zap.String("dst", dstAddr.String()),
	)

	// Forward packets bidirectionally
	return f.relay(ctx, conn, relay, srcUDP, dstUDP)
}

// getOrCreateRelay gets an existing relay or creates a new one
func (f *UDPForwarder) getOrCreateRelay(ctx context.Context, client *socks5.Client, localAddr *net.UDPAddr) (*socks5.UDPRelay, error) {
	key := localAddr.String()

	f.poolMu.Lock()
	defer f.poolMu.Unlock()

	if entry, ok := f.relayPool[key]; ok {
		entry.lastUsed = time.Now()
		return entry.relay, nil
	}

	// Create new relay
	relay, err := client.UDPAssociate(ctx, localAddr)
	if err != nil {
		return nil, err
	}

	f.relayPool[key] = &relayEntry{
		relay:    relay,
		lastUsed: time.Now(),
	}

	return relay, nil
}

// relay performs bidirectional UDP forwarding
func (f *UDPForwarder) relay(ctx context.Context, local net.PacketConn, relay *socks5.UDPRelay, srcUDP, dstUDP *net.UDPAddr) error {
	errCh := make(chan error, 2)

	// Create target address for SOCKS5
	var target *socks5.Address
	if ip4 := dstUDP.IP.To4(); ip4 != nil {
		target = &socks5.Address{
			Type: socks5.AddrTypeIPv4,
			IP:   ip4,
			Port: uint16(dstUDP.Port),
		}
	} else {
		target = &socks5.Address{
			Type: socks5.AddrTypeIPv6,
			IP:   dstUDP.IP,
			Port: uint16(dstUDP.Port),
		}
	}

	// Local -> Relay (via SOCKS5)
	go func() {
		buf := make([]byte, f.bufferSize)
		for {
			select {
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			default:
			}

			n, _, err := local.ReadFrom(buf)
			if err != nil {
				errCh <- err
				return
			}

			if err := relay.Send(buf[:n], target); err != nil {
				errCh <- err
				return
			}
		}
	}()

	// Relay -> Local (via SOCKS5)
	go func() {
		buf := make([]byte, f.bufferSize)
		for {
			select {
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			default:
			}

			n, remoteAddr, err := relay.Receive(buf)
			if err != nil {
				errCh <- err
				return
			}

			// Convert remote address to UDP address
			var udpAddr *net.UDPAddr
			if remoteAddr.IP != nil {
				udpAddr = &net.UDPAddr{
					IP:   remoteAddr.IP,
					Port: int(remoteAddr.Port),
				}
			} else {
				// Domain - need to resolve
				ips, err := net.LookupIP(remoteAddr.Domain)
				if err != nil || len(ips) == 0 {
					continue
				}
				udpAddr = &net.UDPAddr{
					IP:   ips[0],
					Port: int(remoteAddr.Port),
				}
			}

			// Send response back to original source
			if _, err := local.WriteTo(buf[:n], srcUDP); err != nil {
				f.logger.Debug("failed to write UDP response",
					zap.String("src", udpAddr.String()),
					zap.Error(err),
				)
			}
		}
	}()

	// Wait for completion
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// cleanupRelayPool periodically cleans up old relay entries
func (f *UDPForwarder) cleanupRelayPool() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		f.poolMu.Lock()
		now := time.Now()
		for key, entry := range f.relayPool {
			if now.Sub(entry.lastUsed) > f.relayTTL {
				entry.relay.Close()
				delete(f.relayPool, key)
			}
		}
		f.poolMu.Unlock()
	}
}

// Close closes all relays
func (f *UDPForwarder) Close() {
	f.poolMu.Lock()
	defer f.poolMu.Unlock()

	for _, entry := range f.relayPool {
		entry.relay.Close()
	}
	f.relayPool = make(map[string]*relayEntry)
}
