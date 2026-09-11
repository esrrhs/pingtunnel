# Pingtunnel 使用与配置指南 / Usage and Configuration Guide

本指南详细介绍 Pingtunnel 的命令行参数、配置文件（`-c`）使用方法，以及在各种典型网络场景下的代理设置。

---

## 目录 / Table of Contents

- [1. 命令行参数详解](#1-命令行参数详解)
  - [服务端参数 (Server Flags)](#服务端参数-server-flags)
  - [客户端参数 (Client Flags)](#客户端参数-client-flags)
- [2. 配置文件使用 (Config File Mode)](#2-配置文件使用-config-file-mode)
  - [服务端配置示例 (Server Config)](#服务端配置示例-server-config)
  - [客户端配置示例 (Client Config)](#客户端配置示例-client-config)
  - [参数优先级 (Precedence)](#参数优先级-precedence)
- [3. 典型代理场景与设置指南](#3-典型代理场景与设置指南)
  - [场景一：SOCKS5 全局代理翻越 / 突破封锁](#场景一socks5-全局代理翻越--突破封锁)
  - [场景二：SOCKS5 国内/国外分流 (基于 GeoIP 过滤)](#场景二socks5-国内国外分流-基于-geoip-过滤)
  - [场景三：远程内网特定 TCP 服务穿透 (如 SSH / 远程桌面)](#场景三远程内网特定-tcp-服务穿透-如-ssh--远程桌面)
  - [场景四：特定 UDP 业务转发 (如 DNS / 游戏服务器)](#场景四特定-udp-业务转发-如-dns--游戏服务器)
  - [场景五：前置/上游二级代理转发 (Forward Proxy)](#场景五前置上游二级代理转发-forward-proxy)
  - [场景六：开启端到端高强度加密传输](#场景六开启端到端高强度加密传输)
- [4. Docker 与守护进程运行](#4-docker-与守护进程运行)

---

## 1. 命令行参数详解

### 服务端参数 (Server Flags)

| 参数 Flag | 默认值 Default | 说明 Description |
|---|---|---|
| `-type` | `""` | 运行角色，必须指定为 `server` 或 `client` |
| `-c` | `""` | 指定 JSON 配置文件路径，命令行参数可覆盖文件配置 |
| `-icmp_l` | `0.0.0.0` | 本地监听 ICMP 流量的网卡 IP |
| `-key` | `0` | 纯数字密码（0 ~ 2147483647），需与客户端一致 |
| `-encrypt` | `""` | 加密算法，支持 `aes128`、`aes256`、`chacha20`（空表示不加密） |
| `-encrypt-key` | `""` | 加密密钥（可为密码短语或 base64 字符串），需与客户端一致 |
| `-maxconn` | `0` | 最大并发连接数限制（0 表示不限制） |
| `-maxprt` | `100` | 服务端数据包最大处理工作协程数 |
| `-maxprb` | `1000` | 服务端处理协程输入缓冲区大小 |
| `-conntt` | `1000` | 服务端向目标地址发起连接的超时时间（毫秒） |
| `-forward` | `""` | 服务端上游前置代理，支持 `socks5://host:port` 或 `http://host:port` |
| `-nolog` | `0` | 设为 `1` 时不写入日志文件，仅控制台输出 |
| `-noprint` | `0` | 设为 `1` 时不向控制台输出日志 |
| `-loglevel` | `info` | 日志级别（`debug`, `info`, `warn`, `error`） |
| `-profile` | `0` | 性能分析（pprof）监听端口，默认不开启 |

### 客户端参数 (Client Flags)

| 参数 Flag | 默认值 Default | 说明 Description |
|---|---|---|
| `-type` | `""` | 运行角色，指定为 `client` |
| `-c` | `""` | 指定 JSON 配置文件路径 |
| `-l` | `""` | 客户端本地监听地址及端口（例如 `:4455` 或 `127.0.0.1:1080`） |
| `-s` | `""` | 远程 Pingtunnel 服务端 IP 或域名 |
| `-t` | `""` | 目标服务的终点地址（例如 `1.1.1.1:53` 或 `目标内网IP:22`，开启 sock5 时可留空） |
| `-sock5` | `0` | 设为 `1` 开启本地 SOCKS5 代理模式（自动开启 TCP） |
| `-s5user` | `""` | 本地 SOCKS5 认证用户名（可选） |
| `-s5pass` | `""` | 本地 SOCKS5 认证密码（可选） |
| `-s5filter`| `""` | SOCKS5 分流过滤国家代码（如 `CN` 表示大陆地区直连，不走隧道） |
| `-s5ftfile`| `GeoLite2-Country.mmdb` | 分流 IP 数据库文件路径 |
| `-tcp` | `0` | 是否按 TCP 模式转发（UDP 业务设为 0，TCP 设为 1） |
| `-tcp_bs` | `1048576` (1MB)| TCP 流量滑动窗口收发缓冲区大小 |
| `-tcp_mw` | `20000` | TCP 模式最大窗口大小 |
| `-tcp_rst` | `400` | TCP 超时重传时间（毫秒） |
| `-tcp_gz` | `0` | 数据包超过指定大小（字节）时启用压缩，0 表示不压缩 |
| `-tcp_stat`| `0` | 设为 `1` 周期性输出 TCP 连接流控统计 |
| `-timeout` | `60` | 连接空闲超时释放时间（秒） |

---

## 2. 配置文件使用 (Config File Mode)

Pingtunnel 支持使用 `-c <config.json>` 启动，无需在命令行拼接冗长参数。

### 命令行加载配置
```bash
# 服务端
sudo pingtunnel -c /etc/pingtunnel/server.json

# 客户端
pingtunnel -c ./client.json
```

### 服务端配置示例 (Server Config)
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

### 客户端配置示例 (Client Config)

#### 1. SOCKS5 代理客户端 (`client-socks5.json`):
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

#### 2. TCP 单端口转发客户端 (`client-tcp.json`):
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

### 参数优先级 (Precedence)
**命令行直接传入的参数优先于配置文件中的同名参数**。例如：
```bash
pingtunnel -c client.json -loglevel debug
```
此时将使用 `client.json` 中的全部配置，但会将日志级别覆盖为 `debug`。

---

## 3. 典型代理场景与设置指南

### 场景一：SOCKS5 全局代理翻越 / 突破封锁
适用于客户机环境被防火墙严格限制 TCP/UDP 外部端口访问，但放行 ICMP (Ping) 回显协议的场景。

1. **服务端启动**：
   ```bash
   sudo ./pingtunnel -type server -key 123456
   ```
2. **客户端启动**：
   ```bash
   ./pingtunnel -type client -l 127.0.0.1:1080 -s <服务端公网IP> -sock5 1 -key 123456
   ```
3. **代理设置**：
   * 在浏览器、SwitchyOmega 或系统中设置 SOCKS5 代理：
     * **主机**：`127.0.0.1`
     * **端口**：`1080`
   * 所有 TCP 网页访问、视频流媒体均将封装进 ICMP Echo 请求发往服务器由其代为请求。

---

### 场景二：SOCKS5 国内/国外分流 (基于 GeoIP 过滤)
当客户端使用 SOCKS5 代理时，希望大陆境内 IP 直连，只有境外 IP 才走 ICMP 隧道。

1. **确保运行目录下存在 `GeoLite2-Country.mmdb`**。
2. **客户端启动**：
   ```bash
   ./pingtunnel -type client -l 127.0.0.1:1080 -s <服务端公网IP> -sock5 1 -key 123456 -s5filter CN
   ```
3. **效果**：客户端访问境内网站时由本机直连，不消耗服务器隧道流量；访问境外网站自动经由隧道中转。

---

### 场景三：远程内网特定 TCP 服务穿透 (如 SSH / 远程桌面)
适用于需要远程访问服务端所在私有局域网内的某台特定主机（例如内网 `192.168.1.100:22` 或 `:3389`）。

1. **服务端启动**：
   ```bash
   sudo ./pingtunnel -type server -key 123456
   ```
2. **客户端启动（映射远程内网 22 到本地 2222）**：
   ```bash
   ./pingtunnel -type client -l :2222 -s <服务端公网IP> -t 192.168.1.100:22 -tcp 1 -key 123456
   ```
3. **发起连接**：
   ```bash
   ssh -p 2222 user@127.0.0.1
   ```
   流量将通过 ICMP 封装送达服务端后，由服务端解包转交至 `192.168.1.100:22`。

---

### 场景四：特定 UDP 业务转发 (如 DNS / 游戏服务器)
适用于本地受限无法直接查询外网 UDP 53 DNS，或游戏 UDP 端口被限速丢包时。

1. **客户端启动（将本地 UDP 5353 转发至远端 8.8.8.8:53）**：
   ```bash
   ./pingtunnel -type client -l :5353 -s <服务端公网IP> -t 8.8.8.8:53 -key 123456
   ```
2. **测试 DNS 解析**：
   ```bash
   dig @127.0.0.1 -p 5353 google.com
   ```

---

### 场景五：前置/上游二级代理转发 (Forward Proxy)
适用于服务端所在网络无法直接连外网，或服务端也需要经过一层公司企业级代理（SOCKS5 / HTTP Proxy）出网。

1. **服务端配置 `-forward`**：
   ```bash
   sudo ./pingtunnel -type server -key 123456 -forward "socks5://127.0.0.1:2080"
   # 或使用 HTTP 代理:
   # sudo ./pingtunnel -type server -key 123456 -forward "http://proxy.corp.internal:8080"
   ```
2. **客户端正常启动连接服务端**：
   ```bash
   ./pingtunnel -type client -l :1080 -s <服务端IP> -sock5 1 -key 123456
   ```
3. 服务端收到 ICMP 解包请求后，将通过二级代理 `127.0.0.1:2080` 进一步向外发起请求。

---

### 场景六：开启端到端高强度加密传输
默认情况下流量仅包含简单的 magic key 校验。在敏感网络环境推荐启用 AEAD 加密（支持 `aes128`, `aes256`, `chacha20`）。

1. **服务端**：
   ```bash
   sudo ./pingtunnel -type server -key 123456 -encrypt chacha20 -encrypt-key "P@ssw0rdCustomSecret!"
   ```
2. **客户端**：
   ```bash
   ./pingtunnel -type client -l :1080 -s <服务端IP> -sock5 1 -key 123456 -encrypt chacha20 -encrypt-key "P@ssw0rdCustomSecret!"
   ```
3. ICMP 包内的 Protobuf Payload 将全部被 ChaCha20-Poly1305 加密，防御 DPI 协议特征检测。

---

## 4. Docker 与守护进程运行

### 服务端 Docker 运行（轻量镜像，推荐）
```bash
docker run -d --name pingtunnel-server \
  --restart=always \
  --privileged \
  --network host \
  esrrhs/pingtunnel ./pingtunnel -type server -key 123456
```

### 挂载配置文件运行
```bash
docker run -d --name pingtunnel-server \
  --restart=always \
  --privileged \
  --network host \
  -v /etc/pingtunnel/server.json:/app/server.json:ro \
  esrrhs/pingtunnel ./pingtunnel -c /app/server.json
```
