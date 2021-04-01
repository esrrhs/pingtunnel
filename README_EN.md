# Pingtunnel

[<img src="https://img.shields.io/github/license/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/languages/top/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel)
[![Go Report Card](https://goreportcard.com/badge/github.com/esrrhs/pingtunnel)](https://goreportcard.com/report/github.com/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/v/release/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel/releases)
[<img src="https://img.shields.io/github/downloads/esrrhs/pingtunnel/total">](https://github.com/esrrhs/pingtunnel/releases)
[<img src="https://img.shields.io/docker/pulls/esrrhs/pingtunnel">](https://hub.docker.com/repository/docker/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/workflow/status/esrrhs/pingtunnel/Go">](https://github.com/esrrhs/pingtunnel/actions)
[![Codacy Badge](https://api.codacy.com/project/badge/Grade/a200bca59d1b4ca7a9c2cdb564508b47)](https://www.codacy.com/manual/esrrhs/pingtunnel?utm_source=github.com&utm_medium=referral&utm_content=esrrhs/pingtunnel&utm_campaign=Badge_Grade)

Pingtunnel is a tool that advertises tcp/udp/sock5 traffic as icmp traffic for forwarding.

## Note: This tool is only to be used for study and research, do not use it for illegal purposes

![image](network.jpg)

## Usage

### Install server

-   First prepare a server with a public IP, such as EC2 on AWS, assuming the domain name or public IP is www.yourserver.com
-   Download the corresponding installation package from [releases](https://github.com/esrrhs/pingtunnel/releases), such as pingtunnel_linux64.zip, then decompress and execute with **root** privileges

```
sudo wget (link of latest release)
sudo unzip pingtunnel_linux64.zip
sudo ./pingtunnel -type server
```

-   (Optional) Disable system default ping

```
echo 1 > /proc/sys/net/ipv4/icmp_echo_ignore_all
```

### Install GUI client (recommended by novices)

-   Download the gui version of qt from [pingtunnel-qt](https://github.com/esrrhs/pingtunnel-qt)
-   Double-click the exe to run, modify the server (such as www.yourserver.com), listen port (such as 1080), tick sock5, other settings can be default, and then click _GO_
-   Everything is normal, there will be a ping value on the interface, and then you can click X to hide it in the status bar
-   Set the browser's sock5 proxy to 127.0.0.1:1080, If you do not connect to the Internet, a socks version not supported error log appears, indicating that the browser's proxy is not a socks5 proxy.If it prompts a non-secure connection, it means there is a problem with dns. Check "Use socks5 proxy DNS query" on the browser.

![image](qtrun.jpg)

### Install the client (recommended for high play)

-   Download the corresponding installation package from [releases](https://github.com/esrrhs/pingtunnel/releases), such as pingtunnel_windows64.zip, and decompress it
-   Then run with **administrator** privileges. The commands corresponding to different forwarding functions are as follows.
-   If you see a log of ping pong, the connection is normal

#### Forward sock5

```
pingtunnel.exe -type client -l: 4455 -s www.yourserver.com -sock5 1
```

#### Forward tcp

```
pingtunnel.exe -type client -l: 4455 -s www.yourserver.com -t www.yourserver.com:4455 -tcp 1
```

#### Forward udp

```
pingtunnel.exe -type client -l: 4455 -s www.yourserver.com -t www.yourserver.com:4455
```

### Use Docker (recommended for high play)
It can also be started directly with docker, which is more convenient. Same parameters as above
-   server:
```
docker run --name pingtunnel-server -d --privileged --network host --restart = always esrrhs / pingtunnel ./pingtunnel -type server -key 123456
```
-   client:
```
docker run --name pingtunnel-client -d --restart = always -p 1080: 1080 esrrhs / pingtunnel ./pingtunnel -type client -l: 1080 -s www.yourserver.com -sock5 1 -key 123456
```

## Test

download the centos image [centos mirror](http://mirrors.ocf.berkeley.edu/centos/8.2.2004/isos/x86_64/CentOS-8.2.2004-x86_64-dvd1.iso)

|              | wget     | ss       | kcp     | pingtunnel |
| ------------ | -------- | -------- | ------- | ---------- |
| AlibabaCloud | 26.6KB/s | 31.8KB/s | 606KB/s | 5.64MB/s   |
