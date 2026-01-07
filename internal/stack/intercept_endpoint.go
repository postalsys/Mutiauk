package stack

import (
	"net"

	"go.uber.org/zap"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// UDPPacketHandler handles intercepted UDP packets
type UDPPacketHandler interface {
	HandleUDPPacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte)
}

// interceptEndpoint wraps a link endpoint to intercept UDP packets
type interceptEndpoint struct {
	stack.LinkEndpoint
	dispatcher  stack.NetworkDispatcher
	udpHandler  UDPPacketHandler
	tunWriter   func([]byte) (int, error)
	logger      *zap.Logger
}

// newInterceptEndpoint creates a new intercepting link endpoint
func newInterceptEndpoint(inner stack.LinkEndpoint, udpHandler UDPPacketHandler, tunWriter func([]byte) (int, error), logger *zap.Logger) *interceptEndpoint {
	return &interceptEndpoint{
		LinkEndpoint: inner,
		udpHandler:   udpHandler,
		tunWriter:    tunWriter,
		logger:       logger,
	}
}

// Attach implements stack.LinkEndpoint
func (e *interceptEndpoint) Attach(dispatcher stack.NetworkDispatcher) {
	e.dispatcher = dispatcher
	// Wrap the dispatcher to intercept packets
	e.LinkEndpoint.Attach(&interceptDispatcher{
		NetworkDispatcher: dispatcher,
		ep:                e,
	})
}

// interceptDispatcher wraps the network dispatcher to intercept packets
type interceptDispatcher struct {
	stack.NetworkDispatcher
	ep *interceptEndpoint
}

// DeliverNetworkPacket intercepts incoming packets
func (d *interceptDispatcher) DeliverNetworkPacket(protocol tcpip.NetworkProtocolNumber, pkt *stack.PacketBuffer) {
	d.ep.logger.Debug("packet received",
		zap.Uint32("protocol", uint32(protocol)),
		zap.Int("size", pkt.Size()),
	)

	// Only intercept IPv4 for now
	if protocol != header.IPv4ProtocolNumber {
		d.NetworkDispatcher.DeliverNetworkPacket(protocol, pkt)
		return
	}

	// Get the network header - may not be parsed yet
	netHdr := pkt.NetworkHeader()

	if netHdr.View().Size() < header.IPv4MinimumSize {
		// Network header not yet parsed, parse from data
		data := pkt.Data().AsRange().ToSlice()
		if len(data) < header.IPv4MinimumSize {
			d.NetworkDispatcher.DeliverNetworkPacket(protocol, pkt)
			return
		}
		ipHdr := header.IPv4(data)
		if ipHdr.Protocol() == uint8(header.UDPProtocolNumber) {
			ipHdrLen := int(ipHdr.HeaderLength())
			if len(data) >= ipHdrLen+header.UDPMinimumSize {
				udpHdr := header.UDP(data[ipHdrLen:])
				payload := data[ipHdrLen+header.UDPMinimumSize:]
				srcAddr := ipHdr.SourceAddress()
				dstAddr := ipHdr.DestinationAddress()
				srcIP := net.IP(srcAddr.AsSlice())
				dstIP := net.IP(dstAddr.AsSlice())

				d.ep.logger.Debug("intercepted UDP packet",
					zap.String("src", srcIP.String()),
					zap.Uint16("src_port", udpHdr.SourcePort()),
					zap.String("dst", dstIP.String()),
					zap.Uint16("dst_port", udpHdr.DestinationPort()),
					zap.Int("payload_len", len(payload)),
				)

				if d.ep.udpHandler != nil {
					d.ep.udpHandler.HandleUDPPacket(srcIP, dstIP, udpHdr.SourcePort(), udpHdr.DestinationPort(), payload)
				}
				return // Don't pass UDP to gVisor - we handle it ourselves
			}
		}
		// Not UDP or malformed, pass to gVisor
		d.NetworkDispatcher.DeliverNetworkPacket(protocol, pkt)
		return
	}

	ipHdr := header.IPv4(netHdr.Slice())

	// Only intercept UDP
	if ipHdr.Protocol() != uint8(header.UDPProtocolNumber) {
		d.NetworkDispatcher.DeliverNetworkPacket(protocol, pkt)
		return
	}

	// Get transport header
	transHdr := pkt.TransportHeader()
	if transHdr.View().Size() < header.UDPMinimumSize {
		d.NetworkDispatcher.DeliverNetworkPacket(protocol, pkt)
		return
	}

	udpHdr := header.UDP(transHdr.Slice())
	srcAddr := ipHdr.SourceAddress()
	dstAddr := ipHdr.DestinationAddress()
	srcIP := net.IP(srcAddr.AsSlice())
	dstIP := net.IP(dstAddr.AsSlice())
	srcPort := udpHdr.SourcePort()
	dstPort := udpHdr.DestinationPort()

	// Get payload
	payload := pkt.Data().AsRange().ToSlice()

	d.ep.logger.Info("intercepted UDP packet",
		zap.String("src", srcIP.String()),
		zap.Uint16("src_port", srcPort),
		zap.String("dst", dstIP.String()),
		zap.Uint16("dst_port", dstPort),
		zap.Int("payload_len", len(payload)),
	)

	// Call the UDP handler if set
	if d.ep.udpHandler != nil {
		d.ep.udpHandler.HandleUDPPacket(srcIP, dstIP, srcPort, dstPort, payload)
	}

	// Don't pass UDP to gVisor - we handle it ourselves
	// d.NetworkDispatcher.DeliverNetworkPacket(protocol, pkt)
}

