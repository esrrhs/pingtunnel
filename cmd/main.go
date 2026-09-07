package main

import (
	"flag"
	"fmt"
	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/loggo"
	"github.com/esrrhs/gohome/thirdparty"
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

    // client, Forward multiple rules from config file
    pingtunnel -type client -c config.yaml

    -type     服务器或者客户端
              client or server

服务器参数server param:

    -icmp_l   本地地址，侦听此地址上的ICMP流量，默认为0.0.0.0
              Local address, listen for ICMP traffic on this address, defaults to 0.0.0.0

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

    -forward  通过指定的代理转发TCP流量，支持socks5和http代理，如 socks5://localhost:2080 或 http://localhost:8080
              Forward TCP traffic through the specified proxy. Supports socks5 and http proxies, e.g. socks5://localhost:2080 or http://localhost:8080

客户端参数client param:

    -c        配置文件模式，从yaml文件读取多条转发规则，单进程并发处理，不用起多个进程。与-l/-t互斥，如 -c config.yaml
              Config file mode, read multiple client rules from a yaml file and forward them concurrently in one process. Mutually exclusive with -l/-t, e.g. -c config.yaml
              文件格式为yaml字符串列表，每行一条规则，参数与命令行相同。配置里的进程级参数(nolog/noprint/encrypt等)以命令行为准。命令行上设置的参数作为所有规则的默认值。
              The file is a yaml list of strings, one rule per line with the same parameters as the command line. Process level flags in rules (nolog/noprint/encrypt...) are ignored, command line wins. Command line parameters act as defaults for all rules.

    -l        本地的地址，发到这个端口的流量将转发到服务器
              Local address, traffic sent to this port will be forwarded to the server

    -s        服务器的地址，流量将通过隧道转发到这个服务器
              The address of the server, the traffic will be forwarded to this server through the tunnel

    -t        远端服务器转发的目的地址，流量将转发到这个地址
              Destination address forwarded by the remote server, traffic will be forwarded to this address

    -icmp_l   本地地址，侦听此地址上的ICMP流量，默认为0.0.0.0
              Local address, listen for ICMP traffic on this address, defaults to 0.0.0.0

    -timeout  本地记录连接超时的时间，单位是秒，默认60s
              The time when the local record connection timed out, in seconds, 60 seconds by default

    -key      设置的密码，默认0
              Set password, default 0

    -encrypt  加密模式，支持aes128, aes256, chacha20
              Encryption mode: aes128, aes256, chacha20

    -encrypt-key 加密密钥，支持base64编码或密码短语
              Encryption key, supports base64 encoded key or passphrase

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

    -s5user   sock5用户名，默认为空不需要认证
              sock5 username, default is empty and no authentication is required

    -s5pass   sock5密码，默认为空不需要认证
              sock5 password, default is empty and no authentication is required

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
	configFile := flag.String("c", "", "config file with multiple client rules")
	cf := newClientFlagSet(flag.CommandLine, nil)
	flag.Usage = func() {
		fmt.Print(usage)
	}

	flag.Parse()

	if *t != "client" && *t != "server" {
		flag.Usage()
		return
	}
	if *t == "client" {
		if len(*configFile) == 0 {
			if len(*cf.listen) == 0 || len(*cf.server) == 0 {
				flag.Usage()
				return
			}
			if *cf.openSock5 == 0 && len(*cf.target) == 0 {
				flag.Usage()
				return
			}
			if *cf.openSock5 != 0 {
				*cf.tcpmode = 1
			}
		}
	}
	if *cf.tcpmodeMaxwin*10 > pingtunnel.FRAME_MAX_ID {
		fmt.Println("set tcp win to big, max = " + strconv.Itoa(pingtunnel.FRAME_MAX_ID/10))
		return
	}

	// Validate encryption parameters
	encryptionMode, err := pingtunnel.ParseEncryptionMode(*cf.encryption)
	if err != nil {
		fmt.Printf("Invalid encryption mode: %v\n", err)
		return
	}

	if encryptionMode != pingtunnel.NoEncryption && *cf.encryptionKey == "" {
		fmt.Println("Encryption key is required when encryption mode is specified")
		return
	}

	// Create crypto configuration
	var cryptoConfig *pingtunnel.CryptoConfig
	if encryptionMode != pingtunnel.NoEncryption {
		cryptoConfig, err = pingtunnel.NewCryptoConfig(encryptionMode, *cf.encryptionKey)
		if err != nil {
			fmt.Printf("Failed to create crypto config: %v\n", err)
			return
		}
	}

	level := loggo.LEVEL_INFO
	if loggo.NameToLevel(*cf.loglevel) >= 0 {
		level = loggo.NameToLevel(*cf.loglevel)
	}
	loggo.Ini(loggo.Config{
		Level:     level,
		Prefix:    "pingtunnel",
		MaxDay:    3,
		NoLogFile: *cf.nolog > 0,
		NoPrint:   *cf.noprint > 0,
	})
	loggo.Info("start...")
	loggo.Info("key %d", *cf.key)

	if *t == "server" {
		// Parse forward proxy configuration
		var forwardConfig *pingtunnel.ForwardConfig
		if *cf.forward != "" {
			var err error
			forwardConfig, err = pingtunnel.ParseForwardURL(*cf.forward)
			if err != nil {
				fmt.Printf("Invalid forward URL: %v\n", err)
				return
			}
			loggo.Info("Forward proxy configured: %s", *cf.forward)
		}

		s, err := pingtunnel.NewServer(*cf.icmpListen, *cf.key, *cf.maxconn, *cf.maxProcessThread, *cf.maxProcessBuffer, *cf.conntt, cryptoConfig, forwardConfig)
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
		if len(*configFile) > 0 {
			loggo.Info("config %s", *configFile)
		} else {
			loggo.Info("listen %s", *cf.listen)
			loggo.Info("server %s", *cf.server)
			loggo.Info("target %s", *cf.target)
		}

		if len(*cf.s5filter) > 0 {
			err := thirdparty.LoadGeoip2(*cf.s5ftfile)
			if err != nil {
				loggo.Error("Load Sock5 ip file ERROR: %s", err.Error())
				return
			}
		}
		filter := func(addr string) bool {
			if len(*cf.s5filter) <= 0 {
				return true
			}

			taddr, err := net.ResolveTCPAddr("tcp", addr)
			if err != nil {
				return false
			}

			ret, err := thirdparty.GetGeoipCountryIsoCode(taddr.IP.String())
			if err != nil {
				return false
			}
			if len(ret) <= 0 {
				return false
			}
			return ret != *cf.s5filter
		}

		if len(*configFile) > 0 {
			clients, err := runClientsFromConfig(*configFile, cf, &filter, cryptoConfig)
			if err != nil {
				loggo.Error("ERROR: %s", err.Error())
				return
			}
			for i, c := range clients {
				loggo.Info("Client[%d] Listen %s (%s) Server %s (%s) TargetPort %s ICMP Listen %s", i+1, c.Addr(), c.IPAddr(),
					c.ServerAddr(), c.ServerIPAddr(), c.TargetAddr(), c.ICMPAddr())
			}
		} else {
			if *cf.tcpmode == 0 {
				*cf.tcpmodeBuffersize = 0
				*cf.tcpmodeMaxwin = 0
				*cf.tcpmodeResendTimems = 0
				*cf.tcpmodeCompress = 0
				*cf.tcpmodeStat = 0
			}
			c, err := pingtunnel.NewClient(*cf.listen, *cf.server, *cf.target, *cf.timeout, *cf.key, *cf.icmpListen,
				*cf.tcpmode, *cf.tcpmodeBuffersize, *cf.tcpmodeMaxwin, *cf.tcpmodeResendTimems, *cf.tcpmodeCompress,
				*cf.tcpmodeStat, *cf.openSock5, *cf.maxconn, &filter, cryptoConfig, *cf.sock5User, *cf.sock5Pass)
			if err != nil {
				loggo.Error("ERROR: %s", err.Error())
				return
			}
			loggo.Info("Client Listen %s (%s) Server %s (%s) TargetPort %s ICMP Listen %s", c.Addr(), c.IPAddr(),
				c.ServerAddr(), c.ServerIPAddr(), c.TargetAddr(), c.ICMPAddr())
			err = c.Run()
			if err != nil {
				loggo.Error("Run ERROR: %s", err.Error())
				return
			}
		}
	} else {
		return
	}

	if *cf.profile > 0 {
		go http.ListenAndServe("0.0.0.0:"+strconv.Itoa(*cf.profile), nil)
	}

	for {
		time.Sleep(time.Hour)
	}
}

