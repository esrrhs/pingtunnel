# Pingtunnel Usage and Configuration Guide

This document covers the current config file format, a detailed CLI flag reference, typical proxy scenarios, and performance tuning tips.

---

## Table of Contents

- [1. Config File Mode](#1-config-file-mode)
  - [Usage](#usage)
  - [JSON Field Reference](#json-field-reference)
  - [Config Templates](#config-templates)
  - [Flag Override Precedence](#flag-override-precedence)
- [2. CLI Flags Reference](#2-cli-flags-reference)
  - [Server Flags](#server-flags)
  - [Client Flags](#client-flags)
- [3. Typical Scenarios](#3-typical-scenarios)
  - [Scenario 1: Global SOCKS5 proxy (browser / app traffic)](#scenario-1-global-socks5-proxy-browser--app-traffic)
  - [Scenario 2: Reaching a remote internal TCP port (SSH / RDP / Web)](#scenario-2-reaching-a-remote-internal-tcp-port-ssh--rdp--web)
  - [Scenario 3: UDP port forwarding (DNS 53 / games)](#scenario-3-udp-port-forwarding-dns-53--games)
  - [Scenario 4: Server-side upstream relay (Forward Proxy)](#scenario-4-server-side-upstream-relay-forward-proxy)
  - [Scenario 5: Full-traffic AEAD encryption](#scenario-5-full-traffic-aead-encryption)
- [4. Tuning & Troubleshooting](#4-tuning--troubleshooting)
  - [Congestion Control (BBR-like)](#congestion-control-bbr-like)
  - [ICMP Privileges and Kernel Echo Replies](#icmp-privileges-and-kernel-echo-replies)
- [5. Docker and Background Deployment](#5-docker-and-background-deployment)

---

## 1. Config File Mode

Pingtunnel natively supports JSON config files, which makes persistent setups and service management (e.g. systemd) easy.

### Usage

Load a config file in any of the following ways:

```bash
# Option A: pass it as the first argument (simplest)
sudo ./pingtunnel config-server.json
./pingtunnel config-client.json

# Option B: use -c or -config
sudo ./pingtunnel -c /etc/pingtunnel/server.json
./pingtunnel -config ./client.json

# Option C: automatic loading (when config.json exists in the current directory and -type is not given)
./pingtunnel
```

---

### JSON Field Reference

| JSON field | Type | Role | Default | CLI flag | Description |
|---|---|---|---|---|---|
| `type` | string | Both | `""` | `-type` | Node type, must be `"server"` or `"client"` |
| `password` | string | Both | — | `-password` | **Required**: shared secret for authentication and encryption, at least 8 characters, must match on both ends |
| `encrypt` | string | Both | `"chacha20"` | `-encrypt` | Encryption algorithm: `"chacha20"` (default), `"aes256"`, `"aes128"` |
| `listen` | string | Client | `""` | `-l` | Local listen address of the client, e.g. `":1080"` or `"127.0.0.1:4455"` |
| `server` | string | Client | `""` | `-s` | IP address or domain of the remote Pingtunnel server |
| `target` | string | Client | `""` | `-t` | Forwarding target address (e.g. `"192.168.1.100:22"`); may be omitted in SOCKS5 mode |
| `socks5` | bool | Client | `false` | `-socks5` | Enable the local SOCKS5 proxy (TCP mode is enabled automatically) |
| `s5user` | string | Client | `""` | `-s5user` | Username for the local SOCKS5 proxy (optional) |
| `s5pass` | string | Client | `""` | `-s5pass` | Password for the local SOCKS5 proxy (optional) |
| `udp` | bool | Client | `false` | `-udp` | Forward in UDP mode (TCP is the default) |
| `icmp_listen`| string | Both | `"0.0.0.0"` | `-icmp_l` | Local interface IP used to send and receive ICMP packets |
| `timeout` | int | Client | `60` | `-timeout` | Idle connection timeout (seconds) |
| `congestion` | string | Both | `"bb"` | `-congestion` | Congestion control: `"bb"` (bandwidth-adaptive, avoids saturating the link) or `""` (disabled) |
| `forward` | string | Server | `""` | `-forward` | Upstream relay proxy of the server (e.g. `"socks5://127.0.0.1:2080"` or `"http://proxy:8080"`) |
| `maxconn` | int | Both | `0` | `-maxconn` | Maximum concurrent connections (`0` = unlimited) |
| `conntt` | int | Server | `3000` | `-conntt` | Timeout (ms) for the server connecting to the target |
| `nolog` | bool | Both | `false` | `-nolog` | Do not write a log file (`true` = print to stdout only) |
| `loglevel` | string | Both | `"info"` | `-loglevel` | Log level (`"debug"`, `"info"`, `"warn"`, `"error"`) |
| `quiet` | bool | Both | `false` | `-quiet` | Do not print logs to the console (`true` = silent) |
| `profile` | int | Both | `0` | `-profile` | Port for Go pprof profiling (`0` disables) |

> 💡 **Migration Notes**:
> The legacy `"key"`, `"encrypt_key"`, `"sock5"`, `"tcp"`, `"tcp_bs"`, `"tcp_mw"`, `"tcp_rst"`, `"tcp_gz"`, `"tcp_stat"`, `"maxprt"`, `"maxprb"` and `"noprint"` fields were removed or renamed in v3 (use `"password"`, `"socks5"`, `"udp"` and `"quiet"` instead; the TCP tuning and worker options are now managed automatically). If a config file still contains them, the program exits with an error and a migration hint, so old configs never fail silently.

---

### Config Templates

#### 1. Standard server config (`config-server.json`)
```json
{
  "type": "server",
  "password": "YourSecureSecretPassword123!",
  "encrypt": "chacha20",
  "icmp_listen": "0.0.0.0",
  "maxconn": 500,
  "loglevel": "info",
  "nolog": false
}
```

#### 2. SOCKS5 proxy client config (`config-client-socks5.json`)
```json
{
  "type": "client",
  "listen": "127.0.0.1:1080",
  "server": "your-server-ip.com",
  "socks5": true,
  "password": "YourSecureSecretPassword123!",
  "encrypt": "chacha20",
  "loglevel": "info"
}
```

#### 3. SOCKS5 client config with user authentication
```json
{
  "type": "client",
  "listen": "127.0.0.1:1080",
  "server": "your-server-ip.com",
  "socks5": true,
  "s5user": "admin",
  "s5pass": "mySecretS5Password",
  "password": "YourSecureSecretPassword123!"
}
```

#### 4. Remote internal TCP port client config (`config-client-tcp.json`)
```json
{
  "type": "client",
  "listen": ":2222",
  "server": "your-server-ip.com",
  "target": "192.168.1.100:22",
  "password": "YourSecureSecretPassword123!"
}
```

---

### Flag Override Precedence
**Flags given explicitly on the command line always take the highest priority and override the same settings in the config file.**
```bash
# Example: use everything from client.json, but temporarily raise loglevel to debug
./pingtunnel client.json -loglevel debug
```

---

## 2. CLI Flags Reference

### Server Flags

```bash
sudo ./pingtunnel -type server [OPTIONS]
```

| Flag | Default | Description |
|---|---|---|
| `-type` | `""` | Must be `server` |
| `-password` | — | **Required**: authentication and encryption secret shared with the client (at least 8 characters) |
| `-encrypt` | `chacha20` | Encryption algorithm: `chacha20` (default), `aes256`, `aes128` |
| `-c`, `-config` | `""` | Path of the JSON config file to load |
| `-icmp_l` | `0.0.0.0` | Interface address to listen on for ICMP packets |
| `-forward` | `""` | Upstream secondary proxy, `socks5://host:port` or `http://host:port` |
| `-congestion`| `bb` | Congestion control algorithm, `bb` by default; pass an empty string `""` to disable |
| `-maxconn` | `0` | Maximum concurrent connections (`0` = unlimited) |
| `-conntt` | `3000` | Timeout for connecting to the target (ms) |
| `-nolog` | `false` | Do not write a log file (print to stdout only) |
| `-quiet` | `false` | Silence console output |
| `-profile` | `0` | Port for Go pprof profiling (`0` disables) |
| `-loglevel` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `-v`, `-version` | `false`| Print the version and Git build info |

---

### Client Flags

```bash
./pingtunnel -type client [OPTIONS]
```

| Flag | Default | Description |
|---|---|---|
| `-type` | `""` | Must be `client` |
| `-s` | `""` | IP or domain of the remote Pingtunnel server |
| `-l` | `""` | Local listen address and port (e.g. `127.0.0.1:1080` or `:4455`) |
| `-password` | — | **Required**: authentication and encryption secret shared with the server (at least 8 characters) |
| `-socks5` | `false` | Boolean switch enabling the local SOCKS5 proxy (TCP is enabled automatically) |
| `-t` | `""` | Forwarding target address (required unless in socks5 mode, e.g. `8.8.8.8:53` or `10.0.0.5:22`) |
| `-udp` | `false` | Forward in UDP mode (TCP is the default) |
| `-s5user` | `""` | Local SOCKS5 username (optional) |
| `-s5pass` | `""` | Local SOCKS5 password (optional) |
| `-encrypt` | `chacha20` | Encryption algorithm: `chacha20` (default), `aes256`, `aes128` |
| `-timeout` | `60` | Idle timeout of local connections (seconds) |
| `-maxconn` | `0` | Maximum concurrent connections (`0` = unlimited) |
| `-icmp_l` | `0.0.0.0` | Local interface address used for ICMP traffic |
| `-nolog` | `false` | Do not write a log file |
| `-quiet` | `false` | Silence console output |
| `-loglevel` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `-profile` | `0` | Port for Go pprof profiling (`0` disables) |
| `-c`, `-config` | `""` | Path of the JSON config file to load |
| `-congestion`| `bb` | Congestion control algorithm, `bb` by default; pass an empty string `""` to disable |

---

## 3. Typical Scenarios

### Scenario 1: Global SOCKS5 proxy (browser / app traffic)
**Problem**: the client's LAN blocks all non-standard TCP/UDP connections but still allows ping.

1. **Start the server**:
   ```bash
   sudo ./pingtunnel -type server -password YOUR_PASSWORD
   ```
2. **Start the client**:
   ```bash
   ./pingtunnel -type client -l 127.0.0.1:1080 -s <SERVER_IP> -socks5 -password YOUR_PASSWORD
   ```
3. **Use it from a terminal or application**:
   ```bash
   curl -x socks5h://127.0.0.1:1080 https://www.google.com
   ```

---

### Scenario 2: Reaching a remote internal TCP port (SSH / RDP / Web)
**Problem**: an internal host `192.168.1.100` behind the server runs SSH (port 22) and you need to connect to it directly and securely from outside.

1. **Start the server**:
   ```bash
   sudo ./pingtunnel -type server -password YOUR_PASSWORD
   ```
2. **Map local port 2222 to remote internal 192.168.1.100:22 on the client (TCP forwarding by default)**:
   ```bash
   ./pingtunnel -type client -l :2222 -s <SERVER_IP> -t 192.168.1.100:22 -password YOUR_PASSWORD
   ```
3. **Verify the connection**:
   ```bash
   ssh -p 2222 user@127.0.0.1
   ```

---

### Scenario 3: UDP port forwarding (DNS 53 / games)
**Problem**: local DNS requests are hijacked or poisoned, or a game's UDP port is throttled by ISP deep packet inspection.

1. **Tunnel local UDP port 5353 through the server to 8.8.8.8:53**:
   ```bash
   ./pingtunnel -type client -l :5353 -s <SERVER_IP> -t 8.8.8.8:53 -udp -password YOUR_PASSWORD
   ```
2. **Test DNS resolution**:
   ```bash
   dig @127.0.0.1 -p 5353 google.com
   ```

---

### Scenario 4: Server-side upstream relay (Forward Proxy)
**Problem**: the Pingtunnel server cannot reach some destinations directly and must go through a corporate HTTP proxy or another SOCKS5 server.

1. **Set `-forward` on the server**:
   ```bash
   sudo ./pingtunnel -type server -password YOUR_PASSWORD -forward "socks5://127.0.0.1:2080"
   # or an HTTP proxy:
   # sudo ./pingtunnel -type server -password YOUR_PASSWORD -forward "http://proxy.corp.internal:8080"
   ```
2. **Use the client as usual**:
   All client traffic is unwrapped from ICMP by the server and automatically relayed through the proxy at `127.0.0.1:2080`.

---

### Scenario 5: Full-traffic AEAD encryption
- Since v3, all payloads carried inside ICMP are always end-to-end encrypted and tamper-protected.
- The default is the modern, high-performance AEAD cipher **ChaCha20-Poly1305** (well suited for mobile devices and platforms without hardware AES acceleration).
- If your platform has hardware AES instructions (AES-NI), you can switch to AES:
  ```bash
  # Use the same setting on server and client
  ./pingtunnel -type server -password YOUR_PASSWORD -encrypt aes256
  ./pingtunnel -type client -l :1080 -s <IP> -socks5 -password YOUR_PASSWORD -encrypt aes256
  ```

---

## 4. Tuning & Troubleshooting

### Congestion Control (BBR-like)
During high-throughput downloads (tens of MB/s) or over long international links, packet loss on weak networks easily causes TCP bufferbloat and tunnel disconnects.
- Pingtunnel enables the adaptive `-congestion bb` algorithm by default, adjusting its send pace dynamically based on round-trip time (RTT).
- If latency is very low and very stable (e.g. a LAN), pass `-congestion ""` to disable flow control and squeeze out maximum single-core throughput.

### ICMP Privileges and Kernel Echo Replies
1. **Avoid kernel conflicts on Linux**:
   The Linux kernel automatically answers ICMP Echo Requests with Echo Replies, which may interfere with flow control in some complex environments. Disable the default echo on the server, temporarily or permanently:
   ```bash
   # Temporarily
   echo 1 > /proc/sys/net/ipv4/icmp_echo_ignore_all

   # Permanently (write to sysctl.conf)
   echo "net.ipv4.icmp_echo_ignore_all = 1" >> /etc/sysctl.conf && sysctl -p
   ```
2. **Running without root (Linux capabilities)**:
   To avoid `sudo` for the server or client, grant the binary the `CAP_NET_RAW` capability:
   ```bash
   sudo setcap cap_net_raw+ep ./pingtunnel
   ./pingtunnel -type server -password YOUR_PASSWORD
   ```

---

## 5. Docker and Background Deployment

### Docker Compose example

Server `docker-compose.yml`:
```yaml
services:
  pingtunnel:
    image: esrrhs/pingtunnel:latest
    container_name: pingtunnel-server
    restart: always
    network_mode: host
    privileged: true
    command: ["./pingtunnel", "-type", "server", "-password", "MyCustomPassword123"]
```

### Systemd service (`/etc/systemd/system/pingtunnel.service`)
```ini
[Unit]
Description=Pingtunnel ICMP Tunnel Service
After=network.target

[Service]
Type=simple
WorkingDirectory=/etc/pingtunnel
ExecStart=/usr/local/bin/pingtunnel /etc/pingtunnel/server.json
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```
Start and manage it:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now pingtunnel
sudo systemctl status pingtunnel
```
