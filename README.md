# Pingtunnel
pingtunnel是把udp流量伪装成icmp流量进行转发的工具，类似于kcptun。用于突破网络封锁，或是绕过WIFI网络的登陆验证。可以与kcptun很方便的结合使用。
<br />Pingtunnel is a tool that advertises udp traffic as icmp traffic for forwarding, similar to kcptun. Used to break through the network blockade, or to bypass the WIFI network login verification. Can be combined with kcptun very convenient.
![image](network.png)
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
# Usage


    通过伪造ping，把udp流量通过远程服务器转发到目的服务器上。用于突破某些运营商封锁UDP流量。
    By forging ping, the udp traffic is forwarded to the destination server through the remote server. Used to break certain operators to block UDP traffic.

    Usage:

    pingtunnel -type server

    pingtunnel -type client -l LOCAL_IP:4455 -s SERVER_IP -t SERVER_IP:4455

    -type     服务器或者客户端
              client or server

    -l        本地的地址，发到这个端口的流量将转发到服务器
              Local address, traffic sent to this port will be forwarded to the server

    -s        服务器的地址，流量将通过隧道转发到这个服务器
              The address of the server, the traffic will be forwarded to this server through the tunnel

    -t        远端服务器转发的目的地址，流量将转发到这个地址
              Destination address forwarded by the remote server, traffic will be forwarded to this address

    -timeout  本地记录连接超时的时间，单位是秒
              The time when the local record connection timed out, in seconds

    -sproto   客户端发送ping协议的协议，默认是13
              The protocol that the client sends the ping. The default is 13.

    -rproto   客户端接收ping协议的协议，默认是14
              The protocol that the client receives the ping. The default is 14.

    -catch    主动抓模式，每秒从服务器主动抓多少个reply包，默认0
              Active capture mode, how many reply packets are actively captured from the server per second, default 0