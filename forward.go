package pingtunnel

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"time"
)

// UDPForwardAssociation keeps the SOCKS5 control TCP connection and UDP relay socket.
// The TCP connection must remain open for the lifetime of the UDP association.
type UDPForwardAssociation struct {
	ControlConn net.Conn
	UDPConn     *net.UDPConn
	RelayAddr   *net.UDPAddr
}

// ForwardConfig holds proxy configuration for forwarding connections
type ForwardConfig struct {
	Scheme string // "socks5" or "http"
	Host   string // proxy host
	Port   int    // proxy port
}

// ParseForwardURL parses a forward URL like "socks5://localhost:2080" or "http://localhost:8080"
func ParseForwardURL(rawURL string) (*ForwardConfig, error) {
	if rawURL == "" {
		return nil, nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid forward URL: %w", err)
	}

	if u.Scheme != "socks5" && u.Scheme != "http" {
		return nil, fmt.Errorf("unsupported proxy scheme: %s (supported: socks5, http)", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return nil, errors.New("missing proxy host in forward URL")
	}

	portStr := u.Port()
	if portStr == "" {
		return nil, errors.New("missing proxy port in forward URL")
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy port: %w", err)
	}

	return &ForwardConfig{
		Scheme: u.Scheme,
		Host:   host,
		Port:   port,
	}, nil
}

// Address returns the proxy address as host:port
func (f *ForwardConfig) Address() string {
	return net.JoinHostPort(f.Host, strconv.Itoa(f.Port))
}

// DialThroughProxy establishes a connection to the target address through the proxy
func DialThroughProxy(config *ForwardConfig, targetAddr string, timeout time.Duration) (net.Conn, error) {
	// Connect to proxy
	conn, err := net.DialTimeout("tcp", config.Address(), timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to proxy: %w", err)
	}

	// Set deadline for handshake
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to set deadline: %w", err)
	}

	// Perform protocol-specific handshake
	switch config.Scheme {
	case "socks5":
		if err := socks5Handshake(conn, targetAddr); err != nil {
			conn.Close()
			return nil, fmt.Errorf("SOCKS5 handshake failed: %w", err)
		}
	case "http":
		if err := httpConnectHandshake(conn, targetAddr); err != nil {
			conn.Close()
			return nil, fmt.Errorf("HTTP CONNECT handshake failed: %w", err)
		}
	default:
		conn.Close()
		return nil, fmt.Errorf("unsupported proxy scheme: %s", config.Scheme)
	}

	// Clear deadline after successful handshake
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to clear deadline: %w", err)
	}

	return conn, nil
}

// DialUDPThroughProxy establishes a SOCKS5 UDP ASSOCIATE and returns a UDP relay association.
func DialUDPThroughProxy(config *ForwardConfig, timeout time.Duration) (*UDPForwardAssociation, error) {
	if config == nil {
		return nil, errors.New("missing forward config")
	}
	if config.Scheme != "socks5" {
		return nil, fmt.Errorf("unsupported proxy scheme for UDP: %s (supported: socks5)", config.Scheme)
	}

	udpConn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open local udp socket: %w", err)
	}

	tcpConn, err := net.DialTimeout("tcp", config.Address(), timeout)
	if err != nil {
		udpConn.Close()
		return nil, fmt.Errorf("failed to connect to proxy: %w", err)
	}

	closeAllWithErr := func(cause error) (*UDPForwardAssociation, error) {
		tcpConn.Close()
		udpConn.Close()
		return nil, cause
	}

	if err := tcpConn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return closeAllWithErr(fmt.Errorf("failed to set deadline: %w", err))
	}

	if err := socks5NegotiateNoAuth(tcpConn); err != nil {
		return closeAllWithErr(fmt.Errorf("SOCKS5 negotiation failed: %w", err))
	}

	localAddr := udpConn.LocalAddr().(*net.UDPAddr)
	associateAddr := socks5UDPAssociateAddr(localAddr)
	if err := socks5SendCommand(tcpConn, socks5CmdUDPAssociate, associateAddr); err != nil {
		return closeAllWithErr(fmt.Errorf("failed to send UDP ASSOCIATE: %w", err))
	}

	rep, relayAddrStr, err := socks5ReadReply(tcpConn)
	if err != nil {
		return closeAllWithErr(fmt.Errorf("failed to read UDP ASSOCIATE reply: %w", err))
	}
	if rep != socks5ReplySucceeded {
		return closeAllWithErr(fmt.Errorf("SOCKS5 UDP ASSOCIATE failed with code: %d", rep))
	}

	relayAddr, err := net.ResolveUDPAddr("udp", relayAddrStr)
	if err != nil {
		return closeAllWithErr(fmt.Errorf("failed to resolve relay address %q: %w", relayAddrStr, err))
	}

	if err := tcpConn.SetDeadline(time.Time{}); err != nil {
		return closeAllWithErr(fmt.Errorf("failed to clear deadline: %w", err))
	}

	return &UDPForwardAssociation{
		ControlConn: tcpConn,
		UDPConn:     udpConn,
		RelayAddr:   relayAddr,
	}, nil
}

