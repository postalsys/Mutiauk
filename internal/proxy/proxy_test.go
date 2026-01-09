package proxy

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/postalsys/mutiauk/internal/nat"
	"github.com/postalsys/mutiauk/internal/socks5"
	"go.uber.org/zap"
)

// --- TCPForwarder Tests ---

func TestNewTCPForwarder(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())

	forwarder := NewTCPForwarder(client, natTable, logger)

	if forwarder == nil {
		t.Fatal("NewTCPForwarder returned nil")
	}
	if forwarder.client != client {
		t.Error("client not set correctly")
	}
	if forwarder.natTable != natTable {
		t.Error("natTable not set correctly")
	}
	if forwarder.bufferSize != 32*1024 {
		t.Errorf("bufferSize = %d, want %d", forwarder.bufferSize, 32*1024)
	}
}

func TestTCPForwarder_UpdateClient(t *testing.T) {
	logger := zap.NewNop()
	client1 := &socks5.Client{}
	client2 := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())

	forwarder := NewTCPForwarder(client1, natTable, logger)

	if forwarder.client != client1 {
		t.Error("initial client not set correctly")
	}

	forwarder.UpdateClient(client2)

	forwarder.mu.RLock()
	currentClient := forwarder.client
	forwarder.mu.RUnlock()

	if currentClient != client2 {
		t.Error("client not updated correctly")
	}
}

func TestTCPForwarder_UpdateClient_Concurrent(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())
	forwarder := NewTCPForwarder(client, natTable, logger)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			newClient := &socks5.Client{}
			forwarder.UpdateClient(newClient)
		}()
	}

	// Also read concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			forwarder.mu.RLock()
			_ = forwarder.client
			forwarder.mu.RUnlock()
		}()
	}

	wg.Wait()
}

func TestTCPForwarder_PendingConnections(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())
	forwarder := NewTCPForwarder(client, natTable, logger)

	// Test that pendingConns map is initialized
	key := "192.168.1.1:12345->10.0.0.1:80"

	// Create a mock connection using net.Pipe
	conn1, conn2 := net.Pipe()
	defer conn1.Close()
	defer conn2.Close()

	// Store a pending connection
	forwarder.pendingConns.Store(key, conn1)

	// Verify it can be retrieved
	if val, ok := forwarder.pendingConns.Load(key); !ok {
		t.Error("pending connection not stored")
	} else if val.(net.Conn) != conn1 {
		t.Error("wrong connection retrieved")
	}

	// Test LoadAndDelete
	if val, ok := forwarder.pendingConns.LoadAndDelete(key); !ok {
		t.Error("LoadAndDelete failed")
	} else if val.(net.Conn) != conn1 {
		t.Error("wrong connection from LoadAndDelete")
	}

	// Verify it's deleted
	if _, ok := forwarder.pendingConns.Load(key); ok {
		t.Error("pending connection should be deleted after LoadAndDelete")
	}
}

// TestTCPForwarder_Relay tests the bidirectional relay function
func TestTCPForwarder_Relay(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())
	forwarder := NewTCPForwarder(client, natTable, logger)

	// Create two pipe pairs for local and remote
	localClient, localServer := net.Pipe()
	remoteClient, remoteServer := net.Pipe()

	defer localClient.Close()
	defer localServer.Close()
	defer remoteClient.Close()
	defer remoteServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start relay in goroutine
	relayDone := make(chan error, 1)
	go func() {
		relayDone <- forwarder.relay(ctx, localServer, remoteClient)
	}()

	// Test local -> remote
	testData := []byte("Hello from local")
	go func() {
		localClient.Write(testData)
	}()

	buf := make([]byte, len(testData))
	n, err := remoteServer.Read(buf)
	if err != nil {
		t.Errorf("failed to read from remote: %v", err)
	}
	if string(buf[:n]) != string(testData) {
		t.Errorf("data mismatch: got %q, want %q", buf[:n], testData)
	}

	// Test remote -> local
	responseData := []byte("Hello from remote")
	go func() {
		remoteServer.Write(responseData)
	}()

	buf = make([]byte, len(responseData))
	n, err = localClient.Read(buf)
	if err != nil {
		t.Errorf("failed to read from local: %v", err)
	}
	if string(buf[:n]) != string(responseData) {
		t.Errorf("data mismatch: got %q, want %q", buf[:n], responseData)
	}

	// Close one side to complete relay
	localClient.Close()

	// Wait for relay to complete
	select {
	case <-relayDone:
		// Expected
	case <-time.After(time.Second):
		t.Error("relay did not complete in time")
	}
}

