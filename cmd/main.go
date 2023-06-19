package main

import (
	"flag"
	"fmt"
	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/geoip"
	"github.com/esrrhs/gohome/loggo"
	"github.com/esrrhs/pingtunnel"
	"net"
	"net/http"
	_ "net/http/pprof"
	"strconv"
	"time"
)

var usage = `
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

服务器参数server param:

    -key      设置的纯数字密码，默认0, 参数为int类型，范围从0-2147483647，不可夹杂字母特殊符号
              Set password, default 0

    -nolog    不写日志文件，只打印标准输出，默认0
              Do not write log files, only print standard output, default 0 is off

    -noprint  不打印屏幕输出，默认0
              Do not print standard output, default 0 is off

    -loglevel 日志文件等级，默认info
              log level, default is info

    -maxconn  最大连接数，默认0，不受限制
              the max num of connections, default 0 is no limit

    -maxprt   server最大处理线程数，默认100
              max process thread in server, default 100

    -maxprb   server最大处理线程buffer数，默认1000
              max process thread's buffer in server, default 1000

    -conntt   server发起连接到目标地址的超时时间，默认1000ms
              The timeout period for the server to initiate a connection to the destination address. The default is 1000ms.

客户端参数client param:

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

    -tcp_mw   tcp的最大窗口，默认20000
              The maximum window of tcp, the default is 20000

    -tcp_rst  tcp的超时发送时间，默认400ms
              Tcp timeout resend time, default 400ms

    -tcp_gz   当数据包超过这个大小，tcp将压缩数据，0表示不压缩，默认0
              Tcp will compress data when the packet exceeds this size, 0 means no compression, default 0

    -tcp_stat 打印tcp的监控，默认0
              Print tcp connection statistic, default 0 is off

    -nolog    不写日志文件，只打印标准输出，默认0
              Do not write log files, only print standard output, default 0 is off

    -noprint  不打印屏幕输出，默认0
              Do not print standard output, default 0 is off

    -loglevel 日志文件等级，默认info
              log level, default is info

    -sock5    开启sock5转发，默认0
              Turn on sock5 forwarding, default 0 is off

    -profile  在指定端口开启性能检测，默认0不开启
              Enable performance detection on the specified port. The default 0 is not enabled.

    -s5filter sock5模式设置转发过滤，默认全转发，设置CN代表CN地区的直连不转发
              Set the forwarding filter in the sock5 mode. The default is full forwarding. For example, setting the CN indicates that the Chinese address is not forwarded.

    -s5ftfile sock5模式转发过滤的数据文件，默认读取当前目录的GeoLite2-Country.mmdb
              The data file in sock5 filter mode, the default reading of the current directory GeoLite2-Country.mmdb
`

