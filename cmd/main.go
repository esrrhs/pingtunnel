package main

import (
	"flag"
	"fmt"
	"github.com/esrrhs/go-engine/src/loggo"
	"github.com/esrrhs/pingtunnel"
	"strconv"
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

    -tcp      设置是否转发tcp，默认0
              Set the switch to forward tcp, the default is 0

    -tcp_bs   tcp的发送接收缓冲区大小，默认10MB
              Tcp send and receive buffer size, default 10MB

    -tcp_mw   tcp的最大窗口，默认10000
              The maximum window of tcp, the default is 10000

    -tcp_rst  tcp的超时发送时间，默认400ms
              Tcp timeout resend time, default 400ms

	-tcp_gz   tcp数据开启压缩，默认0
              Whether the tcp data is compressed or not, the default is 0.
`

func main() {

	t := flag.String("type", "", "client or server")
	listen := flag.String("l", "", "listen addr")
	target := flag.String("t", "", "target addr")
	server := flag.String("s", "", "server addr")
	timeout := flag.Int("timeout", 60, "conn timeout")
	key := flag.Int("key", 0, "key")
	tcpmode := flag.Int("tcp", 0, "tcp mode")
	tcpmode_buffersize := flag.Int("tcp_bs", 10*1024*1024, "tcp mode buffer size")
	tcpmode_maxwin := flag.Int("tcp_mw", 10000, "tcp mode max win")
	tcpmode_resend_timems := flag.Int("tcp_rst", 400, "tcp mode resend time ms")
	tcpmode_compress := flag.Int("tcp_gz", 0, "tcp data compress")
	flag.Usage = func() {
		fmt.Printf(usage)
	}

	flag.Parse()

	if (*t != "client" && *t != "server") || (*t == "client" && (len(*listen) == 0 || len(*target) == 0 || len(*server) == 0)) {
		flag.Usage()
		return
	}
	if *tcpmode_maxwin*10 > pingtunnel.FRAME_MAX_ID {
		fmt.Println("set tcp win to big, max = " + strconv.Itoa(pingtunnel.FRAME_MAX_ID/10))
		return
	}

	loggo.Ini(loggo.Config{Level: loggo.LEVEL_INFO, Prefix: "pingtunnel", MaxDay: 3})
	loggo.Info("start...")
	loggo.Info("key %d", *key)

	if *t == "server" {
		s, err := pingtunnel.NewServer(*key)
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

		if *tcpmode == 0 {
			*tcpmode_buffersize = 0
			*tcpmode_maxwin = 0
			*tcpmode_resend_timems = 0
		}

		c, err := pingtunnel.NewClient(*listen, *server, *target, *timeout, *key,
			*tcpmode, *tcpmode_buffersize, *tcpmode_maxwin, *tcpmode_resend_timems, *tcpmode_compress)
		if err != nil {
			loggo.Error("ERROR: %s", err.Error())
			return
		}
		loggo.Info("Client Listen %s (%s) Server %s (%s) TargetPort %s:", c.Addr(), c.IPAddr(),
			c.ServerAddr(), c.ServerIPAddr(), c.TargetAddr())
		c.Run()
	}
}
