# Pingtunnel

[<img src="https://img.shields.io/github/license/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/languages/top/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel)
[![Go Report Card](https://goreportcard.com/badge/github.com/esrrhs/pingtunnel)](https://goreportcard.com/report/github.com/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/v/release/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel/releases)
[<img src="https://img.shields.io/github/downloads/esrrhs/pingtunnel/total">](https://github.com/esrrhs/pingtunnel/releases)
[<img src="https://img.shields.io/docker/pulls/esrrhs/pingtunnel">](https://hub.docker.com/repository/docker/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/workflow/status/esrrhs/pingtunnel/Go">](https://github.com/esrrhs/pingtunnel/actions)

pingtunnel是把tcp/udp/sock5流量伪装成icmp流量进行转发的工具。用于突破网络封锁，或是绕过WIFI网络的登陆验证，或是在某些网络加快网络传输速度。

[Readme EN](./README_EN.md)

**注意：本工具只是用作学习研究，请勿用于非法用途！**

**有问题可以加QQ群交流：1023345068**

![image](network.jpg)

# Why use this
* 因为网络审查，ip会直接被ban，但是却可以ping通，这时候就可以用这个工具继续连接服务器。
* 在咖啡厅或是机场，可以连接free wifi，但是需要登录跳转验证，这时候就可以用这个工具绕过登录上网，因为wifi虽然不可以上网，但是却可以ping通你的服务器。
* 在某些网络，tcp的传输很慢，但是如果用icmp协议，可能因为运营商的设置或是网络拓扑，速度会变快，实际测试在中国大陆连aws的服务器会有加速效果。

# Sample
如把本机的:4455的UDP流量转发到www.yourserver.com:4455：
* 在www.yourserver.com的服务器上用root权限运行。
```
sudo ./pingtunnel -type server
```
* 在你本地电脑上用管理员权限运行。
```
pingtunnel.exe -type client -l :4455 -s www.yourserver.com -t www.yourserver.com:4455
```
* 如果看到客户端不停的ping、pong日志输出，说明工作正常。
```
ping www.xx.com 2018-12-23 13:05:50.5724495 +0800 CST m=+3.023909301 8 0 1997 2
pong from xx.xx.xx.xx 210.8078ms
```
* 如果想转发tcp流量，只需要在客户端加上-tcp的参数。
```
pingtunnel.exe -type client -l :4455 -s www.yourserver.com -t www.yourserver.com:4455 -tcp 1
```
* 如果想转发sock5流量，只需要在客户端加上-sock5的参数。
```
pingtunnel.exe -type client -l :4455 -s www.yourserver.com -sock5 1
```
* 大功告成，然后你就可以开始和本机的:4455端口通信，数据都被自动转发到远端，如同连接到www.yourserver.com:4455一样。 

# Test
测试pingtunnel的加速效果，服务器位于aws Korea，客户端位于中国大陆。

下载centos镜像 [centos jp mirror](http://ftp.riken.jp/Linux/centos/8/isos/x86_64/CentOS-8-x86_64-1905-dvd1.iso) 
直接wget、通过shadowsocks wget、通过pingtunnel wget的结果如下。

|              | wget     | shaowsocks | pingtunnel |
|--------------|----------|------------|------------|
| 阿里云 | 26.6KB/s | 31.8KB/s   | 897KB/s    |
| 移动宽带     | 23.1KB/s | 28.4KB/s   | 408KB/s    |

可以看到加速效果基本上**20倍**。

同样的，克隆github仓库 [go-engine](https://github.com/esrrhs/go-engine.git) ，加速效果也很明显。

![image](test.png)

# Download
cmd: https://github.com/esrrhs/pingtunnel/releases

QT GUI: https://github.com/esrrhs/pingtunnel-qt

# Docker
server:
```
docker run --name pingtunnel-server -d --privileged --network host --restart=always esrrhs/pingtunnel ./pingtunnel -type server -key 123456
```
client:
```
docker run --name pingtunnel-client -d --restart=always -p 1080:1080 esrrhs/pingtunnel ./pingtunnel -type client -l :1080 -s www.yourserver.com -sock5 1 -key 123456
```

# Stargazers over time

[![Stargazers over time](https://starchart.cc/esrrhs/pingtunnel.svg)](https://starchart.cc/esrrhs/pingtunnel)
      
# Usage
    通过伪造ping，把tcp/udp/sock5流量通过远程服务器转发到目的服务器上。用于突破某些运营商封锁TCP/UDP流量。
    
    Usage:

    // server
    pingtunnel -type server

    // client, Forward udp
    pingtunnel -type client -l LOCAL_IP:4455 -s SERVER_IP -t SERVER_IP:4455

    // client, Forward tcp
    pingtunnel -type client -l LOCAL_IP:4455 -s SERVER_IP -t SERVER_IP:4455 -tcp 1

    // client, Forward sock5, implicitly open tcp, so no target server is needed
    pingtunnel -type client -l LOCAL_IP:4455 -s SERVER_IP -sock5 1

    -type     服务器或者客户端
              
    -l        本地的地址，发到这个端口的流量将转发到服务器
              
    -s        服务器的地址，流量将通过隧道转发到这个服务器
              
    -t        远端服务器转发的目的地址，流量将转发到这个地址
              
    -timeout  本地记录连接超时的时间，单位是秒，默认60s
              
    -key      设置的密码，默认0
              
    -tcp      设置是否转发tcp，默认0
              
    -tcp_bs   tcp的发送接收缓冲区大小，默认1MB
              
    -tcp_mw   tcp的最大窗口，默认10000
              
    -tcp_rst  tcp的超时发送时间，默认400ms
              
    -tcp_gz   当数据包超过这个大小，tcp将压缩数据，0表示不压缩，默认0
              
    -tcp_stat 打印tcp的监控，默认0
              
    -nolog    不写日志文件，只打印标准输出，默认0
              
    -noprint  不打印屏幕输出，默认0
              
    -loglevel 日志文件等级，默认info
              
    -sock5    开启sock5转发，默认0
              
    -maxconn  最大连接数，默认1000
              
    -maxprt   server最大处理线程数，默认100
              
    -maxprb   server最大处理线程buffer数，默认1000
              
    -profile  在指定端口开启性能检测，默认0不开启
              
    -conntt   server发起连接到目标地址的超时时间，默认1000ms
              
    -s5filter sock5模式设置转发过滤，默认全转发，设置CN代表CN地区的直连不转发
              
    -s5ftfile sock5模式转发过滤的数据文件，默认读取当前目录的GeoLite2-Country.mmdb
              
