package pingtunnel

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
)

const (
	socks5Version = 0x05

	socks5CmdConnect      = 0x01
	socks5CmdUDPAssociate = 0x03

	socks5AddrIPv4   = 0x01
	socks5AddrDomain = 0x03
	socks5AddrIPv6   = 0x04

	socks5ReplySucceeded              = 0x00
	socks5ReplyGeneralFailure         = 0x01
	socks5ReplyCommandNotSupported    = 0x07
	socks5ReplyAddressTypeUnsupported = 0x08
)

type socks5Request struct {
	Command byte
	Address string
}

func readSocks5Request(r io.Reader) (*socks5Request, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("read socks5 request header: %w", err)
	}
	if header[0] != socks5Version {
		return nil, fmt.Errorf("unsupported socks version: %d", header[0])
	}
	if header[2] != 0x00 {
		return nil, fmt.Errorf("invalid socks reserved byte: %d", header[2])
	}

	addr, err := readSocks5AddressFromReader(r, header[3])
	if err != nil {
		return nil, err
	}

	return &socks5Request{
		Command: header[1],
		Address: addr,
	}, nil
}

func readSocks5AddressFromReader(r io.Reader, atyp byte) (string, error) {
	switch atyp {
	case socks5AddrIPv4:
		buf := make([]byte, net.IPv4len+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", fmt.Errorf("read ipv4 address: %w", err)
		}
		host := net.IP(buf[:net.IPv4len]).String()
		port := binary.BigEndian.Uint16(buf[net.IPv4len:])
		return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
	case socks5AddrIPv6:
		buf := make([]byte, net.IPv6len+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", fmt.Errorf("read ipv6 address: %w", err)
		}
		host := net.IP(buf[:net.IPv6len]).String()
		port := binary.BigEndian.Uint16(buf[net.IPv6len:])
		return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
	case socks5AddrDomain:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(r, lenBuf); err != nil {
			return "", fmt.Errorf("read domain length: %w", err)
		}
		domainLen := int(lenBuf[0])
		if domainLen == 0 {
			return "", fmt.Errorf("invalid empty domain in socks5 address")
		}
		buf := make([]byte, domainLen+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", fmt.Errorf("read domain address: %w", err)
		}
		host := string(buf[:domainLen])
		port := binary.BigEndian.Uint16(buf[domainLen:])
		return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
	default:
		return "", fmt.Errorf("unsupported socks5 address type: %d", atyp)
	}
}

func writeSocks5Reply(w io.Writer, rep byte, bindAddr string) error {
	if bindAddr == "" {
		bindAddr = "0.0.0.0:0"
	}
	encodedAddr, err := encodeSocks5Address(bindAddr)
	if err != nil {
		return err
	}

	reply := make([]byte, 0, 3+len(encodedAddr))
	reply = append(reply, socks5Version, rep, 0x00)
	reply = append(reply, encodedAddr...)
	_, err = w.Write(reply)
	return err
}

func parseSocks5UDPDatagram(packet []byte) (targetAddr string, payload []byte, err error) {
	if len(packet) < 4 {
		return "", nil, fmt.Errorf("socks5 udp packet too short: %d", len(packet))
	}
	if packet[0] != 0x00 || packet[1] != 0x00 {
		return "", nil, fmt.Errorf("socks5 udp packet has invalid reserved bytes")
	}
	if packet[2] != 0x00 {
		return "", nil, fmt.Errorf("socks5 udp fragmentation is not supported")
	}

	targetAddr, consumed, err := parseSocks5Address(packet[3:])
	if err != nil {
		return "", nil, err
	}

	payloadStart := 3 + consumed
	if payloadStart > len(packet) {
		return "", nil, fmt.Errorf("invalid socks5 udp payload offset")
	}

	return targetAddr, packet[payloadStart:], nil
}

func buildSocks5UDPDatagram(targetAddr string, payload []byte) ([]byte, error) {
	addr, err := encodeSocks5Address(targetAddr)
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, 3+len(addr)+len(payload))
	out = append(out, 0x00, 0x00, 0x00)
	out = append(out, addr...)
	out = append(out, payload...)
	return out, nil
}

func parseSocks5Address(data []byte) (addr string, consumed int, err error) {
	if len(data) < 1 {
		return "", 0, fmt.Errorf("empty socks5 address payload")
	}

	atyp := data[0]
	switch atyp {
	case socks5AddrIPv4:
		const totalLen = 1 + net.IPv4len + 2
		if len(data) < totalLen {
			return "", 0, fmt.Errorf("truncated ipv4 socks5 address")
		}
		host := net.IP(data[1 : 1+net.IPv4len]).String()
		port := binary.BigEndian.Uint16(data[1+net.IPv4len : totalLen])
		return net.JoinHostPort(host, strconv.Itoa(int(port))), totalLen, nil
	case socks5AddrIPv6:
		const totalLen = 1 + net.IPv6len + 2
		if len(data) < totalLen {
			return "", 0, fmt.Errorf("truncated ipv6 socks5 address")
		}
		host := net.IP(data[1 : 1+net.IPv6len]).String()
		port := binary.BigEndian.Uint16(data[1+net.IPv6len : totalLen])
		return net.JoinHostPort(host, strconv.Itoa(int(port))), totalLen, nil
	case socks5AddrDomain:
		if len(data) < 2 {
			return "", 0, fmt.Errorf("truncated domain socks5 address")
		}
		domainLen := int(data[1])
		totalLen := 1 + 1 + domainLen + 2
		if len(data) < totalLen {
			return "", 0, fmt.Errorf("truncated domain socks5 address")
		}
		if domainLen == 0 {
			return "", 0, fmt.Errorf("invalid empty domain socks5 address")
		}
		host := string(data[2 : 2+domainLen])
		port := binary.BigEndian.Uint16(data[2+domainLen : totalLen])
		return net.JoinHostPort(host, strconv.Itoa(int(port))), totalLen, nil
	default:
		return "", 0, fmt.Errorf("unsupported socks5 address type: %d", atyp)
	}
}

func encodeSocks5Address(addr string) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid socks5 address %q: %w", addr, err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid socks5 port %q", portStr)
	}
	portHi := byte(port >> 8)
	portLo := byte(port)

	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			out := make([]byte, 0, 1+net.IPv4len+2)
			out = append(out, socks5AddrIPv4)
			out = append(out, ip4...)
			out = append(out, portHi, portLo)
			return out, nil
		}
		ip16 := ip.To16()
		if ip16 == nil {
			return nil, fmt.Errorf("invalid ipv6 address %q", host)
		}
		out := make([]byte, 0, 1+net.IPv6len+2)
		out = append(out, socks5AddrIPv6)
		out = append(out, ip16...)
		out = append(out, portHi, portLo)
		return out, nil
	}

	if len(host) == 0 || len(host) > 255 {
		return nil, fmt.Errorf("invalid domain length for %q", host)
	}

	out := make([]byte, 0, 1+1+len(host)+2)
	out = append(out, socks5AddrDomain, byte(len(host)))
	out = append(out, []byte(host)...)
	out = append(out, portHi, portLo)
	return out, nil
}