// runClientsFromConfig creates and runs one client per config rule. If any
// rule fails, all already started clients are stopped before returning the
// error.
func runClientsFromConfig(configFile string, cf *ClientFlags, filter *func(addr string) bool, cryptoConfig *pingtunnel.CryptoConfig) (clients []*pingtunnel.Client, err error) {

	rules, err := loadClientRules(configFile, cf.toRule())
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			for _, c := range clients {
				c.Stop()
			}
		}
	}()

	for i, r := range rules {

		tcpmodeBuffersize := r.tcpmodeBuffersize
		tcpmodeMaxwin := r.tcpmodeMaxwin
		tcpmodeResendTimems := r.tcpmodeResendTimems
		tcpmodeCompress := r.tcpmodeCompress
		tcpmodeStat := r.tcpmodeStat
		if r.tcpmode == 0 {
			tcpmodeBuffersize = 0
			tcpmodeMaxwin = 0
			tcpmodeResendTimems = 0
			tcpmodeCompress = 0
			tcpmodeStat = 0
		}

		c, err := pingtunnel.NewClient(r.listen, r.server, r.target, r.timeout, r.key, *cf.icmpListen,
			r.tcpmode, tcpmodeBuffersize, tcpmodeMaxwin, tcpmodeResendTimems, tcpmodeCompress,
			tcpmodeStat, r.openSock5, r.maxconn, filter, cryptoConfig, r.sock5User, r.sock5Pass)
		if err != nil {
			return clients, fmt.Errorf("config rule %d: %s", i+1, err.Error())
		}
		err = c.Run()
		if err != nil {
			return clients, fmt.Errorf("config rule %d: %s", i+1, err.Error())
		}
		clients = append(clients, c)
	}

	return clients, nil
}
