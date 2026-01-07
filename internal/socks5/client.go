package socks5

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"
)

// Client connects to a SOCKS5 proxy
type Client struct {
	ServerAddr string
	Auth       Authenticator
	Timeout    time.Duration
	KeepAlive  time.Duration
}

// NewClient creates a new SOCKS5 client
func NewClient(serverAddr string, auth Authenticator, timeout, keepAlive time.Duration) *Client {
	if auth == nil {
		auth = &NoAuth{}
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if keepAlive == 0 {
		keepAlive = 60 * time.Second
	}

	return &Client{
		ServerAddr: serverAddr,
		Auth:       auth,
		Timeout:    timeout,
		KeepAlive:  keepAlive,
	}
}

// Connect establishes a TCP connection through the SOCKS5 proxy
func (c *Client) Connect(ctx context.Context, addr string) (net.Conn, error) {
	// Parse target address
	target, err := ParseAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid target address: %w", err)
	}

	// Connect to SOCKS5 server
	conn, err := c.dialServer(ctx)
	if err != nil {
		return nil, err
	}

	// Set deadline for handshake
	if c.Timeout > 0 {
		conn.SetDeadline(time.Now().Add(c.Timeout))
	}

	// Perform handshake
	if err := c.handshake(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("handshake failed: %w", err)
	}

	// Send CONNECT request
	if err := c.sendConnect(conn, target); err != nil {
		conn.Close()
		return nil, fmt.Errorf("connect failed: %w", err)
	}

	// Clear deadline after successful connection
	conn.SetDeadline(time.Time{})

	return conn, nil
}

// UDPAssociate establishes a UDP relay through the SOCKS5 proxy
func (c *Client) UDPAssociate(ctx context.Context, localAddr *net.UDPAddr) (*UDPRelay, error) {
	// Connect to SOCKS5 server
	conn, err := c.dialServer(ctx)
	if err != nil {
		return nil, err
	}

	// Set deadline for handshake
	if c.Timeout > 0 {
		conn.SetDeadline(time.Now().Add(c.Timeout))
	}

	// Perform handshake
	if err := c.handshake(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("handshake failed: %w", err)
	}

	// Send UDP ASSOCIATE request
	relayAddr, err := c.sendUDPAssociate(conn, localAddr)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("udp associate failed: %w", err)
	}

	// Clear deadline
	conn.SetDeadline(time.Time{})

	// Fix relay address if server returns localhost/unspecified
	// Some SOCKS5 servers return 0.0.0.0 or 127.0.0.1 as relay address
	// In these cases, use the SOCKS5 server's address instead
	if relayAddr.IP != nil && (relayAddr.IP.IsLoopback() || relayAddr.IP.IsUnspecified()) {
		serverHost, _, _ := net.SplitHostPort(c.ServerAddr)
		serverIP := net.ParseIP(serverHost)
		if serverIP == nil {
			// Hostname - resolve it
			ips, err := net.LookupIP(serverHost)
			if err == nil && len(ips) > 0 {
				serverIP = ips[0]
			}
		}
		if serverIP != nil {
			relayAddr.IP = serverIP
			if serverIP.To4() != nil {
				relayAddr.Type = AddrTypeIPv4
			} else {
				relayAddr.Type = AddrTypeIPv6
			}
		}
	}

	// Create UDP connection to relay
	relayUDP := relayAddr.ToUDPAddr()
	udpConn, err := net.DialUDP("udp", nil, relayUDP)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to connect to relay %s: %w", relayUDP.String(), err)
	}

	return &UDPRelay{
		controlConn: conn,
		relayConn:   udpConn,
		relayAddr:   relayAddr,
	}, nil
}

// dialServer connects to the SOCKS5 server
func (c *Client) dialServer(ctx context.Context) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   c.Timeout,
		KeepAlive: c.KeepAlive,
	}

	conn, err := dialer.DialContext(ctx, "tcp", c.ServerAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SOCKS5 server: %w", err)
	}

	return conn, nil
}

