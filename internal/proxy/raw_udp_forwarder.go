package proxy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/coinstash/mutiauk/internal/socks5"
	"go.uber.org/zap"
)

// RawUDPForwarder handles raw UDP packet forwarding through SOCKS5
type RawUDPForwarder struct {
	mu     sync.RWMutex
	client *socks5.Client
	logger *zap.Logger

	// Relay pool for reusing UDP ASSOCIATE sessions
	relayPool map[string]*rawRelayEntry
	poolMu    sync.Mutex
	relayTTL  time.Duration
}

type rawRelayEntry struct {
	relay    *socks5.UDPRelay
	lastUsed time.Time
}

// NewRawUDPForwarder creates a new raw UDP forwarder
func NewRawUDPForwarder(client *socks5.Client, logger *zap.Logger) *RawUDPForwarder {
	f := &RawUDPForwarder{
		client:    client,
		logger:    logger,
		relayPool: make(map[string]*rawRelayEntry),
		relayTTL:  5 * time.Minute,
	}

	// Start relay pool cleanup goroutine
	go f.cleanupRelayPool()

	return f
}

// UpdateClient updates the SOCKS5 client
func (f *RawUDPForwarder) UpdateClient(client *socks5.Client) {
	f.mu.Lock()
	f.client = client
	f.mu.Unlock()

	// Clear relay pool on client change
	f.poolMu.Lock()
	for _, entry := range f.relayPool {
		entry.relay.Close()
	}
	f.relayPool = make(map[string]*rawRelayEntry)
	f.poolMu.Unlock()
}

// HandleRawUDP forwards a UDP packet through SOCKS5 and returns the response
func (f *RawUDPForwarder) HandleRawUDP(ctx context.Context, srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) ([]byte, error) {
	f.mu.RLock()
	client := f.client
	f.mu.RUnlock()

	// Get or create a relay
	relay, err := f.getOrCreateRelay(ctx, client, srcIP, srcPort)
	if err != nil {
		return nil, fmt.Errorf("failed to get UDP relay: %w", err)
	}

	// Create target address for SOCKS5
	var target *socks5.Address
	if ip4 := dstIP.To4(); ip4 != nil {
		target = &socks5.Address{
			Type: socks5.AddrTypeIPv4,
			IP:   ip4,
			Port: dstPort,
		}
	} else {
		target = &socks5.Address{
			Type: socks5.AddrTypeIPv6,
			IP:   dstIP,
			Port: dstPort,
		}
	}

	f.logger.Debug("sending UDP via SOCKS5",
		zap.String("src", fmt.Sprintf("%s:%d", srcIP, srcPort)),
		zap.String("dst", target.String()),
		zap.Int("payload_len", len(payload)),
	)

	// Send the packet
	if err := relay.Send(payload, target); err != nil {
		return nil, fmt.Errorf("failed to send UDP: %w", err)
	}

	// Wait for response with timeout
	f.logger.Debug("waiting for UDP response")
	relay.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 65535)
	n, remoteAddr, err := relay.Receive(buf)
	if err != nil {
		// Timeout is expected for some UDP - not all servers respond
		f.logger.Debug("UDP receive timeout or error",
			zap.Error(err),
		)
		return nil, nil // Return empty response, not error
	}

	f.logger.Debug("received UDP response via SOCKS5",
		zap.String("remote", remoteAddr.String()),
		zap.Int("len", n),
	)

	return buf[:n], nil
}

// getOrCreateRelay gets an existing relay or creates a new one
func (f *RawUDPForwarder) getOrCreateRelay(ctx context.Context, client *socks5.Client, srcIP net.IP, srcPort uint16) (*socks5.UDPRelay, error) {
	key := fmt.Sprintf("%s:%d", srcIP, srcPort)

	f.poolMu.Lock()
	defer f.poolMu.Unlock()

	if entry, ok := f.relayPool[key]; ok {
		entry.lastUsed = time.Now()
		return entry.relay, nil
	}

	// Create new relay
	relay, err := client.UDPAssociate(ctx, nil)
	if err != nil {
		return nil, err
	}

	f.relayPool[key] = &rawRelayEntry{
		relay:    relay,
		lastUsed: time.Now(),
	}

	f.logger.Debug("created new UDP relay",
		zap.String("key", key),
		zap.String("local", relay.LocalAddr().String()),
		zap.String("relay_addr", relay.RemoteAddr()),
	)

	return relay, nil
}

// cleanupRelayPool periodically cleans up old relay entries
func (f *RawUDPForwarder) cleanupRelayPool() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		f.poolMu.Lock()
		now := time.Now()
		for key, entry := range f.relayPool {
			if now.Sub(entry.lastUsed) > f.relayTTL {
				entry.relay.Close()
				delete(f.relayPool, key)
				f.logger.Debug("cleaned up stale UDP relay", zap.String("key", key))
			}
		}
		f.poolMu.Unlock()
	}
}

// Close closes all relays
func (f *RawUDPForwarder) Close() {
	f.poolMu.Lock()
	defer f.poolMu.Unlock()

	for _, entry := range f.relayPool {
		entry.relay.Close()
	}
	f.relayPool = make(map[string]*rawRelayEntry)
}
