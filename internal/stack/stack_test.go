//go:build linux

package stack

import (
	"context"
	"net"
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip/header"
)

// --- BuildUDPResponse Tests ---

func TestBuildUDPResponse_Basic(t *testing.T) {
	srcIP := net.ParseIP("192.168.1.1").To4()
	dstIP := net.ParseIP("10.0.0.1").To4()
	srcPort := uint16(12345)
	dstPort := uint16(53)
	payload := []byte("test payload")

	pkt := BuildUDPResponse(srcIP, dstIP, srcPort, dstPort, payload)

	// Verify packet length
	expectedLen := header.IPv4MinimumSize + header.UDPMinimumSize + len(payload)
	if len(pkt) != expectedLen {
		t.Errorf("packet length = %d, want %d", len(pkt), expectedLen)
	}

	// Parse and verify IP header
	ipHdr := header.IPv4(pkt)

	// Check version via first nibble
	version := pkt[0] >> 4
	if version != 4 {
		t.Errorf("IP version = %d, want 4", version)
	}

	if ipHdr.HeaderLength() != header.IPv4MinimumSize {
		t.Errorf("IP header length = %d, want %d", ipHdr.HeaderLength(), header.IPv4MinimumSize)
	}

	if ipHdr.TotalLength() != uint16(expectedLen) {
		t.Errorf("IP total length = %d, want %d", ipHdr.TotalLength(), expectedLen)
	}

	if ipHdr.TTL() != 64 {
		t.Errorf("TTL = %d, want 64", ipHdr.TTL())
	}

	if ipHdr.Protocol() != uint8(header.UDPProtocolNumber) {
		t.Errorf("protocol = %d, want %d (UDP)", ipHdr.Protocol(), header.UDPProtocolNumber)
	}

	srcAddr := ipHdr.SourceAddress()
	gotSrcIP := net.IP(srcAddr.AsSlice())
	if !gotSrcIP.Equal(srcIP) {
		t.Errorf("source IP = %v, want %v", gotSrcIP, srcIP)
	}

	dstAddr := ipHdr.DestinationAddress()
	gotDstIP := net.IP(dstAddr.AsSlice())
	if !gotDstIP.Equal(dstIP) {
		t.Errorf("destination IP = %v, want %v", gotDstIP, dstIP)
	}

	// Verify IP checksum is valid
	if ipHdr.CalculateChecksum() != 0xffff {
		t.Error("IP checksum is invalid")
	}
}

func TestBuildUDPResponse_UDPHeader(t *testing.T) {
	srcIP := net.ParseIP("10.10.10.10").To4()
	dstIP := net.ParseIP("20.20.20.20").To4()
	srcPort := uint16(8080)
	dstPort := uint16(443)
	payload := []byte("hello world")

	pkt := BuildUDPResponse(srcIP, dstIP, srcPort, dstPort, payload)

	// Parse UDP header
	udpHdr := header.UDP(pkt[header.IPv4MinimumSize:])

	if udpHdr.SourcePort() != srcPort {
		t.Errorf("UDP source port = %d, want %d", udpHdr.SourcePort(), srcPort)
	}

	if udpHdr.DestinationPort() != dstPort {
		t.Errorf("UDP destination port = %d, want %d", udpHdr.DestinationPort(), dstPort)
	}

	expectedUDPLen := header.UDPMinimumSize + len(payload)
	if udpHdr.Length() != uint16(expectedUDPLen) {
		t.Errorf("UDP length = %d, want %d", udpHdr.Length(), expectedUDPLen)
	}

	// Verify payload
	gotPayload := pkt[header.IPv4MinimumSize+header.UDPMinimumSize:]
	if string(gotPayload) != string(payload) {
		t.Errorf("payload = %q, want %q", gotPayload, payload)
	}
}