func main() {

	defer common.CrashLog()

	t := flag.String("type", "", "client or server")
	listen := flag.String("l", "", "listen addr")
	target := flag.String("t", "", "target addr")
	server := flag.String("s", "", "server addr")
	timeout := flag.Int("timeout", 60, "conn timeout")
	key := flag.Int("key", 0, "key")
	tcpmode := flag.Int("tcp", 0, "tcp mode")
	tcpmode_buffersize := flag.Int("tcp_bs", 1*1024*1024, "tcp mode buffer size")
	tcpmode_maxwin := flag.Int("tcp_mw", 20000, "tcp mode max win")
	tcpmode_resend_timems := flag.Int("tcp_rst", 400, "tcp mode resend time ms")
	tcpmode_compress := flag.Int("tcp_gz", 0, "tcp data compress")
	nolog := flag.Int("nolog", 0, "write log file")
	noprint := flag.Int("noprint", 0, "print stdout")
	tcpmode_stat := flag.Int("tcp_stat", 0, "print tcp stat")
	loglevel := flag.String("loglevel", "info", "log level")
	open_sock5 := flag.Int("sock5", 0, "sock5 mode")
	maxconn := flag.Int("maxconn", 0, "max num of connections")
	max_process_thread := flag.Int("maxprt", 100, "max process thread in server")
	max_process_buffer := flag.Int("maxprb", 1000, "max process thread's buffer in server")
	profile := flag.Int("profile", 0, "open profile")
	conntt := flag.Int("conntt", 1000, "the connect call's timeout")
	s5filter := flag.String("s5filter", "", "sock5 filter")
	s5ftfile := flag.String("s5ftfile", "GeoLite2-Country.mmdb", "sock5 filter file")
	flag.Usage = func() {
		fmt.Printf(usage)
	}

	flag.Parse()

	if *t != "client" && *t != "server" {
		flag.Usage()
		return
	}
	if *t == "client" {
		if len(*listen) == 0 || len(*server) == 0 {
			flag.Usage()
			return
		}
		if *open_sock5 == 0 && len(*target) == 0 {
			flag.Usage()
			return
		}
		if *open_sock5 != 0 {
			*tcpmode = 1
		}
	}
	if *tcpmode_maxwin*10 > pingtunnel.FRAME_MAX_ID {
		fmt.Println("set tcp win to big, max = " + strconv.Itoa(pingtunnel.FRAME_MAX_ID/10))
		return
	}

	level := loggo.LEVEL_INFO
	if loggo.NameToLevel(*loglevel) >= 0 {
		level = loggo.NameToLevel(*loglevel)
	}
	loggo.Ini(loggo.Config{
		Level:     level,
		Prefix:    "pingtunnel",
		MaxDay:    3,
		NoLogFile: *nolog > 0,
		NoPrint:   *noprint > 0,
	})
	loggo.Info("start...")
	loggo.Info("key %d", *key)

	if *t == "server" {
		s, err := pingtunnel.NewServer(*key, *maxconn, *max_process_thread, *max_process_buffer, *conntt)
		if err != nil {
			loggo.Error("ERROR: %s", err.Error())
			return
		}
		loggo.Info("Server start")
		err = s.Run()
		if err != nil {
			loggo.Error("Run ERROR: %s", err.Error())
			return
		}
	} else if *t == "client" {

		loggo.Info("type %s", *t)
		loggo.Info("listen %s", *listen)
		loggo.Info("server %s", *server)
		loggo.Info("target %s", *target)

		if *tcpmode == 0 {
			*tcpmode_buffersize = 0
			*tcpmode_maxwin = 0
			*tcpmode_resend_timems = 0
			*tcpmode_compress = 0
			*tcpmode_stat = 0
		}

		if len(*s5filter) > 0 {
			err := geoip.Load(*s5ftfile)
			if err != nil {
				loggo.Error("Load Sock5 ip file ERROR: %s", err.Error())
				return
			}
		}
		filter := func(addr string) bool {
			if len(*s5filter) <= 0 {
				return true
			}

			taddr, err := net.ResolveTCPAddr("tcp", addr)
			if err != nil {
				return false
			}

			ret, err := geoip.GetCountryIsoCode(taddr.IP.String())
			if err != nil {
				return false
			}
			if len(ret) <= 0 {
				return false
			}
			return ret != *s5filter
		}

		c, err := pingtunnel.NewClient(*listen, *server, *target, *timeout, *key,
			*tcpmode, *tcpmode_buffersize, *tcpmode_maxwin, *tcpmode_resend_timems, *tcpmode_compress,
			*tcpmode_stat, *open_sock5, *maxconn, &filter)
		if err != nil {
			loggo.Error("ERROR: %s", err.Error())
			return
		}
		loggo.Info("Client Listen %s (%s) Server %s (%s) TargetPort %s:", c.Addr(), c.IPAddr(),
			c.ServerAddr(), c.ServerIPAddr(), c.TargetAddr())
		err = c.Run()
		if err != nil {
			loggo.Error("Run ERROR: %s", err.Error())
			return
		}
	} else {
		return
	}

	if *profile > 0 {
		go http.ListenAndServe("0.0.0.0:"+strconv.Itoa(*profile), nil)
	}

	for {
		time.Sleep(time.Hour)
	}
}