// handshake performs the SOCKS5 handshake including authentication
func (c *Client) handshake(conn net.Conn) error {
	// Send method selection
	// +----+----------+----------+
	// |VER | NMETHODS | METHODS  |
	// +----+----------+----------+
	// | 1  |    1     | 1 to 255 |
	// +----+----------+----------+
	methods := []byte{Version, 1, c.Auth.Method()}
	if _, err := conn.Write(methods); err != nil {
		return fmt.Errorf("failed to send methods: %w", err)
	}

	// Read server response
	// +----+--------+
	// |VER | METHOD |
	// +----+--------+
	// | 1  |   1    |
	// +----+--------+
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return fmt.Errorf("failed to read method response: %w", err)
	}

	if resp[0] != Version {
		return fmt.Errorf("unexpected SOCKS version: %d", resp[0])
	}

	if resp[1] == AuthNoAccept {
		return fmt.Errorf("no acceptable authentication method")
	}

	if resp[1] != c.Auth.Method() {
		return fmt.Errorf("unexpected authentication method: %d", resp[1])
	}

	// Perform authentication if needed
	if err := c.Auth.Authenticate(conn); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	return nil
}

// sendConnect sends a CONNECT request
func (c *Client) sendConnect(conn net.Conn, target *Address) error {
	// Build CONNECT request
	// +----+-----+-------+------+----------+----------+
	// |VER | CMD |  RSV  | ATYP | DST.ADDR | DST.PORT |
	// +----+-----+-------+------+----------+----------+
	// | 1  |  1  | X'00' |  1   | Variable |    2     |
	// +----+-----+-------+------+----------+----------+
	addrBytes, err := target.MarshalBinary()
	if err != nil {
		return err
	}

	req := make([]byte, 3+len(addrBytes))
	req[0] = Version
	req[1] = CmdConnect
	req[2] = 0x00 // Reserved
	copy(req[3:], addrBytes)

	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("failed to send connect request: %w", err)
	}

	// Read response
	return c.readReply(conn)
}

// sendUDPAssociate sends a UDP ASSOCIATE request
func (c *Client) sendUDPAssociate(conn net.Conn, localAddr *net.UDPAddr) (*Address, error) {
	// Build UDP ASSOCIATE request
	// Client tells server which address/port it will send UDP from
	// If not known, use 0.0.0.0:0
	var addr *Address
	if localAddr != nil && localAddr.IP != nil {
		if ip4 := localAddr.IP.To4(); ip4 != nil {
			addr = &Address{
				Type: AddrTypeIPv4,
				IP:   ip4,
				Port: uint16(localAddr.Port),
			}
		} else {
			addr = &Address{
				Type: AddrTypeIPv6,
				IP:   localAddr.IP,
				Port: uint16(localAddr.Port),
			}
		}
	} else {
		addr = &Address{
			Type: AddrTypeIPv4,
			IP:   net.IPv4zero,
			Port: 0,
		}
	}

	addrBytes, err := addr.MarshalBinary()
	if err != nil {
		return nil, err
	}

	req := make([]byte, 3+len(addrBytes))
	req[0] = Version
	req[1] = CmdUDPAssociate
	req[2] = 0x00 // Reserved
	copy(req[3:], addrBytes)

	if _, err := conn.Write(req); err != nil {
		return nil, fmt.Errorf("failed to send udp associate request: %w", err)
	}

	// Read response and get relay address
	return c.readReplyWithAddr(conn)
}

// readReply reads a SOCKS5 reply
func (c *Client) readReply(conn net.Conn) error {
	// Read reply header
	// +----+-----+-------+------+----------+----------+
	// |VER | REP |  RSV  | ATYP | BND.ADDR | BND.PORT |
	// +----+-----+-------+------+----------+----------+
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return fmt.Errorf("failed to read reply header: %w", err)
	}

	if header[0] != Version {
		return fmt.Errorf("unexpected SOCKS version in reply: %d", header[0])
	}

	if header[1] != ReplySucceeded {
		return &Error{
			Code:    header[1],
			Message: ReplyMessage(header[1]),
		}
	}

	// Read and discard bound address
	switch header[3] {
	case AddrTypeIPv4:
		discard := make([]byte, 4+2)
		io.ReadFull(conn, discard)
	case AddrTypeIPv6:
		discard := make([]byte, 16+2)
		io.ReadFull(conn, discard)
	case AddrTypeDomain:
		lenBuf := make([]byte, 1)
		io.ReadFull(conn, lenBuf)
		discard := make([]byte, int(lenBuf[0])+2)
		io.ReadFull(conn, discard)
	}

	return nil
}