func TestBuildUDPResponse_EmptyPayload(t *testing.T) {
	srcIP := net.ParseIP("1.2.3.4").To4()
	dstIP := net.ParseIP("5.6.7.8").To4()
	srcPort := uint16(1234)
	dstPort := uint16(5678)
	payload := []byte{}

	pkt := BuildUDPResponse(srcIP, dstIP, srcPort, dstPort, payload)

	expectedLen := header.IPv4MinimumSize + header.UDPMinimumSize
	if len(pkt) != expectedLen {
		t.Errorf("packet length = %d, want %d", len(pkt), expectedLen)
	}

	// Verify UDP length
	udpHdr := header.UDP(pkt[header.IPv4MinimumSize:])
	if udpHdr.Length() != header.UDPMinimumSize {
		t.Errorf("UDP length = %d, want %d", udpHdr.Length(), header.UDPMinimumSize)
	}
}

func TestBuildUDPResponse_LargePayload(t *testing.T) {
	srcIP := net.ParseIP("192.168.0.1").To4()
	dstIP := net.ParseIP("192.168.0.2").To4()
	srcPort := uint16(9999)
	dstPort := uint16(8888)

	// Create 1000 byte payload
	payload := make([]byte, 1000)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	pkt := BuildUDPResponse(srcIP, dstIP, srcPort, dstPort, payload)

	expectedLen := header.IPv4MinimumSize + header.UDPMinimumSize + len(payload)
	if len(pkt) != expectedLen {
		t.Errorf("packet length = %d, want %d", len(pkt), expectedLen)
	}

	// Verify payload integrity
	gotPayload := pkt[header.IPv4MinimumSize+header.UDPMinimumSize:]
	for i := range payload {
		if gotPayload[i] != payload[i] {
			t.Errorf("payload byte %d = %d, want %d", i, gotPayload[i], payload[i])
			break
		}
	}
}

func TestBuildUDPResponse_DifferentAddresses(t *testing.T) {
	tests := []struct {
		name    string
		srcIP   string
		dstIP   string
		srcPort uint16
		dstPort uint16
	}{
		{
			name:    "localhost addresses",
			srcIP:   "127.0.0.1",
			dstIP:   "127.0.0.2",
			srcPort: 1000,
			dstPort: 2000,
		},
		{
			name:    "private network",
			srcIP:   "10.0.0.1",
			dstIP:   "10.255.255.254",
			srcPort: 80,
			dstPort: 443,
		},
		{
			name:    "public addresses",
			srcIP:   "8.8.8.8",
			dstIP:   "1.1.1.1",
			srcPort: 53,
			dstPort: 53,
		},
		{
			name:    "edge ports",
			srcIP:   "192.168.1.1",
			dstIP:   "192.168.1.2",
			srcPort: 1,     // minimum
			dstPort: 65535, // maximum
		},
		{
			name:    "zero port",
			srcIP:   "10.0.0.1",
			dstIP:   "10.0.0.2",
			srcPort: 0,
			dstPort: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srcIP := net.ParseIP(tt.srcIP).To4()
			dstIP := net.ParseIP(tt.dstIP).To4()
			payload := []byte("test")

			pkt := BuildUDPResponse(srcIP, dstIP, tt.srcPort, tt.dstPort, payload)

			// Verify IP addresses
			ipHdr := header.IPv4(pkt)
			srcAddr := ipHdr.SourceAddress()
			dstAddr := ipHdr.DestinationAddress()
			gotSrcIP := net.IP(srcAddr.AsSlice())
			gotDstIP := net.IP(dstAddr.AsSlice())

			if !gotSrcIP.Equal(srcIP) {
				t.Errorf("source IP = %v, want %v", gotSrcIP, srcIP)
			}
			if !gotDstIP.Equal(dstIP) {
				t.Errorf("destination IP = %v, want %v", gotDstIP, dstIP)
			}

			// Verify ports
			udpHdr := header.UDP(pkt[header.IPv4MinimumSize:])
			if udpHdr.SourcePort() != tt.srcPort {
				t.Errorf("source port = %d, want %d", udpHdr.SourcePort(), tt.srcPort)
			}
			if udpHdr.DestinationPort() != tt.dstPort {
				t.Errorf("destination port = %d, want %d", udpHdr.DestinationPort(), tt.dstPort)
			}

			// Verify checksum is valid (non-zero unless zero checksum is valid)
			if ipHdr.CalculateChecksum() != 0xffff {
				t.Error("IP checksum is invalid")
			}
		})
	}
}

