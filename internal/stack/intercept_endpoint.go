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

// ICMPPacketHandler handles intercepted ICMP packets
type ICMPPacketHandler interface {
	HandleICMPPacket(srcIP, dstIP net.IP, id, seq uint16, payload []byte)
}

// interceptEndpoint wraps a link endpoint to intercept UDP and ICMP packets
type interceptEndpoint struct {
	stack.LinkEndpoint
	dispatcher  stack.NetworkDispatcher
	udpHandler  UDPPacketHandler
	icmpHandler ICMPPacketHandler
	tunWriter   func([]byte) (int, error)
	logger      *zap.Logger
}

// newInterceptEndpoint creates a new intercepting link endpoint
func newInterceptEndpoint(inner stack.LinkEndpoint, udpHandler UDPPacketHandler, icmpHandler ICMPPacketHandler, tunWriter func([]byte) (int, error), logger *zap.Logger) *interceptEndpoint {
	return &interceptEndpoint{
		LinkEndpoint: inner,
		udpHandler:   udpHandler,
		icmpHandler:  icmpHandler,
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

	// Handle IPv6 packets
	if protocol == header.IPv6ProtocolNumber {
		d.handleIPv6Packet(pkt)
		return
	}

	// Handle IPv4 packets
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
		ipHdrLen := int(ipHdr.HeaderLength())

		// Check for UDP
		if ipHdr.Protocol() == uint8(header.UDPProtocolNumber) {
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

		// Check for ICMP Echo Request
		if ipHdr.Protocol() == uint8(header.ICMPv4ProtocolNumber) {
			if len(data) >= ipHdrLen+header.ICMPv4MinimumSize {
				icmpHdr := header.ICMPv4(data[ipHdrLen:])
				// Only handle Echo Request (Type 8)
				if icmpHdr.Type() == header.ICMPv4Echo {
					srcAddr := ipHdr.SourceAddress()
					dstAddr := ipHdr.DestinationAddress()
					srcIP := net.IP(srcAddr.AsSlice())
					dstIP := net.IP(dstAddr.AsSlice())
					id := icmpHdr.Ident()
					seq := icmpHdr.Sequence()
					payload := data[ipHdrLen+header.ICMPv4MinimumSize:]

					d.ep.logger.Debug("intercepted ICMP Echo Request",
						zap.String("src", srcIP.String()),
						zap.String("dst", dstIP.String()),
						zap.Uint16("id", id),
						zap.Uint16("seq", seq),
						zap.Int("payload_len", len(payload)),
					)

					if d.ep.icmpHandler != nil {
						d.ep.icmpHandler.HandleICMPPacket(srcIP, dstIP, id, seq, payload)
					}
					return // Don't pass ICMP to gVisor - we handle it ourselves
				}
			}
		}

		// Not UDP/ICMP or malformed, pass to gVisor
		d.NetworkDispatcher.DeliverNetworkPacket(protocol, pkt)
		return
	}

	ipHdr := header.IPv4(netHdr.Slice())
	srcAddr := ipHdr.SourceAddress()
	dstAddr := ipHdr.DestinationAddress()
	srcIP := net.IP(srcAddr.AsSlice())
	dstIP := net.IP(dstAddr.AsSlice())

	// Handle UDP
	if ipHdr.Protocol() == uint8(header.UDPProtocolNumber) {
		// Get transport header
		transHdr := pkt.TransportHeader()
		if transHdr.View().Size() < header.UDPMinimumSize {
			d.NetworkDispatcher.DeliverNetworkPacket(protocol, pkt)
			return
		}

		udpHdr := header.UDP(transHdr.Slice())
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
		return // Don't pass UDP to gVisor - we handle it ourselves
	}

	// Handle ICMP Echo Request
	if ipHdr.Protocol() == uint8(header.ICMPv4ProtocolNumber) {
		// Get transport header (contains ICMP data)
		transHdr := pkt.TransportHeader()
		if transHdr.View().Size() < header.ICMPv4MinimumSize {
			d.NetworkDispatcher.DeliverNetworkPacket(protocol, pkt)
			return
		}

		icmpHdr := header.ICMPv4(transHdr.Slice())
		// Only handle Echo Request (Type 8)
		if icmpHdr.Type() == header.ICMPv4Echo {
			id := icmpHdr.Ident()
			seq := icmpHdr.Sequence()
			payload := pkt.Data().AsRange().ToSlice()

			d.ep.logger.Info("intercepted ICMP Echo Request",
				zap.String("src", srcIP.String()),
				zap.String("dst", dstIP.String()),
				zap.Uint16("id", id),
				zap.Uint16("seq", seq),
				zap.Int("payload_len", len(payload)),
			)

			if d.ep.icmpHandler != nil {
				d.ep.icmpHandler.HandleICMPPacket(srcIP, dstIP, id, seq, payload)
			}
			return // Don't pass ICMP to gVisor - we handle it ourselves
		}
	}

	// Other protocols - pass to gVisor
	d.NetworkDispatcher.DeliverNetworkPacket(protocol, pkt)
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

// BuildICMPEchoReply creates an ICMP Echo Reply packet
func BuildICMPEchoReply(srcIP, dstIP net.IP, id, seq uint16, payload []byte) []byte {
	// Calculate total lengths
	ipLen := header.IPv4MinimumSize
	icmpLen := header.ICMPv4MinimumSize + len(payload)
	totalLen := ipLen + icmpLen

	pkt := make([]byte, totalLen)

	// Build IP header
	ip := header.IPv4(pkt)
	ip.Encode(&header.IPv4Fields{
		TotalLength: uint16(totalLen),
		TTL:         64,
		Protocol:    uint8(header.ICMPv4ProtocolNumber),
		SrcAddr:     tcpip.AddrFrom4Slice(srcIP.To4()),
		DstAddr:     tcpip.AddrFrom4Slice(dstIP.To4()),
	})
	ip.SetChecksum(^ip.CalculateChecksum())

	// Build ICMP Echo Reply header (Type 0, Code 0)
	icmpHdr := header.ICMPv4(pkt[ipLen:])
	icmpHdr.SetType(header.ICMPv4EchoReply)
	icmpHdr.SetCode(0)
	icmpHdr.SetIdent(id)
	icmpHdr.SetSequence(seq)

	// Copy payload
	copy(pkt[ipLen+header.ICMPv4MinimumSize:], payload)

	// Calculate ICMP checksum (over entire ICMP message)
	icmpHdr.SetChecksum(^checksum(pkt[ipLen:], 0))

	return pkt
}

// handleIPv6Packet handles IPv6 packet interception
func (d *interceptDispatcher) handleIPv6Packet(pkt *stack.PacketBuffer) {
	// Get the network header - may not be parsed yet
	netHdr := pkt.NetworkHeader()

	if netHdr.View().Size() < header.IPv6MinimumSize {
		// Network header not yet parsed, parse from data
		data := pkt.Data().AsRange().ToSlice()
		if len(data) < header.IPv6MinimumSize {
			d.NetworkDispatcher.DeliverNetworkPacket(header.IPv6ProtocolNumber, pkt)
			return
		}
		ipHdr := header.IPv6(data)

		// Check for UDP
		if ipHdr.NextHeader() == uint8(header.UDPProtocolNumber) {
			ipHdrLen := header.IPv6MinimumSize
			if len(data) >= ipHdrLen+header.UDPMinimumSize {
				udpHdr := header.UDP(data[ipHdrLen:])
				payload := data[ipHdrLen+header.UDPMinimumSize:]
				srcAddr := ipHdr.SourceAddress()
				dstAddr := ipHdr.DestinationAddress()
				srcIP := net.IP(srcAddr.AsSlice())
				dstIP := net.IP(dstAddr.AsSlice())

				d.ep.logger.Debug("intercepted IPv6 UDP packet",
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

		// Check for ICMPv6 Echo Request
		if ipHdr.NextHeader() == uint8(header.ICMPv6ProtocolNumber) {
			ipHdrLen := header.IPv6MinimumSize
			if len(data) >= ipHdrLen+header.ICMPv6MinimumSize {
				icmpHdr := header.ICMPv6(data[ipHdrLen:])
				// Only handle Echo Request (Type 128)
				if icmpHdr.Type() == header.ICMPv6EchoRequest {
					srcAddr := ipHdr.SourceAddress()
					dstAddr := ipHdr.DestinationAddress()
					srcIP := net.IP(srcAddr.AsSlice())
					dstIP := net.IP(dstAddr.AsSlice())
					// ICMPv6 echo has identifier at offset 4 and sequence at offset 6
					id := uint16(data[ipHdrLen+4])<<8 | uint16(data[ipHdrLen+5])
					seq := uint16(data[ipHdrLen+6])<<8 | uint16(data[ipHdrLen+7])
					payload := data[ipHdrLen+header.ICMPv6EchoMinimumSize:]

					d.ep.logger.Debug("intercepted ICMPv6 Echo Request",
						zap.String("src", srcIP.String()),
						zap.String("dst", dstIP.String()),
						zap.Uint16("id", id),
						zap.Uint16("seq", seq),
						zap.Int("payload_len", len(payload)),
					)

					if d.ep.icmpHandler != nil {
						d.ep.icmpHandler.HandleICMPPacket(srcIP, dstIP, id, seq, payload)
					}
					return // Don't pass ICMPv6 to gVisor - we handle it ourselves
				}
			}
		}

		// Not UDP/ICMPv6 or malformed, pass to gVisor
		d.NetworkDispatcher.DeliverNetworkPacket(header.IPv6ProtocolNumber, pkt)
		return
	}

	ipHdr := header.IPv6(netHdr.Slice())
	srcAddr := ipHdr.SourceAddress()
	dstAddr := ipHdr.DestinationAddress()
	srcIP := net.IP(srcAddr.AsSlice())
	dstIP := net.IP(dstAddr.AsSlice())

	// Handle UDP
	if ipHdr.NextHeader() == uint8(header.UDPProtocolNumber) {
		// Get transport header
		transHdr := pkt.TransportHeader()
		if transHdr.View().Size() < header.UDPMinimumSize {
			d.NetworkDispatcher.DeliverNetworkPacket(header.IPv6ProtocolNumber, pkt)
			return
		}

		udpHdr := header.UDP(transHdr.Slice())
		srcPort := udpHdr.SourcePort()
		dstPort := udpHdr.DestinationPort()

		// Get payload
		payload := pkt.Data().AsRange().ToSlice()

		d.ep.logger.Info("intercepted IPv6 UDP packet",
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
		return // Don't pass UDP to gVisor - we handle it ourselves
	}

	// Handle ICMPv6 Echo Request
	if ipHdr.NextHeader() == uint8(header.ICMPv6ProtocolNumber) {
		// Get transport header (contains ICMPv6 data)
		transHdr := pkt.TransportHeader()
		if transHdr.View().Size() < header.ICMPv6MinimumSize {
			d.NetworkDispatcher.DeliverNetworkPacket(header.IPv6ProtocolNumber, pkt)
			return
		}

		icmpHdr := header.ICMPv6(transHdr.Slice())
		// Only handle Echo Request (Type 128)
		if icmpHdr.Type() == header.ICMPv6EchoRequest {
			// ICMPv6 echo has identifier at offset 4 and sequence at offset 6
			icmpData := transHdr.Slice()
			id := uint16(icmpData[4])<<8 | uint16(icmpData[5])
			seq := uint16(icmpData[6])<<8 | uint16(icmpData[7])
			payload := pkt.Data().AsRange().ToSlice()

			d.ep.logger.Info("intercepted ICMPv6 Echo Request",
				zap.String("src", srcIP.String()),
				zap.String("dst", dstIP.String()),
				zap.Uint16("id", id),
				zap.Uint16("seq", seq),
				zap.Int("payload_len", len(payload)),
			)

			if d.ep.icmpHandler != nil {
				d.ep.icmpHandler.HandleICMPPacket(srcIP, dstIP, id, seq, payload)
			}
			return // Don't pass ICMPv6 to gVisor - we handle it ourselves
		}
	}

	// Other protocols - pass to gVisor
	d.NetworkDispatcher.DeliverNetworkPacket(header.IPv6ProtocolNumber, pkt)
}

// BuildICMPv6EchoReply creates an ICMPv6 Echo Reply packet
func BuildICMPv6EchoReply(srcIP, dstIP net.IP, id, seq uint16, payload []byte) []byte {
	// Calculate total lengths
	ipLen := header.IPv6MinimumSize
	icmpLen := header.ICMPv6EchoMinimumSize + len(payload)
	totalLen := ipLen + icmpLen

	pkt := make([]byte, totalLen)

	// Build IPv6 header
	ip := header.IPv6(pkt)
	ip.Encode(&header.IPv6Fields{
		PayloadLength:     uint16(icmpLen),
		TransportProtocol: header.ICMPv6ProtocolNumber,
		HopLimit:          64,
		SrcAddr:           tcpip.AddrFrom16Slice(srcIP.To16()),
		DstAddr:           tcpip.AddrFrom16Slice(dstIP.To16()),
	})

	// Build ICMPv6 Echo Reply header (Type 129, Code 0)
	icmpHdr := header.ICMPv6(pkt[ipLen:])
	icmpHdr.SetType(header.ICMPv6EchoReply)
	icmpHdr.SetCode(0)
	// Set identifier and sequence (at offsets 4 and 6 in ICMPv6 message)
	pkt[ipLen+4] = byte(id >> 8)
	pkt[ipLen+5] = byte(id)
	pkt[ipLen+6] = byte(seq >> 8)
	pkt[ipLen+7] = byte(seq)

	// Copy payload
	copy(pkt[ipLen+header.ICMPv6EchoMinimumSize:], payload)

	// Calculate ICMPv6 checksum (requires pseudo-header)
	xsum := header.PseudoHeaderChecksum(header.ICMPv6ProtocolNumber,
		tcpip.AddrFrom16Slice(srcIP.To16()),
		tcpip.AddrFrom16Slice(dstIP.To16()),
		uint16(icmpLen))
	xsum = checksum(pkt[ipLen:], xsum)
	icmpHdr.SetChecksum(^xsum)

	return pkt
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
