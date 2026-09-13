# Pingtunnel Usage and Configuration Guide

This guide provides detailed documentation on Pingtunnel command-line flags, JSON configuration file (`-c`) usage, and proxy setup recipes for typical network scenarios.

---

## Table of Contents

- [1. Command-Line Flags](#1-command-line-flags)
  - [Server Flags](#server-flags)
  - [Client Flags](#client-flags)
- [2. Config File Mode](#2-config-file-mode)
  - [Server Config Example](#server-config-example)
  - [Client Config Example](#client-config-example)
  - [Flag Precedence](#flag-precedence)
- [3. Typical Proxy Scenarios](#3-typical-proxy-scenarios)
  - [Scenario 1: Global SOCKS5 Proxy](#scenario-1-global-socks5-proxy)
  - [Scenario 2: GeoIP Split Routing (Domestic / International Bypass)](#scenario-2-geoip-split-routing-domestic--international-bypass)
  - [Scenario 3: Specific Remote TCP Port Forwarding (e.g. SSH / RDP)](#scenario-3-specific-remote-tcp-port-forwarding-eg-ssh--rdp)
  - [Scenario 4: Specific UDP Traffic Forwarding (e.g. DNS / Gaming)](#scenario-4-specific-udp-traffic-forwarding-eg-dns--gaming)
  - [Scenario 5: Upstream Forward Proxy](#scenario-5-upstream-forward-proxy)
  - [Scenario 6: End-to-End High-Strength Encryption](#scenario-6-end-to-end-high-strength-encryption)
- [4. Docker and Daemon Execution](#4-docker-and-daemon-execution)

---

## 1. Command-Line Flags

### Server Flags

| Flag | Default | Description |
|---|---|---|
| `-type` | `""` | Operating role, must be `server` or `client` |
| `-c` | `""` | Path to JSON config file (CLI flags override config values) |
| `-icmp_l` | `0.0.0.0` | Local network interface IP to listen for ICMP traffic |
| `-key` | `0` | Numeric key / authentication code (`0` - `2147483647`), must match client |
| `-encrypt` | `""` | Encryption algorithm: `aes128`, `aes256`, or `chacha20` (empty means disabled) |
| `-encrypt-key` | `""` | Encryption key (passphrase or base64 string), must match client |
| `-maxconn` | `0` | Maximum concurrent connections limit (`0` for unlimited) |
| `-maxprt` | `100` | Maximum packet processing worker goroutines on server |
| `-maxprb` | `1000` | Input buffer size for server packet processing workers |
| `-conntt` | `1000` | Connect timeout to destination address in milliseconds |
| `-forward` | `""` | Upstream forward proxy, supports `socks5://host:port` or `http://host:port` |
| `-congestion` | `bb` | Congestion control algorithm; default `bb` (bandwidth-adaptive algorithm similar to BBR to prevent bufferbloat / disconnections during heavy downloads). Pass empty string `""` to disable |
| `-nolog` | `0` | Set to `1` to disable writing to log files (console only) |
| `-noprint` | `0` | Set to `1` to suppress console output |
| `-loglevel` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `-profile` | `0` | Performance profiling (pprof) listening port (`0` to disable) |
| `-v`, `-version` | `false` | Print version and build information |

### Client Flags

| Flag | Default | Description |
|---|---|---|
| `-type` | `""` | Operating role, specified as `client` |
| `-c` | `""` | Path to JSON config file |
| `-l` | `""` | Local listen address and port (e.g. `:4455` or `127.0.0.1:1080`) |
| `-s` | `""` | Remote Pingtunnel server IP or domain name |
| `-t` | `""` | Destination target address (e.g. `1.1.1.1:53` or internal IP `10.0.0.5:22`; can be omitted when `sock5` is enabled) |
| `-sock5` | `0` | Set to `1` to enable local SOCKS5 proxy mode (automatically enables TCP) |
| `-s5user` | `""` | Local SOCKS5 username authentication (optional) |
| `-s5pass` | `""` | Local SOCKS5 password authentication (optional) |
| `-s5filter`| `""` | SOCKS5 split routing country code (e.g. `CN` to connect directly without tunneling) |
| `-s5ftfile`| `GeoLite2-Country.mmdb` | GeoIP database file path for split routing |
| `-congestion` | `bb` | Congestion control algorithm; default `bb` (bandwidth-adaptive algorithm similar to BBR to prevent bufferbloat / disconnections during heavy downloads). Pass empty string `""` to disable |
| `-tcp` | `0` | Whether to forward in TCP mode (`0` for UDP service, `1` for TCP) |
| `-tcp_bs` | `1048576` (1MB)| TCP sliding window send/receive buffer size |
| `-tcp_mw` | `20000` | Maximum TCP window size |
| `-tcp_rst` | `400` | TCP retransmission timeout in milliseconds |
| `-tcp_gz` | `0` | Enable compression when packet size exceeds this threshold (bytes); `0` to disable |
| `-tcp_stat`| `0` | Set to `1` to periodically output TCP flow control statistics |
| `-timeout` | `60` | Connection idle timeout in seconds before release |
| `-v`, `-version` | `false` | Print version and build information |

---

## 2. Config File Mode

Pingtunnel supports `-c <config.json>` to load settings from a file instead of passing long command-line arguments.

### Running with Config File
```bash
# Server
sudo pingtunnel -c /etc/pingtunnel/server.json

# Client
pingtunnel -c ./client.json
```

### Server Config Example
`server.json`:
```json
{
  "type": "server",
  "key": 888888,
  "encrypt": "aes128",
  "encrypt_key": "MySecureSecretKey",
  "icmp_listen": "0.0.0.0",
  "maxconn": 500,
  "loglevel": "info",
  "nolog": 0
}
```

### Client Config Example

#### 1. SOCKS5 Proxy Client (`client-socks5.json`):
```json
{
  "type": "client",
  "listen": ":1080",
  "server": "your-server-ip.com",
  "sock5": 1,
  "key": 888888,
  "encrypt": "aes128",
  "encrypt_key": "MySecureSecretKey",
  "s5user": "myuser",
  "s5pass": "mypass",
  "loglevel": "info"
}
```

#### 2. TCP Port Forwarding Client (`client-tcp.json`):
```json
{
  "type": "client",
  "listen": ":2222",
  "server": "your-server-ip.com",
  "target": "10.0.0.5:22",
  "tcp": 1,
  "key": 888888
}
```

### Flag Precedence
**Command-line arguments take precedence over matching keys in the configuration file.** For example:
```bash
pingtunnel -c client.json -loglevel debug
```
This applies all options from `client.json` while overriding `loglevel` to `debug`.

---


## 3. Typical Proxy Scenarios

### Scenario 1: Global SOCKS5 Proxy
Useful when client networks block outbound TCP/UDP traffic but allow ICMP (Ping) Echo packets.

1. **Start Server**:
   ```bash
   sudo ./pingtunnel -type server -key 123456
   ```
2. **Start Client**:
   ```bash
   ./pingtunnel -type client -l 127.0.0.1:1080 -s <SERVER_IP> -sock5 1 -key 123456
   ```
3. **Configure Proxy**:
   * Configure SOCKS5 proxy in your browser, SwitchyOmega, or operating system:
     * **Host**: `127.0.0.1`
     * **Port**: `1080`
   * TCP web requests and media streams will be encapsulated into ICMP Echo packets and relayed by the server.

---

### Scenario 2: GeoIP Split Routing (Domestic / International Bypass)
Allows domestic destination IPs to connect directly without tunneling, while routing foreign IPs through the ICMP tunnel.

1. **Ensure `GeoLite2-Country.mmdb` is placed in the working directory.**
2. **Start Client**:
   ```bash
   ./pingtunnel -type client -l 127.0.0.1:1080 -s <SERVER_IP> -sock5 1 -key 123456 -s5filter CN
   ```
3. **Behavior**: Direct connection is used for domestic (CN) addresses without consuming tunnel bandwidth; foreign destinations are routed through the tunnel.

---

### Scenario 3: Specific Remote TCP Port Forwarding (e.g. SSH / RDP)
Useful when exposing a service inside the server's private network (e.g. `192.168.1.100:22` or `:3389`).

1. **Start Server**:
   ```bash
   sudo ./pingtunnel -type server -key 123456
   ```
2. **Start Client (maps remote private port 22 to local port 2222)**:
   ```bash
   ./pingtunnel -type client -l :2222 -s <SERVER_IP> -t 192.168.1.100:22 -tcp 1 -key 123456
   ```
3. **Connect**:
   ```bash
   ssh -p 2222 user@127.0.0.1
   ```
   Traffic is encapsulated over ICMP to the server, which forwards it to `192.168.1.100:22`.

---

### Scenario 4: Specific UDP Traffic Forwarding (e.g. DNS / Gaming)
Useful when local networks restrict direct outbound UDP port 53 (DNS) or throttle game UDP traffic.

1. **Start Client (forwards local UDP 5353 to remote 8.8.8.8:53)**:
   ```bash
   ./pingtunnel -type client -l :5353 -s <SERVER_IP> -t 8.8.8.8:53 -key 123456
   ```
2. **Test DNS Resolution**:
   ```bash
   dig @127.0.0.1 -p 5353 google.com
   ```

---

### Scenario 5: Upstream Forward Proxy
Useful when the Pingtunnel server cannot directly reach external internet services and needs an enterprise egress proxy (SOCKS5 / HTTP Proxy).

1. **Configure `-forward` on Server**:
   ```bash
   sudo ./pingtunnel -type server -key 123456 -forward "socks5://127.0.0.1:2080"
   # Or with an HTTP proxy:
   # sudo ./pingtunnel -type server -key 123456 -forward "http://proxy.corp.internal:8080"
   ```
2. **Start Client normally**:
   ```bash
   ./pingtunnel -type client -l :1080 -s <SERVER_IP> -sock5 1 -key 123456
   ```
3. Upon receiving encapsulated requests via ICMP, the server relays traffic through `127.0.0.1:2080`.

---

### Scenario 6: End-to-End High-Strength Encryption
By default, traffic uses numeric key verification. In sensitive network environments, AEAD encryption (`aes128`, `aes256`, `chacha20`) can be enabled.

1. **Server**:
   ```bash
   sudo ./pingtunnel -type server -key 123456 -encrypt chacha20 -encrypt-key "P@ssw0rdCustomSecret!"
   ```
2. **Client**:
   ```bash
   ./pingtunnel -type client -l :1080 -s <SERVER_IP> -sock5 1 -key 123456 -encrypt chacha20 -encrypt-key "P@ssw0rdCustomSecret!"
   ```
3. The Protobuf payload inside ICMP packets is encrypted using ChaCha20-Poly1305 to resist deep packet inspection (DPI).

---

## 4. Docker and Daemon Execution

### Server Docker Run (Slim Image, Recommended)
```bash
docker run -d --name pingtunnel-server \
  --restart=always \
  --privileged \
  --network host \
  esrrhs/pingtunnel ./pingtunnel -type server -key 123456
```

### Running with Mounted Config File
```bash
docker run -d --name pingtunnel-server \
  --restart=always \
  --privileged \
  --network host \
  -v /etc/pingtunnel/server.json:/app/server.json:ro \
  esrrhs/pingtunnel ./pingtunnel -c /app/server.json
```