// --- checksum Tests ---

func TestChecksum_Empty(t *testing.T) {
	result := checksum([]byte{}, 0)
	if result != 0 {
		t.Errorf("checksum of empty data = %d, want 0", result)
	}
}

func TestChecksum_SingleByte(t *testing.T) {
	// Single byte (odd length) should be padded
	data := []byte{0x45}
	result := checksum(data, 0)

	// 0x45 << 8 = 0x4500
	expected := uint16(0x4500)
	if result != expected {
		t.Errorf("checksum = 0x%04x, want 0x%04x", result, expected)
	}
}

func TestChecksum_TwoBytes(t *testing.T) {
	data := []byte{0x12, 0x34}
	result := checksum(data, 0)

	expected := uint16(0x1234)
	if result != expected {
		t.Errorf("checksum = 0x%04x, want 0x%04x", result, expected)
	}
}

func TestChecksum_FourBytes(t *testing.T) {
	data := []byte{0x00, 0x01, 0x00, 0x02}
	result := checksum(data, 0)

	// 0x0001 + 0x0002 = 0x0003
	expected := uint16(0x0003)
	if result != expected {
		t.Errorf("checksum = 0x%04x, want 0x%04x", result, expected)
	}
}

func TestChecksum_WithOverflow(t *testing.T) {
	// Test carry-over behavior
	data := []byte{0xFF, 0xFF, 0x00, 0x01}
	result := checksum(data, 0)

	// 0xFFFF + 0x0001 = 0x10000 -> fold to 0x0001
	expected := uint16(0x0001)
	if result != expected {
		t.Errorf("checksum = 0x%04x, want 0x%04x", result, expected)
	}
}

func TestChecksum_WithInitial(t *testing.T) {
	data := []byte{0x00, 0x10}
	initial := uint16(0x0005)
	result := checksum(data, initial)

	// 0x0005 + 0x0010 = 0x0015
	expected := uint16(0x0015)
	if result != expected {
		t.Errorf("checksum = 0x%04x, want 0x%04x", result, expected)
	}
}

func TestChecksum_OddLength(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03}
	result := checksum(data, 0)

	// 0x0102 + 0x0300 = 0x0402
	expected := uint16(0x0402)
	if result != expected {
		t.Errorf("checksum = 0x%04x, want 0x%04x", result, expected)
	}
}

func TestChecksum_AllZeros(t *testing.T) {
	data := make([]byte, 100)
	result := checksum(data, 0)

	if result != 0 {
		t.Errorf("checksum of zeros = 0x%04x, want 0", result)
	}
}

func TestChecksum_AllOnes(t *testing.T) {
	data := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	result := checksum(data, 0)

	// 0xFFFF + 0xFFFF = 0x1FFFE -> fold to 0xFFFF
	expected := uint16(0xFFFF)
	if result != expected {
		t.Errorf("checksum = 0x%04x, want 0x%04x", result, expected)
	}
}

func TestChecksum_LargeData(t *testing.T) {
	// Test with larger data to ensure no overflow issues
	data := make([]byte, 1000)
	for i := range data {
		data[i] = byte(i % 256)
	}

	// Should not panic
	result := checksum(data, 0)

	// Just verify it returns something reasonable (non-zero for this data)
	if result == 0 {
		t.Error("checksum of non-zero data should not be 0")
	}
}

// --- Config Tests ---

func TestConfig_DefaultMTU(t *testing.T) {
	cfg := Config{}
	if cfg.MTU != 0 {
		t.Errorf("default MTU = %d, want 0 (unset)", cfg.MTU)
	}
}

func TestConfig_FieldTypes(t *testing.T) {
	// Verify Config can hold expected values
	cfg := Config{
		MTU:      1500,
		IPv4Addr: net.ParseIP("10.0.0.1"),
		IPv4Mask: net.CIDRMask(24, 32),
		IPv6Addr: net.ParseIP("::1"),
		IPv6Mask: net.CIDRMask(64, 128),
	}

	if cfg.MTU != 1500 {
		t.Errorf("MTU = %d, want 1500", cfg.MTU)
	}

	if !cfg.IPv4Addr.Equal(net.ParseIP("10.0.0.1")) {
		t.Errorf("IPv4Addr = %v, want 10.0.0.1", cfg.IPv4Addr)
	}

	ones, bits := cfg.IPv4Mask.Size()
	if ones != 24 || bits != 32 {
		t.Errorf("IPv4Mask = /%d (bits=%d), want /24 (bits=32)", ones, bits)
	}
}