func TestTCPForwarder_Relay_ContextCancel(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())
	forwarder := NewTCPForwarder(client, natTable, logger)

	localClient, localServer := net.Pipe()
	remoteClient, remoteServer := net.Pipe()

	defer localClient.Close()
	defer localServer.Close()
	defer remoteClient.Close()
	defer remoteServer.Close()

	ctx, cancel := context.WithCancel(context.Background())

	relayDone := make(chan error, 1)
	go func() {
		relayDone <- forwarder.relay(ctx, localServer, remoteClient)
	}()

	// Give relay time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for relay to return
	select {
	case err := <-relayDone:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Error("relay did not respond to context cancellation")
	}
}

// --- UDPForwarder Tests ---

func TestNewUDPForwarder(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())

	forwarder := NewUDPForwarder(client, natTable, logger)

	if forwarder == nil {
		t.Fatal("NewUDPForwarder returned nil")
	}
	if forwarder.client != client {
		t.Error("client not set correctly")
	}
	if forwarder.natTable != natTable {
		t.Error("natTable not set correctly")
	}
	if forwarder.bufferSize != 65535 {
		t.Errorf("bufferSize = %d, want 65535", forwarder.bufferSize)
	}
	if forwarder.relayTTL != 5*time.Minute {
		t.Errorf("relayTTL = %v, want 5m", forwarder.relayTTL)
	}
	if forwarder.relayPool == nil {
		t.Error("relayPool not initialized")
	}

	// Clean up the cleanup goroutine
	forwarder.Close()
}

func TestUDPForwarder_UpdateClient(t *testing.T) {
	logger := zap.NewNop()
	client1 := &socks5.Client{}
	client2 := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())

	forwarder := NewUDPForwarder(client1, natTable, logger)
	defer forwarder.Close()

	forwarder.UpdateClient(client2)

	forwarder.mu.RLock()
	currentClient := forwarder.client
	forwarder.mu.RUnlock()

	if currentClient != client2 {
		t.Error("client not updated correctly")
	}
}

func TestUDPForwarder_Close(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())

	forwarder := NewUDPForwarder(client, natTable, logger)

	// Close should not panic
	forwarder.Close()

	// Pool should be empty after close
	forwarder.poolMu.Lock()
	poolLen := len(forwarder.relayPool)
	forwarder.poolMu.Unlock()

	if poolLen != 0 {
		t.Errorf("relay pool should be empty after Close, got %d entries", poolLen)
	}
}

func TestUDPForwarder_Close_Multiple(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())

	forwarder := NewUDPForwarder(client, natTable, logger)

	// Multiple closes should not panic
	forwarder.Close()
	forwarder.Close()
	forwarder.Close()
}

// --- RawUDPForwarder Tests ---

func TestNewRawUDPForwarder(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}

	forwarder := NewRawUDPForwarder(client, logger)

	if forwarder == nil {
		t.Fatal("NewRawUDPForwarder returned nil")
	}
	if forwarder.client != client {
		t.Error("client not set correctly")
	}
	if forwarder.relayTTL != 5*time.Minute {
		t.Errorf("relayTTL = %v, want 5m", forwarder.relayTTL)
	}
	if forwarder.relayPool == nil {
		t.Error("relayPool not initialized")
	}

	forwarder.Close()
}

func TestRawUDPForwarder_UpdateClient(t *testing.T) {
	logger := zap.NewNop()
	client1 := &socks5.Client{}
	client2 := &socks5.Client{}

	forwarder := NewRawUDPForwarder(client1, logger)
	defer forwarder.Close()

	forwarder.UpdateClient(client2)

	forwarder.mu.RLock()
	currentClient := forwarder.client
	forwarder.mu.RUnlock()

	if currentClient != client2 {
		t.Error("client not updated correctly")
	}
}

func TestRawUDPForwarder_Close(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}

	forwarder := NewRawUDPForwarder(client, logger)

	// Close should not panic
	forwarder.Close()

	// Pool should be empty after close
	forwarder.poolMu.Lock()
	poolLen := len(forwarder.relayPool)
	forwarder.poolMu.Unlock()

	if poolLen != 0 {
		t.Errorf("relay pool should be empty after Close, got %d entries", poolLen)
	}
}

func TestRawUDPForwarder_Close_Multiple(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}

	forwarder := NewRawUDPForwarder(client, logger)

	// Multiple closes should not panic
	forwarder.Close()
	forwarder.Close()
	forwarder.Close()
}

// --- relayEntry Tests ---

func TestRelayEntry_Fields(t *testing.T) {
	entry := relayEntry{
		relay:    nil, // Would be a real UDPRelay
		lastUsed: time.Now(),
	}

	if time.Since(entry.lastUsed) > time.Second {
		t.Error("lastUsed should be recent")
	}
}