func socks5UDPAssociateAddr(localAddr *net.UDPAddr) string {
	if localAddr == nil {
		return "0.0.0.0:0"
	}
	if localAddr.IP == nil || localAddr.IP.IsUnspecified() {
		return net.JoinHostPort("0.0.0.0", strconv.Itoa(localAddr.Port))
	}
	return net.JoinHostPort(localAddr.IP.String(), strconv.Itoa(localAddr.Port))
}

func socks5NegotiateNoAuth(conn net.Conn) error {
	greeting := []byte{socks5Version, 0x01, 0x00}
	if _, err := conn.Write(greeting); err != nil {
		return fmt.Errorf("failed to send greeting: %w", err)
	}

	response := make([]byte, 2)
	if _, err := io.ReadFull(conn, response); err != nil {
		return fmt.Errorf("failed to read greeting response: %w", err)
	}

	if response[0] != socks5Version {
		return fmt.Errorf("unexpected SOCKS version: %d", response[0])
	}
	if response[1] == 0xFF {
		return errors.New("SOCKS5 proxy requires authentication (not supported)")
	}
	if response[1] != 0x00 {
		return fmt.Errorf("unexpected SOCKS5 auth method: %d", response[1])
	}

	return nil
}

func socks5SendCommand(conn net.Conn, cmd byte, targetAddr string) error {
	encodedAddr, err := encodeSocks5Address(targetAddr)
	if err != nil {
		return fmt.Errorf("invalid target address %q: %w", targetAddr, err)
	}

	request := make([]byte, 0, 3+len(encodedAddr))
	request = append(request, socks5Version, cmd, 0x00)
	request = append(request, encodedAddr...)

	if _, err := conn.Write(request); err != nil {
		return err
	}
	return nil
}

func socks5ReadReply(conn net.Conn) (rep byte, bindAddr string, err error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return 0, "", fmt.Errorf("failed to read reply header: %w", err)
	}

	if header[0] != socks5Version {
		return 0, "", fmt.Errorf("unexpected SOCKS version in reply: %d", header[0])
	}
	if header[2] != 0x00 {
		return 0, "", fmt.Errorf("invalid socks reserved byte in reply: %d", header[2])
	}

	addr, err := readSocks5AddressFromReader(conn, header[3])
	if err != nil {
		return 0, "", err
	}

	return header[1], addr, nil
}