// --- Interface Tests (compile-time verification) ---

// Verify interfaces are correctly defined

type mockTCPHandler struct{}

func (m *mockTCPHandler) HandleTCP(ctx context.Context, conn net.Conn, srcAddr, dstAddr net.Addr) error {
	return nil
}

type mockTCPPreConnector struct{}

func (m *mockTCPPreConnector) HandleTCP(ctx context.Context, conn net.Conn, srcAddr, dstAddr net.Addr) error {
	return nil
}

func (m *mockTCPPreConnector) PreConnect(ctx context.Context, srcAddr, dstAddr net.Addr) (net.Conn, error) {
	return nil, nil
}

type mockUDPHandler struct{}

func (m *mockUDPHandler) HandleUDP(ctx context.Context, conn net.PacketConn, srcAddr, dstAddr net.Addr) error {
	return nil
}

type mockRawUDPHandler struct{}

func (m *mockRawUDPHandler) HandleRawUDP(ctx context.Context, srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) ([]byte, error) {
	return nil, nil
}

type mockUDPPacketHandler struct{}

func (m *mockUDPPacketHandler) HandleUDPPacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) {
}

func TestInterfaces_TCPHandler(t *testing.T) {
	var _ TCPHandler = &mockTCPHandler{}
}

func TestInterfaces_TCPPreConnector(t *testing.T) {
	var h TCPHandler = &mockTCPPreConnector{}
	_, ok := h.(TCPPreConnector)
	if !ok {
		t.Error("mockTCPPreConnector should implement TCPPreConnector")
	}
}

func TestInterfaces_UDPHandler(t *testing.T) {
	var _ UDPHandler = &mockUDPHandler{}
}

func TestInterfaces_RawUDPHandler(t *testing.T) {
	var _ RawUDPHandler = &mockRawUDPHandler{}
}

func TestInterfaces_UDPPacketHandler(t *testing.T) {
	var _ UDPPacketHandler = &mockUDPPacketHandler{}
}

// --- UDP Response Round-trip Test ---

func TestBuildUDPResponse_RoundTrip(t *testing.T) {
	// Build a packet and verify it can be fully parsed
	srcIP := net.ParseIP("172.16.0.1").To4()
	dstIP := net.ParseIP("172.16.0.2").To4()
	srcPort := uint16(5000)
	dstPort := uint16(6000)
	payload := []byte("DNS query simulation")

	pkt := BuildUDPResponse(srcIP, dstIP, srcPort, dstPort, payload)

	// Parse as IP packet
	ipHdr := header.IPv4(pkt)

	// Verify IP header fields - check version via first nibble
	version := pkt[0] >> 4
	if version != 4 {
		t.Fatalf("IP version = %d, want 4", version)
	}

	totalLen := ipHdr.TotalLength()
	if int(totalLen) != len(pkt) {
		t.Errorf("total length mismatch: header says %d, actual %d", totalLen, len(pkt))
	}

	// Parse UDP
	ipHdrLen := ipHdr.HeaderLength()
	udpHdr := header.UDP(pkt[ipHdrLen:])

	// Verify UDP fields match input
	if udpHdr.SourcePort() != srcPort {
		t.Errorf("source port = %d, want %d", udpHdr.SourcePort(), srcPort)
	}
	if udpHdr.DestinationPort() != dstPort {
		t.Errorf("dest port = %d, want %d", udpHdr.DestinationPort(), dstPort)
	}

	// Extract and verify payload
	udpPayload := pkt[int(ipHdrLen)+header.UDPMinimumSize:]
	if string(udpPayload) != string(payload) {
		t.Errorf("payload = %q, want %q", udpPayload, payload)
	}
}
