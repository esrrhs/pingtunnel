# Pingtunnel 完整使用与参数调优指南 (Usage and Configuration Guide)

本文档提供 Pingtunnel 最新的配置文件格式、命令行参数详解、典型网络代理场景实操以及性能调优指南。

---

## 目录 (Table of Contents)

- [1. 配置文件模式 (Config File Mode)](#1-配置文件模式-config-file-mode)
  - [使用方式](#使用方式)
  - [JSON 配置字段完整对照表](#json-配置字段完整对照表)
  - [多场景配置模板](#多场景配置模板)
  - [参数覆盖优先级规则](#参数覆盖优先级规则)
- [2. 命令行参数参考 (CLI Flags Reference)](#2-命令行参数参考-cli-flags-reference)
  - [服务端参数 (Server Flags)](#服务端参数-server-flags)
  - [客户端参数 (Client Flags)](#客户端参数-client-flags)
- [3. 典型实战拓扑与场景 (Typical Scenarios)](#3-典型实战拓扑与场景-typical-scenarios)
  - [场景 1：SOCKS5 全局代理（科学上网/浏览器穿透）](#场景-1socks5-全局代理科学上网浏览器穿透)
  - [场景 2：远程内网特定 TCP 端口穿透（SSH / 远程桌面 / Web）](#场景-2远程内网特定-tcp-端口穿透ssh--远程桌面--web)
  - [场景 3：特定 UDP 端口转发（DNS 53 / 游戏防丢包）](#场景-3特定-udp-端口转发dns-53--游戏防丢包)
  - [场景 4：服务端上游中继代理 (Forward Proxy)](#场景-4服务端上游中继代理-forward-proxy)
  - [场景 5：全流量 AEAD 强加密机制](#场景-5全流量-aead-强加密机制)
- [4. 高级性能调优与排错 (Tuning & Troubleshooting)](#4-高级性能调优与排错-tuning--troubleshooting)
  - [拥塞控制与流控 (BBR-like Congestion Control)](#拥塞控制与流控-bbr-like-congestion-control)
  - [系统底层 ICMP 权限与内核回显响应优化](#系统底层-icmp-权限与内核回显响应优化)
- [5. Docker 与后台运行部署](#5-docker-与后台运行部署)

---

## 1. 配置文件模式 (Config File Mode)

Pingtunnel 原生支持通过 JSON 配置文件启动，便于持久化维护和系统服务管理（如 systemd）。

### 使用方式

你可以通过以下任一方式加载配置文件：

```bash
# 方式 A：直接作为首个参数传入（最简）
sudo ./pingtunnel config-server.json
./pingtunnel config-client.json

# 方式 B：通过 -c 或 -config 参数指定
sudo ./pingtunnel -c /etc/pingtunnel/server.json
./pingtunnel -config ./client.json

# 方式 C：缺省自动加载（当前目录下存在 config.json 且未指定 -type 时自动生效）
./pingtunnel
```

---

### JSON 配置字段完整对照表

| JSON 字段名 | 类型 | 适用角色 | 默认值 | 对应 CLI 参数 | 说明 |
|---|---|---|---|---|---|
| `type` | string | 双端 | `""` | `-type` | 节点类型，必须为 `"server"` 或 `"client"` |
| `password` | string | 双端 | — | `-password` | **核心必填**：认证与加密共享密钥，最小 8 字符，双端必须一致 |
| `encrypt` | string | 双端 | `"chacha20"` | `-encrypt` | 加密算法重载：`"chacha20"`（默认）、`"aes256"`、`"aes128"` |
| `listen` | string | Client | `""` | `-l` | 客户端本地侦听地址，如 `":1080"` 或 `"127.0.0.1:4455"` |
| `server` | string | Client | `""` | `-s` | 远端 Pingtunnel 服务端 IP 地址或域名 |
| `target` | string | Client | `""` | `-t` | 穿透转发的目标地址（如 `"192.168.1.100:22"`），SOCKS5 模式下可省略 |
| `socks5` | bool | Client | `false` | `-socks5` | 是否开启本地 SOCKS5 代理服务（开启后自动启用 TCP 模式） |
| `s5user` | string | Client | `""` | `-s5user` | 本地 SOCKS5 代理认证用户名（可选） |
| `s5pass` | string | Client | `""` | `-s5pass` | 本地 SOCKS5 代理认证密码（可选） |
| `tcp` | int | Client | `1` | `-tcp` | 是否按 TCP 模式转发（`1` 为 TCP，`0` 为 UDP，单端口转发默认启用 TCP） |
| `udp` | bool | Client | `false`| `-udp` | 开启 UDP 转发模式（优先级高于默认的 TCP 模式） |
| `icmp_listen`| string | 双端 | `"0.0.0.0"` | `-icmp_l` | 本地用于收发 ICMP 报文的网络接口 IP |
| `timeout` | int | Client | `60` | `-timeout` | 连接空闲超时释放时间（秒） |
| `congestion` | string | 双端 | `"bb"` | `-congestion` | 拥塞控制算法：`"bb"`（带宽自适应算法，防打满断流）或 `""`（禁用） |
| `forward` | string | Server | `""` | `-forward` | 服务端上游中继代理（如 `"socks5://127.0.0.1:2080"` 或 `"http://proxy:8080"`） |
| `maxconn` | int | 双端 | `0` | `-maxconn` | 最大允许并发连接数（`0` 表示不限） |
| `maxprt` | int | Server | `100` | `-maxprt` | (高级/内部) 服务端报文并行处理的工作协程数 |
| `maxprb` | int | Server | `1000` | `-maxprb` | (高级/内部) 服务端报文处理队列缓冲区大小 |
| `conntt` | int | Server | `3000` | `-conntt` | 服务端向目标 target 发起连接的超时毫秒数 |
| `tcp_bs` | int | Client | `1048576` | `-tcp_bs` | (高级/内部) TCP 模式滑动窗口收发缓冲区大小（字节），默认 1MB |
| `tcp_mw` | int | Client | `20000` | `-tcp_mw` | (高级/内部) TCP 模式最大发送窗口（帧数） |
| `tcp_rst` | int | Client | `400` | `-tcp_rst` | (高级/内部) TCP 帧重传检测周期（毫秒） |
| `tcp_gz` | int | Client | `0` | `-tcp_gz` | (高级/内部) 数据包超过指定大小（字节）时自动启用 Gzip 压缩，`0` 为禁用 |
| `tcp_stat` | int | Client | `0` | `-tcp_stat` | (高级/内部) 是否定期在日志中打印 TCP 流控与发包统计（`1` 开启，`0` 关闭） |
| `nolog` | int | 双端 | `0` | `-nolog` | 是否禁止写日志文件（`1` 表示只打印到标准输出） |
| `noprint` | int | 双端 | `0` | `-noprint` | 是否禁止向控制台标准输出打印日志（`1` 开启静默） |
| `loglevel` | string | 双端 | `"info"` | `-loglevel` | 日志打印等级（`"debug"`, `"info"`, `"warn"`, `"error"`） |
| `profile` | int | 双端 | `0` | `-profile` | Go pprof 性能分析侦听端口（`0` 为禁用） |

> 💡 **版本迁移提示 (Migration Notes)**：
> 旧版本中的 `"key"`、`"encrypt_key"` 以及 `"sock5"` 字段已在 v3 中废弃。若配置文件中依然包含这些字段，程序会直接报错并提示修正，防止旧配置静默失效。

---

### 多场景配置模板

#### 1. 服务端标准配置 (`config-server.json`)
```json
{
  "type": "server",
  "password": "YourSecureSecretPassword123!",
  "encrypt": "chacha20",
  "icmp_listen": "0.0.0.0",
  "maxconn": 500,
  "loglevel": "info",
  "nolog": 0
}
```

#### 2. SOCKS5 代理客户端配置 (`config-client-socks5.json`)
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

#### 3. 带用户鉴权的 SOCKS5 客户端配置
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

#### 4. 远程内网 TCP 端口穿透客户端配置 (`config-client-tcp.json`)
```json
{
  "type": "client",
  "listen": ":2222",
  "server": "your-server-ip.com",
  "target": "192.168.1.100:22",
  "tcp": 1,
  "password": "YourSecureSecretPassword123!"
}
```

---

### 参数覆盖优先级规则
**命令行显式指定的参数始终拥有最高优先级，会自动覆盖配置文件中的同名项。**
```bash
# 例如：采用 client.json 的所有参数，但临时将 loglevel 提升为 debug
./pingtunnel client.json -loglevel debug
```

---

## 2. 命令行参数参考 (CLI Flags Reference)

### 服务端参数 (Server Flags)

```bash
sudo ./pingtunnel -type server [OPTIONS]
```

| 参数 | 默认值 | 作用说明 |
|---|---|---|
| `-type` | `""` | 必须指定为 `server` |
| `-password` | — | **必填**：与客户端一致的认证及加密密钥（最少 8 字符） |
| `-encrypt` | `"chacha20"` | 加密算法重载：`chacha20`（默认）、`aes256`、`aes128` |
| `-c`, `-config` | `""` | 指定读取的 JSON 配置文件路径 |
| `-icmp_l` | `0.0.0.0` | 侦听 ICMP 报文的网卡地址 |
| `-forward` | `""` | 上游二级代理，支持 `socks5://host:port` 或 `http://host:port` |
| `-congestion`| `bb` | 拥塞控制算法，默认 `bb`，传空字符串 `""` 禁用 |
| `-maxconn` | `0` | 最大并发连接数限制（`0` 不限） |
| `-maxprt` | `100` | 服务端报文分发处理的工作协程数 |
| `-maxprb` | `1000` | 报文处理缓冲区大小 |
| `-conntt` | `3000` | 向目的 target 建联超时时间（毫秒） |
| `-nolog` | `0` | 设为 `1` 时不写入日志文件 |
| `-noprint` | `0` | 设为 `1` 时静默控制台标准输出 |
| `-loglevel` | `info` | 日志等级：`debug`, `info`, `warn`, `error` |
| `-v`, `-version` | `false`| 打印版本号及 Git 构建信息 |

---

### 客户端参数 (Client Flags)

```bash
./pingtunnel -type client [OPTIONS]
```

| 参数 | 默认值 | 作用说明 |
|---|---|---|
| `-type` | `""` | 必须指定为 `client` |
| `-s` | `""` | 远端 Pingtunnel 服务端 IP 或域名 |
| `-l` | `""` | 本地服务侦听地址与端口（如 `127.0.0.1:1080` 或 `:4455`） |
| `-password` | — | **必填**：与服务端一致的认证及加密密钥（最少 8 字符） |
| `-socks5` | `false` | 布尔开关，开启本地 SOCKS5 代理服务（自动启用 TCP） |
| `-t` | `""` | 转发的目的目标地址（非 socks5 模式必填，如 `8.8.8.8:53` 或 `10.0.0.5:22`） |
| `-udp` | `false` | 转发模式：开启 UDP 转发（默认是 TCP 转发） |
| `-tcp` | `1` | (兼容选项) 转发模式：`1` 为 TCP 模式，`0` 为 UDP 模式 |
| `-s5user` | `""` | 本地 SOCKS5 用户名（可选） |
| `-s5pass` | `""` | 本地 SOCKS5 密码（可选） |
| `-encrypt` | `"chacha20"` | 加密算法重载：`chacha20`（默认）、`aes256`、`aes128` |
| `-timeout` | `60` | 本地连接空闲超时释放时间（秒） |
| `-congestion`| `bb` | 拥塞控制算法，默认 `bb`，传空字符串 `""` 禁用 |
| `-tcp_bs` | `1048576` | TCP 滑动窗口接收/发送缓冲大小（字节），默认 1MB |
| `-tcp_mw` | `20000` | TCP 最大滑动窗口大小 |
| `-tcp_rst` | `400` | TCP 丢包重传超时时间（毫秒） |
| `-tcp_gz` | `0` | 超过指定字节数开启 Gzip 压缩，`0` 禁用 |
| `-tcp_stat` | `0` | 设为 `1` 定期打印 TCP 统计性能数据 |

---

## 3. 典型实战拓扑与场景 (Typical Scenarios)

### 场景 1：SOCKS5 全局代理（科学上网/浏览器穿透）
**痛点**：客户端所处局域网封锁所有非标准端口的 TCP/UDP 连接，但允许 Ping。

1. **服务端启动**：
   ```bash
   sudo ./pingtunnel -type server -password "MyPassword999"
   ```
2. **客户端启动**：
   ```bash
   ./pingtunnel -type client -l 127.0.0.1:1080 -s <SERVER_IP> -socks5 -password "MyPassword999"
   ```
3. **终端或应用使用**：
   ```bash
   curl -x socks5h://127.0.0.1:1080 https://www.google.com
   ```

---

### 场景 2：远程内网特定 TCP 端口穿透（SSH / 远程桌面 / Web）
**痛点**：目标服务器内网中有台内网主机 `192.168.1.100` 开设了 SSH（端口 22），需要从外部安全直连。

1. **服务端启动**：
   ```bash
   sudo ./pingtunnel -type server -password "MyPassword999"
   ```
2. **客户端将本地 2222 端口绑定映射至远端内网 192.168.1.100:22（默认 TCP 转发）**：
   ```bash
   ./pingtunnel -type client -l :2222 -s <SERVER_IP> -t 192.168.1.100:22 -password "MyPassword999"
   ```
3. **连接验证**：
   ```bash
   ssh -p 2222 user@127.0.0.1
   ```

---

### 场景 3：特定 UDP 端口转发（DNS 53 / 游戏防丢包）
**痛点**：本地出网 DNS 请求被劫持污染，或游戏 UDP 端口被 ISP 深度检测限速。

1. **客户端将本地 5353 的 UDP 流量穿透至远端由服务器转发给 8.8.8.8:53**：
   ```bash
   ./pingtunnel -type client -l :5353 -s <SERVER_IP> -t 8.8.8.8:53 -udp -password "MyPassword999"
   ```
2. **测试 DNS 解析**：
   ```bash
   dig @127.0.0.1 -p 5353 google.com
   ```

---

### 场景 4：服务端上游中继代理 (Forward Proxy)
**痛点**：Pingtunnel 服务端本身无法直接访问某些目标站点，需要借助企业外部 HTTP 代理或另一台 SOCKS5 服务器转发。

1. **服务端配置 `-forward`**：
   ```bash
   sudo ./pingtunnel -type server -password "MyPassword999" -forward "socks5://127.0.0.1:2080"
   # 或 HTTP 代理:
   # sudo ./pingtunnel -type server -password "MyPassword999" -forward "http://proxy.corp.internal:8080"
   ```
2. **客户端正常使用**：
   客户端发出的所有流量经由 ICMP 解包后，服务端自动送入 `127.0.0.1:2080` 代理转发。

---

### 场景 5：全流量 AEAD 强加密机制
- 自 v3 起，所有流经 ICMP Payload 的业务报文强制进行全程端到端加密与防篡改校验。
- 默认采用现代高性能 AEAD 算法 **ChaCha20-Poly1305**（对移动设备与无硬件 AES 加速环境极友好）。
- 若平台具备硬件 AES 指令集（AES-NI），可重载为 AES：
  ```bash
  # 服务端与客户端保持一致
  ./pingtunnel -type server -password "MySecret" -encrypt aes256
  ./pingtunnel -type client -l :1080 -s <IP> -socks5 -password "MySecret" -encrypt aes256
  ```

---

## 4. 高级性能调优与排错 (Tuning & Troubleshooting)

### 拥塞控制与流控 (BBR-like Congestion Control)
在高吞吐下载（如几十兆/秒的流量）或长距离国际链路中，弱网丢包极易诱发 TCP 缓冲区堆积（Bufferbloat）导致隧道断连。
- Pingtunnel 默认开启 `-congestion bb` 自适应拥塞控制算法，根据往返延迟 RTT 动态调整发送节奏。
- 若网络延迟极低且极度稳定（如内网环境），可传入 `-congestion ""` 关闭流控以榨干单核吞吐。

### 系统底层 ICMP 权限与内核回显响应优化
1. **Linux 避免内核冲突**：
   Linux 系统内核默认会在收到 ICMP Echo Request 时自动应答 Echo Reply，可能会干扰部分复杂环境下的连接流控。建议在服务端临时或永久关闭系统默认回显：
   ```bash
   # 临时关闭
   echo 1 > /proc/sys/net/ipv4/icmp_echo_ignore_all

   # 永久关闭（写入 sysctl.conf）
   echo "net.ipv4.icmp_echo_ignore_all = 1" >> /etc/sysctl.conf && sysctl -p
   ```
2. **免 root 运行（Linux Capabilities）**：
   如果不想使用 `sudo` 运行服务端或客户端，可赋予二进制文件 `CAP_NET_RAW` 权限：
   ```bash
   sudo setcap cap_net_raw+ep ./pingtunnel
   ./pingtunnel -type server -password "SECRET"
   ```

---

## 5. Docker 与后台运行部署

### Docker Compose 一键编排示例

服务端 `docker-compose.yml`：
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

### Systemd 服务守护进程配置 (`/etc/systemd/system/pingtunnel.service`)
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
启停命令：
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now pingtunnel
sudo systemctl status pingtunnel
```
