package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"

	"github.com/coinstash/mutiauk/internal/nat"
	"github.com/coinstash/mutiauk/internal/socks5"
	"go.uber.org/zap"
)

// TCPForwarder forwards TCP connections through SOCKS5
type TCPForwarder struct {
	mu       sync.RWMutex
	client   *socks5.Client
	natTable *nat.Table
	logger   *zap.Logger

	bufferSize int
}

// NewTCPForwarder creates a new TCP forwarder
func NewTCPForwarder(client *socks5.Client, natTable *nat.Table, logger *zap.Logger) *TCPForwarder {
	return &TCPForwarder{
		client:     client,
		natTable:   natTable,
		logger:     logger,
		bufferSize: 32 * 1024, // 32KB buffer
	}
}

// UpdateClient updates the SOCKS5 client
func (f *TCPForwarder) UpdateClient(client *socks5.Client) {
	f.mu.Lock()
	f.client = client
	f.mu.Unlock()
}

// HandleTCP handles a TCP connection from the TUN interface
func (f *TCPForwarder) HandleTCP(ctx context.Context, conn net.Conn, srcAddr, dstAddr net.Addr) error {
	defer conn.Close()

	f.mu.RLock()
	client := f.client
	f.mu.RUnlock()

	// Parse addresses for NAT tracking
	srcTCP := srcAddr.(*net.TCPAddr)
	dstTCP := dstAddr.(*net.TCPAddr)

	srcAddrPort := netip.MustParseAddrPort(srcTCP.String())
	dstAddrPort := netip.MustParseAddrPort(dstTCP.String())

	// Check for existing NAT entry
	entry, exists := f.natTable.Lookup("tcp", srcAddrPort, dstAddrPort)
	if exists && entry.ProxyConn != nil {
		// Use existing connection (shouldn't happen for TCP, but handle it)
		f.logger.Debug("using existing NAT entry",
			zap.String("src", srcAddr.String()),
			zap.String("dst", dstAddr.String()),
		)
	}

	// Connect to destination via SOCKS5
	targetAddr := dstTCP.String()
	proxyConn, err := client.Connect(ctx, targetAddr)
	if err != nil {
		f.logger.Error("SOCKS5 connect failed",
			zap.String("src", srcAddr.String()),
			zap.String("dst", dstAddr.String()),
			zap.Error(err),
		)
		return fmt.Errorf("SOCKS5 connect failed: %w", err)
	}
	defer proxyConn.Close()

	// Create NAT entry
	entry = &nat.Entry{
		Protocol:  "tcp",
		SrcAddr:   srcAddrPort,
		DstAddr:   dstAddrPort,
		ProxyConn: proxyConn,
		State:     nat.ConnStateEstablished,
	}

	if err := f.natTable.Insert(entry); err != nil {
		f.logger.Debug("failed to insert NAT entry (may already exist)",
			zap.Error(err),
		)
	}
	defer f.natTable.Remove("tcp", srcAddrPort, dstAddrPort)

	f.logger.Debug("TCP forwarding established",
		zap.String("src", srcAddr.String()),
		zap.String("dst", dstAddr.String()),
	)

	// Bidirectional copy
	return f.relay(ctx, conn, proxyConn)
}

// relay performs bidirectional data transfer
func (f *TCPForwarder) relay(ctx context.Context, local, remote net.Conn) error {
	errCh := make(chan error, 2)

	// Local -> Remote
	go func() {
		_, err := io.CopyBuffer(remote, local, make([]byte, f.bufferSize))
		errCh <- err
	}()

	// Remote -> Local
	go func() {
		_, err := io.CopyBuffer(local, remote, make([]byte, f.bufferSize))
		errCh <- err
	}()

	// Wait for either direction to complete or context to cancel
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		// One direction completed, wait briefly for the other
		return err
	}
}