// readReplyWithAddr reads a SOCKS5 reply and returns the bound address
func (c *Client) readReplyWithAddr(conn net.Conn) (*Address, error) {
	// Read reply header
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, fmt.Errorf("failed to read reply header: %w", err)
	}

	if header[0] != Version {
		return nil, fmt.Errorf("unexpected SOCKS version in reply: %d", header[0])
	}

	if header[1] != ReplySucceeded {
		return nil, &Error{
			Code:    header[1],
			Message: ReplyMessage(header[1]),
		}
	}

	// Read bound address
	addr := &Address{Type: header[3]}

	switch addr.Type {
	case AddrTypeIPv4:
		buf := make([]byte, 4+2)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return nil, fmt.Errorf("failed to read IPv4 address: %w", err)
		}
		addr.IP = make(net.IP, 4)
		copy(addr.IP, buf[:4])
		addr.Port = uint16(buf[4])<<8 | uint16(buf[5])

	case AddrTypeIPv6:
		buf := make([]byte, 16+2)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return nil, fmt.Errorf("failed to read IPv6 address: %w", err)
		}
		addr.IP = make(net.IP, 16)
		copy(addr.IP, buf[:16])
		addr.Port = uint16(buf[16])<<8 | uint16(buf[17])

	case AddrTypeDomain:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return nil, fmt.Errorf("failed to read domain length: %w", err)
		}
		domainLen := int(lenBuf[0])
		buf := make([]byte, domainLen+2)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return nil, fmt.Errorf("failed to read domain: %w", err)
		}
		addr.Domain = string(buf[:domainLen])
		addr.Port = uint16(buf[domainLen])<<8 | uint16(buf[domainLen+1])

	default:
		return nil, fmt.Errorf("unknown address type: %d", addr.Type)
	}

	return addr, nil
}

// UDPRelay handles UDP forwarding through SOCKS5
type UDPRelay struct {
	controlConn net.Conn    // TCP connection to keep UDP association alive
	relayConn   *net.UDPConn // UDP connection to relay
	relayAddr   *Address     // Address of the relay
}

// Send sends a UDP datagram through the relay
func (r *UDPRelay) Send(data []byte, target *Address) error {
	// Build UDP relay header
	// +----+------+------+----------+----------+----------+
	// |RSV | FRAG | ATYP | DST.ADDR | DST.PORT |   DATA   |
	// +----+------+------+----------+----------+----------+
	// | 2  |  1   |  1   | Variable |    2     | Variable |
	// +----+------+------+----------+----------+----------+
	addrBytes, err := target.MarshalBinary()
	if err != nil {
		return err
	}

	packet := make([]byte, 3+len(addrBytes)+len(data))
	packet[0] = 0 // RSV
	packet[1] = 0 // RSV
	packet[2] = 0 // FRAG (no fragmentation)
	copy(packet[3:], addrBytes)
	copy(packet[3+len(addrBytes):], data)

	_, err = r.relayConn.Write(packet)
	return err
}

// Receive receives a UDP datagram from the relay
func (r *UDPRelay) Receive(buf []byte) (int, *Address, error) {
	n, err := r.relayConn.Read(buf)
	if err != nil {
		return 0, nil, err
	}

	if n < 4 {
		return 0, nil, fmt.Errorf("packet too short")
	}

	// Parse header
	if buf[2] != 0 {
		return 0, nil, fmt.Errorf("fragmented packets not supported")
	}

	// Parse source address
	addr, consumed, err := ReadAddress(buf[3:n])
	if err != nil {
		return 0, nil, fmt.Errorf("failed to parse address: %w", err)
	}

	dataStart := 3 + consumed
	dataLen := n - dataStart

	// Move data to beginning of buffer
	copy(buf, buf[dataStart:n])

	return dataLen, addr, nil
}

// LocalAddr returns the local UDP address
func (r *UDPRelay) LocalAddr() *net.UDPAddr {
	return r.relayConn.LocalAddr().(*net.UDPAddr)
}

// Close closes the UDP relay
func (r *UDPRelay) Close() error {
	r.relayConn.Close()
	return r.controlConn.Close()
}