// socks5Handshake performs SOCKS5 handshake (RFC 1928) without authentication
func socks5Handshake(conn net.Conn, targetAddr string) error {
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return fmt.Errorf("invalid target address: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("invalid target port: %w", err)
	}

	// Step 1: Send greeting (version 5, 1 method, no auth)
	// [VER, NMETHODS, METHODS...]
	greeting := []byte{0x05, 0x01, 0x00}
	if _, err := conn.Write(greeting); err != nil {
		return fmt.Errorf("failed to send greeting: %w", err)
	}

	// Step 2: Receive server choice
	// [VER, METHOD]
	response := make([]byte, 2)
	if _, err := conn.Read(response); err != nil {
		return fmt.Errorf("failed to read greeting response: %w", err)
	}

	if response[0] != 0x05 {
		return fmt.Errorf("unexpected SOCKS version: %d", response[0])
	}

	if response[1] == 0xFF {
		return errors.New("SOCKS5 proxy requires authentication (not supported)")
	}

	if response[1] != 0x00 {
		return fmt.Errorf("unexpected SOCKS5 auth method: %d", response[1])
	}

	// Step 3: Send connect request
	// [VER, CMD, RSV, ATYP, DST.ADDR, DST.PORT]
	// CMD: 0x01 = CONNECT
	// ATYP: 0x03 = domain name
	request := make([]byte, 0, 7+len(host))
	request = append(request, 0x05, 0x01, 0x00, 0x03)         // VER, CMD, RSV, ATYP (domain)
	request = append(request, byte(len(host)))                // domain length
	request = append(request, []byte(host)...)                // domain
	request = append(request, byte(port>>8), byte(port&0xFF)) // port (big-endian)

	if _, err := conn.Write(request); err != nil {
		return fmt.Errorf("failed to send connect request: %w", err)
	}

	// Step 4: Receive connect response
	// [VER, REP, RSV, ATYP, BND.ADDR, BND.PORT]
	// We need to read at least 4 bytes first to determine address type
	reply := make([]byte, 4)
	if _, err := conn.Read(reply); err != nil {
		return fmt.Errorf("failed to read connect response: %w", err)
	}

	if reply[0] != 0x05 {
		return fmt.Errorf("unexpected SOCKS version in reply: %d", reply[0])
	}

	if reply[1] != 0x00 {
		return fmt.Errorf("SOCKS5 connect failed with code: %d", reply[1])
	}

	// Read remaining bytes based on address type
	var addrLen int
	switch reply[3] {
	case 0x01: // IPv4
		addrLen = 4 + 2 // 4 bytes IP + 2 bytes port
	case 0x03: // Domain name
		// Read the length byte first
		lenByte := make([]byte, 1)
		if _, err := conn.Read(lenByte); err != nil {
			return fmt.Errorf("failed to read domain length: %w", err)
		}
		addrLen = int(lenByte[0]) + 2 // domain + 2 bytes port
	case 0x04: // IPv6
		addrLen = 16 + 2 // 16 bytes IP + 2 bytes port
	default:
		return fmt.Errorf("unexpected address type: %d", reply[3])
	}

	// Read the address and port (we don't actually need them)
	remaining := make([]byte, addrLen)
	if _, err := conn.Read(remaining); err != nil {
		return fmt.Errorf("failed to read bound address: %w", err)
	}

	return nil
}

// httpConnectHandshake performs HTTP CONNECT handshake
func httpConnectHandshake(conn net.Conn, targetAddr string) error {
	// Send CONNECT request
	request := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", targetAddr, targetAddr)
	if _, err := conn.Write([]byte(request)); err != nil {
		return fmt.Errorf("failed to send CONNECT request: %w", err)
	}

	// Read response
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read response status line: %w", err)
	}

	// Parse status code (expecting "HTTP/1.x 200 ...")
	var httpVersion string
	var statusCode int
	var statusText string
	n, err := fmt.Sscanf(statusLine, "%s %d %s", &httpVersion, &statusCode, &statusText)
	if err != nil || n < 2 {
		return fmt.Errorf("invalid HTTP response: %s", statusLine)
	}

	if statusCode != 200 {
		return fmt.Errorf("HTTP CONNECT failed with status: %d", statusCode)
	}

	// Read and discard headers until we get an empty line
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read response headers: %w", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	return nil
}