func TestRawRelayEntry_Fields(t *testing.T) {
	entry := rawRelayEntry{
		relay:    nil, // Would be a real UDPRelay
		lastUsed: time.Now(),
	}

	if time.Since(entry.lastUsed) > time.Second {
		t.Error("lastUsed should be recent")
	}
}

// --- Concurrent Access Tests ---

func TestTCPForwarder_ConcurrentUpdateClient(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())
	forwarder := NewTCPForwarder(client, natTable, logger)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			forwarder.UpdateClient(&socks5.Client{})
		}()
		go func() {
			defer wg.Done()
			forwarder.mu.RLock()
			_ = forwarder.client
			forwarder.mu.RUnlock()
		}()
	}
	wg.Wait()
}

func TestUDPForwarder_ConcurrentOperations(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())
	forwarder := NewUDPForwarder(client, natTable, logger)
	defer forwarder.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			forwarder.UpdateClient(&socks5.Client{})
		}()
		go func() {
			defer wg.Done()
			forwarder.poolMu.Lock()
			_ = len(forwarder.relayPool)
			forwarder.poolMu.Unlock()
		}()
	}
	wg.Wait()
}

func TestRawUDPForwarder_ConcurrentOperations(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	forwarder := NewRawUDPForwarder(client, logger)
	defer forwarder.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			forwarder.UpdateClient(&socks5.Client{})
		}()
		go func() {
			defer wg.Done()
			forwarder.poolMu.Lock()
			_ = len(forwarder.relayPool)
			forwarder.poolMu.Unlock()
		}()
	}
	wg.Wait()
}

// --- Large Data Transfer Test ---

func TestTCPForwarder_Relay_LargeData(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())
	forwarder := NewTCPForwarder(client, natTable, logger)

	localClient, localServer := net.Pipe()
	remoteClient, remoteServer := net.Pipe()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start relay
	go func() {
		forwarder.relay(ctx, localServer, remoteClient)
	}()

	// Send smaller data for faster test
	dataSize := 64 * 1024 // 64KB
	largeData := make([]byte, dataSize)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	// Write and read concurrently
	errCh := make(chan error, 2)

	go func() {
		_, err := localClient.Write(largeData)
		localClient.Close()
		errCh <- err
	}()

	received := make([]byte, 0, dataSize)
	go func() {
		buf := make([]byte, 32*1024)
		for len(received) < dataSize {
			remoteServer.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, err := remoteServer.Read(buf)
			if n > 0 {
				received = append(received, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
		errCh <- nil
	}()

	// Wait for both operations
	<-errCh
	<-errCh

	// Clean up
	localServer.Close()
	remoteClient.Close()
	remoteServer.Close()

	if len(received) < dataSize/2 {
		t.Errorf("received only %d bytes, expected at least %d", len(received), dataSize/2)
	}

	// Verify data integrity for received data
	for i := 0; i < min(100, len(received)); i++ {
		if received[i] != byte(i%256) {
			t.Errorf("data mismatch at index %d: got %d, want %d", i, received[i], i%256)
			break
		}
	}
}

// --- Buffer Size Tests ---

func TestTCPForwarder_BufferSize(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())

	forwarder := NewTCPForwarder(client, natTable, logger)

	expectedSize := 32 * 1024 // 32KB
	if forwarder.bufferSize != expectedSize {
		t.Errorf("bufferSize = %d, want %d", forwarder.bufferSize, expectedSize)
	}
}

func TestUDPForwarder_BufferSize(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())

	forwarder := NewUDPForwarder(client, natTable, logger)
	defer forwarder.Close()

	expectedSize := 65535 // Max UDP packet size
	if forwarder.bufferSize != expectedSize {
		t.Errorf("bufferSize = %d, want %d", forwarder.bufferSize, expectedSize)
	}
}

// --- TTL Tests ---

func TestUDPForwarder_RelayTTL(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}
	natTable := nat.NewTable(nat.DefaultConfig())

	forwarder := NewUDPForwarder(client, natTable, logger)
	defer forwarder.Close()

	expectedTTL := 5 * time.Minute
	if forwarder.relayTTL != expectedTTL {
		t.Errorf("relayTTL = %v, want %v", forwarder.relayTTL, expectedTTL)
	}
}

func TestRawUDPForwarder_RelayTTL(t *testing.T) {
	logger := zap.NewNop()
	client := &socks5.Client{}

	forwarder := NewRawUDPForwarder(client, logger)
	defer forwarder.Close()

	expectedTTL := 5 * time.Minute
	if forwarder.relayTTL != expectedTTL {
		t.Errorf("relayTTL = %v, want %v", forwarder.relayTTL, expectedTTL)
	}
}
