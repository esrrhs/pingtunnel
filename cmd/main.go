package main

import (
	"flag"
	"fmt"
	"github.com/esrrhs/pingtunnel"
)

var usage = `
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

    -timeout  本地记录连接超时的时间，单位是秒，默认60s
              The time when the local record connection timed out, in seconds, 60 seconds by default

    -sproto   客户端发送ping协议的协议，默认是8
              The protocol that the client sends the ping. The default is 8.

    -rproto   客户端接收ping协议的协议，默认是0
              The protocol that the client receives the ping. The default is 0.

    -catch    主动抓模式，每秒从服务器主动抓多少个reply包，默认0
              Active capture mode, how many reply packets are actively captured from the server per second, default 0

    -key      设置的密码，默认0
              Set password, default 0
`

func main() {

	t := flag.String("type", "", "client or server")
	listen := flag.String("l", "", "listen addr")
	target := flag.String("t", "", "target addr")
	server := flag.String("s", "", "server addr")
	timeout := flag.Int("timeout", 60, "conn timeout")
	sproto := flag.Int("sproto", 8, "send ping proto")
	rproto := flag.Int("rproto", 0, "recv ping proto")
	catch := flag.Int("catch", 0, "catch mode")
	key := flag.Int("key", 0, "key")
	flag.Usage = func() {
		fmt.Printf(usage)
	}

	flag.Parse()

	if (*t != "client" && *t != "server") || (*t == "client" && (len(*listen) == 0 || len(*target) == 0 || len(*server) == 0)) {
		flag.Usage()
		return
	}

	fmt.Println("start...")
	fmt.Printf("key %d\n", *key)

	if *t == "server" {
		s, err := pingtunnel.NewServer(*timeout, *key)
		if err != nil {
			fmt.Printf("ERROR: %s\n", err.Error())
			return
		}
		fmt.Printf("Server start\n")
		s.Run()
	}
	if *t == "client" {

		fmt.Printf("type %s\n", *t)
		fmt.Printf("listen %s\n", *listen)
		fmt.Printf("server %s\n", *server)
		fmt.Printf("target %s\n", *target)

		c, err := pingtunnel.NewClient(*listen, *server, *target, *timeout, *sproto, *rproto, *catch, *key)
		if err != nil {
			fmt.Printf("ERROR: %s\n", err.Error())
			return
		}
		fmt.Printf("Client Listen %s (%s) Server %s (%s) TargetPort %s:\n", c.Addr(), c.IPAddr(),
			c.ServerAddr(), c.ServerIPAddr(), c.TargetAddr())
		c.Run()
	}
}
