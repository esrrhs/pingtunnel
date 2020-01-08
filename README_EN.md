# Pingtunnel

[<img src="https://img.shields.io/github/license/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/languages/top/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel)
[![Go Report Card](https://goreportcard.com/badge/github.com/esrrhs/pingtunnel)](https://goreportcard.com/report/github.com/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/v/release/esrrhs/pingtunnel">](https://github.com/esrrhs/pingtunnel/releases)
[<img src="https://img.shields.io/github/downloads/esrrhs/pingtunnel/total">](https://github.com/esrrhs/pingtunnel/releases)
[<img src="https://img.shields.io/docker/pulls/esrrhs/pingtunnel">](https://hub.docker.com/repository/docker/esrrhs/pingtunnel)
[<img src="https://img.shields.io/github/workflow/status/esrrhs/pingtunnel/Go">](https://github.com/esrrhs/pingtunnel/actions)

Pingtunnel is a tool that advertises tcp/udp/sock5 traffic as icmp traffic for forwarding. Used to break through the network blockade, or to bypass the WIFI network login verification, or speed up network transmission speed on some networks. 

![image](network.jpg)

# Why use this
* TCP and UDP traffic of some servers are banned and can be bypassed by pingtunnel.
* In some occasions, such as schools, cafes, and airports, login jump authentication is required, which can be bypassed by pingtunnel.
* In some networks, TCP transmission is very slow. You can speed up the network through pingtunnel.
# Use
### Install server
* First prepare a server with a public IP, such as EC2 on AWS, assuming the domain name or public IP is www.yourserver.com
* Download the corresponding installation package from [releases](https://github.com/esrrhs/pingtunnel/releases), such as pingtunnel_linux64.zip, then decompress and execute
```
sudo wget https://github.com/esrrhs/pingtunnel/releases/download/1.5/pingtunnel_linux64.zip
sudo unzip pingtunnel_linux64.zip
sudo ./pingtunnel -type server
```
### Install GUI client (recommended by novices)
* Download the gui version of qt from [pingtunnel-qt](https://github.com/esrrhs/pingtunnel-qt)
* Double-click the exe to run, modify the server (such as www.yourserver.com), listen port (such as 1080), tick sock5, other settings can be default, and then click *GO*
* Everything is normal, there will be a ping value on the interface, and then you can click X to hide it in the status bar
* Set the browser's sock5 proxy to 127.0.0.1:1080

![image](qtrun.jpg)

### Install the client (recommended for high play)
* Download the corresponding installation package from [releases](https://github.com/esrrhs/pingtunnel/releases), such as pingtunnel_windows64.zip, and decompress it
* Then run with administrator privileges. The commands corresponding to different forwarding functions are as follows.
* If you see a log of ping pong, the connection is normal
##### Forward sock5
```
pingtunnel.exe -type client -l: 4455 -s www.yourserver.com -sock5 1
```
##### Forward tcp
```
pingtunnel.exe -type client -l: 4455 -s www.yourserver.com -t www.yourserver.com:4455 -tcp 1
```
##### Forward udp
```
pingtunnel.exe -type client -l: 4455 -s www.yourserver.com -t www.yourserver.com:4455
```

### Use Docker
server:
```
docker run --name pingtunnel-server -d --privileged --network host --restart = always esrrhs / pingtunnel ./pingtunnel -type server -key 123456
```
client:
```
docker run --name pingtunnel-client -d --restart = always -p 1080: 1080 esrrhs / pingtunnel ./pingtunnel -type client -l: 1080 -s www.yourserver.com -sock5 1 -key 123456
```

# Test
Test the acceleration effect of pingtunnel. The server is located in aws Korea and the client is located in mainland China.

download the centos image [centos mirror](http://mirror.calgah.com/centos/8/isos/x86_64/CentOS-8-x86_64-1905-dvd1.iso) , the results of direct wget, shadowsocks wget, and pingtunnel wget are as follows.

|              | wget     | shaowsocks | pingtunnel |
|--------------|----------|------------|------------|
| AlibabaCloud | 26.6KB/s | 31.8KB/s   | 897KB/s    |
| Home Broadband     | 23.1KB/s | 28.4KB/s   | 408KB/s    |

the acceleration effect is basically **20 times**.

Similarly, clone the github repository [go-engine](https://github.com/esrrhs/go-engine.git) , the acceleration effect is also obvious

![image](test.png)

# Download
cmd: https://github.com/esrrhs/pingtunnel/releases

QT GUI: https://github.com/esrrhs/pingtunnel-qt

# Stargazers over time

[![Stargazers over time](https://starchart.cc/esrrhs/pingtunnel.svg)](https://starchart.cc/esrrhs/pingtunnel)
      
# Usage
    By forging ping, the tcp/udp/sock5 traffic is forwarded to the destination server through the remote server. Used to break certain operators to block TCP/UDP traffic.

    Usage:

    // server
    pingtunnel -type server

    // client, Forward udp
    pingtunnel -type client -l LOCAL_IP:4455 -s SERVER_IP -t SERVER_IP:4455

    // client, Forward tcp
    pingtunnel -type client -l LOCAL_IP:4455 -s SERVER_IP -t SERVER_IP:4455 -tcp 1

    // client, Forward sock5, implicitly open tcp, so no target server is needed
    pingtunnel -type client -l LOCAL_IP:4455 -s SERVER_IP -sock5 1

    -type     client or server

    -l        Local address, traffic sent to this port will be forwarded to the server

    -s        The address of the server, the traffic will be forwarded to this server through the tunnel

    -t        Destination address forwarded by the remote server, traffic will be forwarded to this address

    -timeout  The time when the local record connection timed out, in seconds, 60 seconds by default

    -key      Set password, default 0

    -tcp      Set the switch to forward tcp, the default is 0

    -tcp_bs   Tcp send and receive buffer size, default 1MB

    -tcp_mw   The maximum window of tcp, the default is 10000

    -tcp_rst  Tcp timeout resend time, default 400ms

    -tcp_gz   Tcp will compress data when the packet exceeds this size, 0 means no compression, default 0

    -tcp_stat Print tcp connection statistic, default 0 is off

    -nolog    Do not write log files, only print standard output, default 0 is off

    -noprint  Do not print standard output, default 0 is off

    -loglevel log level, default is info

    -sock5    Turn on sock5 forwarding, default 0 is off

    -maxconn  the max num of connections, default 1000

    -maxprt   max process thread in server, default 100

    -maxprb   max process thread's buffer in server, default 1000

    -profile  Enable performance detection on the specified port. The default 0 is not enabled.

    -conntt   The timeout period for the server to initiate a connection to the destination address. The default is 1000ms.

    -s5filter Set the forwarding filter in the sock5 mode. The default is full forwarding. For example, setting the CN indicates that the Chinese address is not forwarded.

    -s5ftfile The data file in sock5 filter mode, the default reading of the current directory GeoLite2-Country.mmdb
