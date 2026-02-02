package pingtunnel

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"
)

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
	request = append(request, 0x05, 0x01, 0x00, 0x03) // VER, CMD, RSV, ATYP (domain)
	request = append(request, byte(len(host)))        // domain length
	request = append(request, []byte(host)...)        // domain
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
