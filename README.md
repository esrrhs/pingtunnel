# Pingtunnel

[<img src="https://img.shields.io/github/license/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/languages/top/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/v/release/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel/releases)
[<img src="https://img.shields.io/github/downloads/esrrhs/pingtunnel/total">](https://github.com/esrrhs/pingtunnel/releases)
[<img src="https://img.shields.io/docker/pulls/esrrhs/pingtunnel">](https://hub.docker.com/repository/docker/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/actions/workflow/status/esrrhs/pingtunnel/go.yml?branch=master">](https://github.com/esrrhs/pingtunnel/actions)

Pingtunnel 是一个通过伪造 ICMP Echo (Ping) 报文将 TCP/UDP/SOCKS5 流量转发到目标服务器的网络穿透工具，用于在网络运营商封锁或限制常规 TCP/UDP 流量时恢复网络连接。

Pingtunnel is a network tool that tunnels TCP/UDP/SOCKS5 traffic over ICMP Echo (Ping) packets to bypass strict network firewalls or throttled TCP/UDP traffic.

> ⚠️ **免责声明 / Note**: 本工具仅供网络技术学习与科研交流使用，请勿用于非法用途。This tool is strictly intended for study and research purposes.

![image](network.jpg)

---

## 🌟 核心特性 (Features)

- **v3 统一安全架构**：告别旧版分散的数字 key 与加密开关，全网统一单一共享密码 (`-password`)，驱动双端认证与 AEAD 全程强加密（默认 `chacha20-poly1305`，可选 `aes256`/`aes128`）。
- **多种运行模式**：
  - **SOCKS5 代理**：一键开启本地 SOCKS5 代理服务，支持账号密码认证；
  - **TCP 端口转发**：穿透内网访问远程特定 TCP 端口（如 SSH、Web、RDP）；
  - **UDP 端口转发**：转发游戏 UDP、DNS 53 等数据；
  - **服务端二级转发 (`-forward`)**：服务端接收流量后可通过指定的上游 SOCKS5/HTTP 代理中继访问外网。
- **配置方式灵活（推荐配置文件模式）**：
  - 支持直接传配置文件运行：`pingtunnel config.json` 或 `pingtunnel -c config.json`；
  - 当前目录下存在 `config.json` 时自动缺省加载；
  - 命令行参数随时覆盖配置文件中的对应项。
- **抗弱网与防断连**：内置带宽自适应拥塞控制算法（`bb`），有效抑制缓冲区膨胀与丢包断流。

---

## 🚀 快速上手 (Quick Start)

### 方式 1：配置文件模式（推荐 / Recommended）

预置示例文件已随仓库提供，只需填写服务器 IP 和强密码即可快速启动：

#### 1. 服务端配置 (`config-server.json`)
```json
{
  "type": "server",
  "password": "YOUR_STRONG_PASSWORD_HERE",
  "encrypt": "chacha20",
  "icmp_listen": "0.0.0.0"
}
```
启动服务端（需管理员权限 / root）：
```bash
sudo ./pingtunnel config-server.json
# 或: sudo ./pingtunnel -c config-server.json
```

#### 2. 客户端配置（以 SOCKS5 代理模式为例 `config-client-socks5.json`）
```json
{
  "type": "client",
  "listen": "127.0.0.1:1080",
  "server": "YOUR_SERVER_IP",
  "socks5": true,
  "password": "YOUR_STRONG_PASSWORD_HERE",
  "encrypt": "chacha20"
}
```
启动客户端：
```bash
./pingtunnel config-client-socks5.json
# 或: ./pingtunnel -c config-client-socks5.json
```
在浏览器或系统网络设置中配置 SOCKS5 代理为 `127.0.0.1:1080` 即可开始使用。

---

### 方式 2：命令行模式 (CLI)

#### 1. 启动服务端 (Server)
在拥有公网 IP 的服务器上执行：
```bash
sudo ./pingtunnel -type server -password "YourSecretPassword"
```
*(可选优化)* 关闭 Linux 系统默认内核 ICMP 回显响应以防干扰：
```bash
echo 1 > /proc/sys/net/ipv4/icmp_echo_ignore_all
```

#### 2. 启动客户端 (Client)
- **SOCKS5 代理模式**：
  ```bash
  ./pingtunnel -type client -l 127.0.0.1:1080 -s YOUR_SERVER_IP -socks5 -password "YourSecretPassword"
  ```
- **TCP 端口转发（默认模式，例如映射远程内网 192.168.1.100:22 到本地 2222）**：
  ```bash
  ./pingtunnel -type client -l :2222 -s YOUR_SERVER_IP -t 192.168.1.100:22 -password "YourSecretPassword"
  ```
- **UDP 转发（例如转发本地 5353 到公共 DNS 8.8.8.8:53）**：
  ```bash
  ./pingtunnel -type client -l :5353 -s YOUR_SERVER_IP -t 8.8.8.8:53 -udp -password "YourSecretPassword"
  ```

---

## 🐳 Docker 运行

### 服务端 Docker 启动
```bash
docker run --name pingtunnel-server -d \
  --restart=always \
  --privileged \
  --network host \
  esrrhs/pingtunnel ./pingtunnel -type server -password "YourSecretPassword"
```

### 挂载配置文件启动
```bash
docker run --name pingtunnel-server -d \
  --restart=always \
  --privileged \
  --network host \
  -v /etc/pingtunnel/server.json:/app/server.json:ro \
  esrrhs/pingtunnel ./pingtunnel /app/server.json
```

---

## 📱 移动端客户端 (Android)

Pingtunnel 社区开源开发了专属 Android 客户端应用：
- 仓库链接：[**pingtunnel-client**](https://github.com/itismoej/pingtunnel-client)

---

## 📖 详细使用与调优指南

关于更多高级参数、多场景拓扑配置（服务端二级代理、端口转发、SOCKS5 认证、流控与拥塞算法调优），请参阅详细文档：
👉 **[USAGE.md - Pingtunnel 完整使用与参数调优指南](USAGE.md)**