// WriteRawPacket writes a raw IP packet back to the TUN
func (e *interceptEndpoint) WriteRawPacket(data []byte) error {
	_, err := e.tunWriter(data)
	return err
}

// BuildUDPResponse creates a UDP response packet
func BuildUDPResponse(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	// Calculate total lengths
	ipLen := header.IPv4MinimumSize
	udpLen := header.UDPMinimumSize + len(payload)
	totalLen := ipLen + udpLen

	pkt := make([]byte, totalLen)

	// Build IP header
	ip := header.IPv4(pkt)
	ip.Encode(&header.IPv4Fields{
		TotalLength: uint16(totalLen),
		TTL:         64,
		Protocol:    uint8(header.UDPProtocolNumber),
		SrcAddr:     tcpip.AddrFrom4Slice(srcIP.To4()),
		DstAddr:     tcpip.AddrFrom4Slice(dstIP.To4()),
	})
	ip.SetChecksum(^ip.CalculateChecksum())

	// Build UDP header
	udp := header.UDP(pkt[ipLen:])
	udp.Encode(&header.UDPFields{
		SrcPort: srcPort,
		DstPort: dstPort,
		Length:  uint16(udpLen),
	})

	// Copy payload
	copy(pkt[ipLen+header.UDPMinimumSize:], payload)

	// Calculate UDP checksum
	xsum := header.PseudoHeaderChecksum(header.UDPProtocolNumber,
		tcpip.AddrFrom4Slice(srcIP.To4()),
		tcpip.AddrFrom4Slice(dstIP.To4()),
		uint16(udpLen))
	xsum = checksum(pkt[ipLen:], xsum)
	udp.SetChecksum(^xsum)

	return pkt
}

// checksum calculates the checksum of data starting from initial value
func checksum(data []byte, initial uint16) uint16 {
	sum := uint32(initial)
	for i := 0; i+1 < len(data); i += 2 {
		sum += uint32(data[i])<<8 | uint32(data[i+1])
	}
	if len(data)%2 != 0 {
		sum += uint32(data[len(data)-1]) << 8
	}
	for sum > 0xffff {
		sum = (sum >> 16) + (sum & 0xffff)
	}
	return uint16(sum)
}

// Ensure interceptEndpoint implements LinkEndpoint
var _ stack.LinkEndpoint = (*interceptEndpoint)(nil)

// Write a response packet to TUN
func (e *interceptEndpoint) WritePacket(pkt *stack.PacketBuffer) tcpip.Error {
	// Get all views from the packet
	views := pkt.AsSlices()

	// Flatten into single buffer
	var totalLen int
	for _, v := range views {
		totalLen += len(v)
	}

	buf := make([]byte, totalLen)
	offset := 0
	for _, v := range views {
		copy(buf[offset:], v)
		offset += len(v)
	}

	_, err := e.tunWriter(buf)
	if err != nil {
		return &tcpip.ErrAborted{}
	}
	return nil
}

// InjectInbound creates a PacketBuffer from raw data and injects it
func (e *interceptEndpoint) InjectInbound(protocol tcpip.NetworkProtocolNumber, data []byte) {
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(data),
	})
	defer pkt.DecRef()

	e.dispatcher.DeliverNetworkPacket(protocol, pkt)
}
