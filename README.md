# Pingtunnel
pingtunnel是把tcp/udp/sock5流量伪装成icmp流量进行转发的工具。用于突破网络封锁，或是绕过WIFI网络的登陆验证，或是在某些网络加快网络传输速度。

**注意：本工具只是用作学习研究，请勿用于非法用途！**
<br />Pingtunnel is a tool that advertises tcp/udp/sock5 traffic as icmp traffic for forwarding. Used to break through the network blockade, or to bypass the WIFI network login verification, or speed up network transmission speed on some networks. 

![image](network.jpg)

# Why use this
* 因为网络审查，ip会直接被ban，但是却可以ping通，这时候就可以用这个工具继续连接服务器。If the server's ip is blocked, all tcp udp packets are forbidden, but it can be pinged. At this point, you can continue to connect to the server with this tool.
* 在咖啡厅或是机场，可以连接free wifi，但是需要登录跳转验证，这时候就可以用这个工具绕过登录上网，因为wifi虽然不可以上网，但是却可以ping通你的服务器。In the coffee shop or airport, you can connect to free wifi, but you need to log in to verify. At this time, you can use this tool to bypass the login, because wifi can not surf the Internet, but you can ping your server.
* 在某些网络，tcp的传输很慢，但是如果用icmp协议，可能因为运营商的设置或是网络拓扑，速度会变快，实际测试在中国大陆连aws的服务器会有加速效果。In some networks, the transmission of tcp is very slow, but if the icmp protocol is used, the speed may be faster because of the operator's settings or the network topology. After testing, connecting the server of aws from mainland China has an accelerated effect.

# Sample
如把本机的:4455的UDP流量转发到www.yourserver.com:4455：For example, the UDP traffic of the machine: 4545 is forwarded to www.yourserver.com:4455:
* 在www.yourserver.com的服务器上用root权限运行。Run with root privileges on the server at www.yourserver.com
```
sudo ./pingtunnel -type server
```
* 在你本地电脑上用管理员权限运行。Run with administrator privileges on your local computer
```
pingtunnel.exe -type client -l :4455 -s www.yourserver.com -t www.yourserver.com:4455
```
* 如果看到客户端不停的ping、pong日志输出，说明工作正常。If you see the client ping, pong log output, it means normal work
```
ping www.xx.com 2018-12-23 13:05:50.5724495 +0800 CST m=+3.023909301 8 0 1997 2
pong from xx.xx.xx.xx 210.8078ms
```
* 如果想转发tcp流量，只需要在客户端加上-tcp的参数。If you want to forward tcp traffic, you only need to add the -tcp parameter to the client.
```
pingtunnel.exe -type client -l :4455 -s www.yourserver.com -t www.yourserver.com:4455 -tcp 1
```
* 如果想转发sock5流量，只需要在客户端加上-sock5的参数。If you want to forward sock5 traffic, you only need to add the -sock5 parameter to the client.
```
pingtunnel.exe -type client -l :4455 -s www.yourserver.com -sock5 1
```
* 大功告成，然后你就可以开始和本机的:4455端口通信，数据都被自动转发到远端，如同连接到www.yourserver.com:4455一样。 Then you can start communicating with the local: 4455 port, the data is automatically forwarded to the remote, as you connect to www.yourserver.com:4455.

# Download
cmd: https://github.com/esrrhs/pingtunnel/releases

QT GUI: https://github.com/esrrhs/pingtunnel-qt

# Stargazers over time

[![Stargazers over time](https://starchart.cc/esrrhs/pingtunnel.svg)](https://starchart.cc/esrrhs/pingtunnel)
      
# Usage
    通过伪造ping，把tcp/udp/sock5流量通过远程服务器转发到目的服务器上。用于突破某些运营商封锁TCP/UDP流量。
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

    -type     服务器或者客户端
              client or server

    -l        本地的地址，发到这个端口的流量将转发到服务器
              Local address, traffic sent to this port will be forwarded to the server

    -s        服务器的地址，流量将通过隧道转发到这个服务器
              The address of the server, the traffic will be forwarded to this server through the tunnel

    -t        远端服务器转发的目的地址，流量将转发到这个地址
              Destination address forwarded by the remote server, traffic will be forwarded to this address

    -timeout  本地记录连接超时的时间，单位是秒，默认60s
              The time when the local record connection timed out, in seconds, 60 seconds by default

    -key      设置的密码，默认0
              Set password, default 0

    -tcp      设置是否转发tcp，默认0
              Set the switch to forward tcp, the default is 0

    -tcp_bs   tcp的发送接收缓冲区大小，默认1MB
              Tcp send and receive buffer size, default 1MB

    -tcp_mw   tcp的最大窗口，默认10000
              The maximum window of tcp, the default is 10000

    -tcp_rst  tcp的超时发送时间，默认400ms
              Tcp timeout resend time, default 400ms

    -tcp_gz   当数据包超过这个大小，tcp将压缩数据，0表示不压缩，默认0
              Tcp will compress data when the packet exceeds this size, 0 means no compression, default 0

    -tcp_stat 打印tcp的监控，默认0
              Print tcp connection statistic, default 0 is off

    -nolog    不写日志文件，只打印标准输出，默认0
              Do not write log files, only print standard output, default 0 is off

    -loglevel 日志文件等级，默认info
              log level, default is info

    -sock5    开启sock5转发，默认0
              Turn on sock5 forwarding, default 0 is off

    -maxconn  最大连接数，默认1000
              the max num of connections, default 1000

    -maxprt   server最大处理线程数，默认100
              max process thread in server, default 100

    -maxprb   server最大处理线程buffer数，默认1000
              max process thread's buffer in server, default 1000

    -profile  在指定端口开启性能检测，默认0不开启
              Enable performance detection on the specified port. The default 0 is not enabled.

    -conntt   server发起连接到目标地址的超时时间，默认1000ms
              The timeout period for the server to initiate a connection to the destination address. The default is 1000ms.

    -s5filter sock5模式设置转发过滤，默认全转发，设置CN代表CN地区的直连不转发
              Set the forwarding filter in the sock5 mode. The default is full forwarding. For example, setting the CN indicates that the Chinese address is not forwarded.

    -s5ftfile sock5模式转发过滤的数据文件，默认读取当前目录的GeoLite2-Country.mmdb
              The data file in sock5 filter mode, the default reading of the current directory GeoLite2-Country.mmdb
