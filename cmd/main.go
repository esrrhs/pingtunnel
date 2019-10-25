package main

import (
	"flag"
	"fmt"
	"github.com/esrrhs/go-engine/src/loggo"
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

    -key      设置的密码，默认0
              Set password, default 0

    -tcp      设置是否转发tcp，默认false
              Set the switch to forward tcp, the default is false

    -tcp_bs   tcp的发送接收缓冲区大小，默认1MB
              Tcp send and receive buffer size, default 1MB

    -tcp_mw   tcp的最大窗口，默认100
              The maximum window of tcp, the default is 100

    -tcp_rst  tcp的超时发送时间，默认200ms
              Tcp timeout resend time, default 200ms
`

func main() {

	t := flag.String("type", "", "client or server")
	listen := flag.String("l", "", "listen addr")
	target := flag.String("t", "", "target addr")
	server := flag.String("s", "", "server addr")
	timeout := flag.Int("timeout", 60, "conn timeout")
	key := flag.Int("key", 0, "key")
	tcpmode := flag.Int("tcp", 0, "tcp mode")
	tcpmode_buffersize := flag.Int("tcp_bs", 1024*1024, "tcp mode buffer size")
	tcpmode_maxwin := flag.Int("tcp_mw", 100, "tcp mode max win")
	tcpmode_resend_timems := flag.Int("tcp_rst", 200, "tcp mode resend time ms")
	flag.Usage = func() {
		fmt.Printf(usage)
	}

	flag.Parse()

	if (*t != "client" && *t != "server") || (*t == "client" && (len(*listen) == 0 || len(*target) == 0 || len(*server) == 0)) {
		flag.Usage()
		return
	}

	loggo.Info("start...")
	loggo.Info("key %d", *key)

	if *t == "server" {
		s, err := pingtunnel.NewServer(*timeout, *key)
		if err != nil {
			loggo.Error("ERROR: %s", err.Error())
			return
		}
		loggo.Info("Server start")
		s.Run()
	}
	if *t == "client" {

		loggo.Info("type %s", *t)
		loggo.Info("listen %s", *listen)
		loggo.Info("server %s", *server)
		loggo.Info("target %s", *target)

		c, err := pingtunnel.NewClient(*listen, *server, *target, *timeout, *key,
			*tcpmode, *tcpmode_buffersize, *tcpmode_maxwin, *tcpmode_resend_timems)
		if err != nil {
			loggo.Error("ERROR: %s", err.Error())
			return
		}
		loggo.Info("Client Listen %s (%s) Server %s (%s) TargetPort %s:", c.Addr(), c.IPAddr(),
			c.ServerAddr(), c.ServerIPAddr(), c.TargetAddr())
		c.Run()
	}
}
