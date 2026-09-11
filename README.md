# Pingtunnel

[<img src="https://img.shields.io/github/license/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/languages/top/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel)
[![Go Report Card](https://goreportcard.com/badge/github.com/esrrhs/pingtunnel)](https://goreportcard.com/report/github.com/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/v/release/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel/releases)
[<img src="https://img.shields.io/github/downloads/esrrhs/pingtunnel/total">](https://github.com/esrrhs/pingtunnel/releases)
[<img src="https://img.shields.io/docker/pulls/esrrhs/pingtunnel">](https://hub.docker.com/repository/docker/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/actions/workflow/status/esrrhs/pingtunnel/go.yml?branch=master">](https://github.com/esrrhs/pingtunnel/actions)

Pingtunnel is a tool that sends TCP/UDP traffic over ICMP.

## Note: This tool is only to be used for study and research, do not use it for illegal purposes

![image](network.jpg)

## Usage & Detailed Documentation

> 📖 **完整使用文档请参阅 [USAGE.md](USAGE.md)**：包含全部命令行参数列表、配置文件模式（`-c config.json`）、各场景代理设置（SOCKS5全局、GeoIP分流、TCP端口穿透、UDP转发、二级上游代理、端到端AES/ChaCha20加密）等。

### Quick Start

#### 1. Server

- Prepare a server with a public IP (e.g. `www.yourserver.com`)
- Download the release package from [releases](https://github.com/esrrhs/pingtunnel/releases) (e.g. `pingtunnel_linux64.zip`), unzip and execute with **root** privileges:

```bash
sudo ./pingtunnel -type server -key 123456
```

- (Optional) Disable system default ICMP echo response:

```bash
echo 1 > /proc/sys/net/ipv4/icmp_echo_ignore_all
```

#### 2. Client

Run with **administrator** privileges:

- **SOCKS5 Proxy Mode**:
```bash
pingtunnel -type client -l :4455 -s www.yourserver.com -sock5 1 -key 123456
```

- **Forward TCP (e.g. SSH/Web)**:
```bash
pingtunnel -type client -l :4455 -s www.yourserver.com -t 192.168.1.100:22 -tcp 1 -key 123456
```

- **Forward UDP (e.g. DNS)**:
```bash
pingtunnel -type client -l :4455 -s www.yourserver.com -t 8.8.8.8:53 -key 123456
```

#### 3. Config File Mode (`-c`)

Both client and server support JSON configuration files:

```bash
# Start server with config
sudo ./pingtunnel -c server.json

# Start client with config
./pingtunnel -c client.json
```

See [USAGE.md](USAGE.md#2-配置文件使用-config-file-mode) for JSON configuration templates and rules.

### Use Android Client

A dedicated Android client for pingtunnel is available, developed by the community:

* [**pingtunnel-client**](https://github.com/itismoej/pingtunnel-client)

> Big thanks to [itismoej](https://github.com/itismoej) for developing this Android client!

### Use Docker

You can also start pingtunnel directly with Docker:

- **Server**:
```bash
docker run --name pingtunnel-server -d --privileged --network host --restart=always esrrhs/pingtunnel ./pingtunnel -type server -key 123456
```

- **Client**:
```bash
docker run --name pingtunnel-client -d --restart=always -p 1080:1080 esrrhs/pingtunnel ./pingtunnel -type client -l :1080 -s www.yourserver.com -sock5 1 -key 123456
```

