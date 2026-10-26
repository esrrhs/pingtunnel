package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/esrrhs/gohome/common"
	"github.com/esrrhs/gohome/loggo"
	"github.com/esrrhs/pingtunnel"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"time"
)

var usage = `
    通过伪造ping，把tcp/udp/socks5流量通过远程服务器转发到目的服务器上。用于突破某些运营商封锁TCP/UDP流量。
    By forging ping, the tcp/udp/socks5 traffic is forwarded to the destination server through the remote server. Used to break certain operators to block TCP/UDP traffic.

Usage:

    // 1. 配置文件模式 Config file mode (推荐 / Recommended)
    pingtunnel -c config.json
    pingtunnel -config config.json
    pingtunnel config.json

    // 2. 命令行模式 CLI mode
    // server (服务端)
    pingtunnel -type server -password SECRET

    // client, Forward socks5 (客户端：SOCKS5动态代理)
    pingtunnel -type client -l LOCAL_IP:4455 -s SERVER_IP -socks5 -password SECRET

    // client, Forward tcp (客户端：TCP端口转发，默认模式)
    pingtunnel -type client -l LOCAL_IP:4455 -s SERVER_IP -t TARGET_IP:TARGET_PORT -password SECRET

    // client, Forward udp (客户端：UDP端口转发)
    pingtunnel -type client -l LOCAL_IP:4455 -s SERVER_IP -t TARGET_IP:TARGET_PORT -udp -password SECRET

    -type       服务器或者客户端 (client or server)

服务端常用参数 (Server param):

    -icmp_l     本地侦听ICMP地址，默认为 0.0.0.0
                Local address, listen for ICMP traffic on this address, defaults to 0.0.0.0

    -password   共享密钥，认证+加密一体，必须两侧一致，最少8个字符 (必需)
                Shared secret for authentication and encryption, must match on both sides, min 8 characters (required)

    -encrypt    加密算法覆盖：chacha20（默认）、aes256、aes128。流量始终加密
                Encryption algorithm override: chacha20 (default), aes256, aes128. Traffic is always encrypted

    -conntt     服务端向目标服务建连超时，默认3000ms
                The connect call's timeout, default 3000ms

    -forward    设置前置/上游二级代理，支持socks5://host:port或http://host:port
                Forward TCP traffic through an upstream proxy (socks5://host:port or http://host:port)

    -maxconn    最大连接数，默认0（不受限制）
                The max num of connections, default 0 is no limit

客户端常用参数 (Client param):

    -l          本地侦听地址，默认 :4455
                Listen addr, default :4455

    -s          服务器地址 (IP或域名)
                Server addr (IP or domain)

    -t          目标地址 (IP:Port)，服务端向此地址转发流量
                Target addr, remote server forwards traffic to this address

    -udp        转发模式切换为UDP模式（默认是TCP模式）
                Forward in UDP mode (default is TCP mode)

    -socks5     开启socks5动态代理转发（此时无需指定 -t）
                Enable socks5 dynamic proxy mode (target addr is not needed)

    -s5user     socks5身份认证用户名，默认无密码
                SOCKS5 username, default is empty and no authentication is required

    -s5pass     socks5身份认证密码，默认无密码
                SOCKS5 password, default is empty and no authentication is required

    -timeout    本地连接空闲超时时间，默认60s
                Conn idle timeout, default 60s

    -password   共享密钥，认证+加密一体，必须两侧一致，最少8个字符 (必需)
                Shared secret for authentication and encryption, must match on both sides, min 8 characters (required)

    -encrypt    加密算法覆盖：chacha20（默认）、aes256、aes128
                Encryption algorithm override: chacha20 (default), aes256, aes128

通用/高级参数 (General & Advanced param):

    -loglevel   日志输出等级：debug, info, warn, error，默认info
                Log level: debug, info, warn, error, default is info

    -nolog      不写日志文件，只打印标准输出
                Do not write log files, only print stdout

    -quiet      静默模式，不向控制台标准输出打印
                Quiet mode, do not print stdout

    -congestion 拥塞控制算法，默认bb（带宽自适应算法），设为空表示不开启
                Congestion control algorithm, default is 'bb', empty string means disabled

    -c, -config 从指定json配置文件读取参数（也可直接作为首个参数：pingtunnel config.json）
                Read parameters from json config file (or pass as first arg: pingtunnel config.json)

    -v, -version 打印版本及编译构建信息
                Print version and build information
`

func main() {
	defer common.CrashLog()

	os.Exit(run(os.Args[1:]))
}

// run holds the whole main body on a private FlagSet so tests can drive it
// with explicit args; the return value is the process exit code. A zero
// return only happens on early exits (usage errors, failed validation,
// listener errors) — a started server or client runs forever below.
func run(args []string) int {

	t := flag.NewFlagSet("pingtunnel", flag.ContinueOnError)
	listen := t.String("l", "", "listen addr")
	target := t.String("t", "", "target addr")
	server := t.String("s", "", "server addr")
	icmpListen := t.String("icmp_l", "0.0.0.0", "listen address for ICMP traffic")
	timeout := t.Int("timeout", 60, "conn timeout")
	password := t.String("password", "", "shared secret for authentication and encryption (required, min 8 characters)")
	encryption := t.String("encrypt", "", "encryption algorithm override: chacha20 (default), aes256, aes128")
	udp := t.Bool("udp", false, "forward in udp mode (default is tcp)")
	nolog := t.Bool("nolog", false, "write log file")
	quiet := t.Bool("quiet", false, "do not print stdout")
	loglevel := t.String("loglevel", "info", "log level")
	socks5 := t.Bool("socks5", false, "socks5 proxy mode")
	sock5_user := t.String("s5user", "", "sock5 username")
	sock5_pass := t.String("s5pass", "", "sock5 password")
	maxconn := t.Int("maxconn", 0, "max num of connections")
	profile := t.Int("profile", 0, "open profile")
	conntt := t.Int("conntt", 3000, "the connect call's timeout in ms")
	forward := t.String("forward", "", "forward TCP traffic through proxy (socks5://host:port or http://host:port)")
	congestion := t.String("congestion", "bb", "congestion control algorithm: bb or empty")
	typ := t.String("type", "", "client or server")
	var configFile string
	t.StringVar(&configFile, "c", "", "path to json config file")
	t.StringVar(&configFile, "config", "", "path to json config file")
	showVersion := t.Bool("v", false, "show version and build info")
	showVersionLong := t.Bool("version", false, "show version and build info")
	t.Usage = func() {
		fmt.Print(usage)
	}

	if err := t.Parse(args); err != nil {
		// Match the historical ExitOnError behavior: -h/--help prints the
		// usage and exits 0; any other parse error prints it and exits 2.
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		t.Usage()
		return 2
	}

	if *showVersion || *showVersionLong {
		fmt.Println("pingtunnel " + pingtunnel.GetVersionInfo())
		return 0
	}

	// Track which flags were explicitly set via command line
	setFlags := make(map[string]bool)
	t.Visit(func(f *flag.Flag) {
		setFlags[f.Name] = true
	})

	// Positional argument support: pingtunnel <config.json>
	if configFile == "" && t.NArg() > 0 {
		arg0 := t.Arg(0)
		if _, err := os.Stat(arg0); err == nil {
			configFile = arg0
		}
	}

	// Auto-detect default config.json if no config or type was specified
	if configFile == "" && !setFlags["type"] && *typ == "" {
		if _, err := os.Stat("config.json"); err == nil {
			configFile = "config.json"
			fmt.Println("Auto-loading default config file: config.json")
		}
	}

	// Load configuration file if specified
	if configFile != "" {
		cfg, err := pingtunnel.LoadConfig(configFile)
		if err != nil {
			fmt.Printf("Failed to load config file (%s): %v\n", configFile, err)
			return 0
		}

		if !setFlags["type"] && cfg.Type != "" {
			*typ = cfg.Type
		}
		if !setFlags["l"] && cfg.Listen != "" {
			*listen = cfg.Listen
		}
		if !setFlags["t"] && cfg.Target != "" {
			*target = cfg.Target
		}
		if !setFlags["s"] && cfg.Server != "" {
			*server = cfg.Server
		}
		if !setFlags["icmp_l"] && cfg.ICMPListen != "" {
			*icmpListen = cfg.ICMPListen
		}
		if !setFlags["timeout"] && cfg.Timeout != 0 {
			*timeout = cfg.Timeout
		}
		if !setFlags["password"] && cfg.Password != "" {
			*password = cfg.Password
		}
		if !setFlags["encrypt"] && cfg.Encrypt != "" {
			*encryption = cfg.Encrypt
		}
		if !setFlags["udp"] && cfg.UDP {
			*udp = true
		}
		if !setFlags["nolog"] && cfg.NoLog {
			*nolog = true
		}
		if !setFlags["quiet"] && cfg.Quiet {
			*quiet = true
		}
		if !setFlags["loglevel"] && cfg.LogLevel != "" {
			*loglevel = cfg.LogLevel
		}
		if !setFlags["socks5"] && cfg.Socks5 {
			*socks5 = true
		}
		if !setFlags["s5user"] && cfg.Sock5User != "" {
			*sock5_user = cfg.Sock5User
		}
		if !setFlags["s5pass"] && cfg.Sock5Pass != "" {
			*sock5_pass = cfg.Sock5Pass
		}
		if !setFlags["maxconn"] && cfg.MaxConn != 0 {
			*maxconn = cfg.MaxConn
		}
		if !setFlags["profile"] && cfg.Profile != 0 {
			*profile = cfg.Profile
		}
		if !setFlags["conntt"] && cfg.ConnectTimeout != 0 {
			*conntt = cfg.ConnectTimeout
		}
		if !setFlags["forward"] && cfg.Forward != "" {
			*forward = cfg.Forward
		}
		if !setFlags["congestion"] && cfg.Congestion != "" {
			*congestion = cfg.Congestion
		}
	}

	if *typ != "client" && *typ != "server" {
		t.Usage()
		return 0
	}
	if *typ == "client" {
		if len(*listen) == 0 || len(*server) == 0 {
			t.Usage()
			return 0
		}
		if !*socks5 && len(*target) == 0 {
			t.Usage()
			return 0
		}
	}

	// Validate the shared secret and derive both the authentication code and
	// the encryption key from it. Everything sent over the wire is encrypted.
	if err := pingtunnel.ValidatePassword(*password); err != nil {
		fmt.Printf("Invalid password: %v\n", err)
		return 0
	}
	authCode := pingtunnel.DeriveAuthCode(*password)

	encryptionMode := pingtunnel.CHACHA20
	if *encryption != "" {
		mode, err := pingtunnel.ParseEncryptionMode(*encryption)
		if err != nil {
			fmt.Printf("Invalid encryption mode: %v\n", err)
			return 0
		}
		if mode == pingtunnel.NoEncryption {
			fmt.Println("Encryption cannot be disabled in v3: traffic is always encrypted")
			return 0
		}
		encryptionMode = mode
	}

	cryptoConfig, err := pingtunnel.NewCryptoConfig(encryptionMode, *password)
	if err != nil {
		fmt.Printf("Failed to create crypto config: %v\n", err)
		return 0
	}

	if *nolog && *quiet {
		fmt.Fprintln(os.Stderr, "Warning: both -nolog and -quiet are set, no log output will be generated.")
	}
	level := loggo.LEVEL_INFO
	if loggo.NameToLevel(*loglevel) >= 0 {
		level = loggo.NameToLevel(*loglevel)
	}
	loggo.Ini(loggo.Config{
		Level:     level,
		Prefix:    "pingtunnel",
		MaxDay:    3,
		NoLogFile: *nolog,
		NoPrint:   *quiet,
	})
	loggo.Info("start pingtunnel (%s)...", pingtunnel.GetVersionInfo())
	loggo.Info("auth code derived from -password; encryption %s", encryptionMode)

	if *typ == "server" {
		// Parse forward proxy configuration
		var forwardConfig *pingtunnel.ForwardConfig
		if *forward != "" {
			var err error
			forwardConfig, err = pingtunnel.ParseForwardURL(*forward)
			if err != nil {
				fmt.Printf("Invalid forward URL: %v\n", err)
				return 0
			}
			loggo.Info("Forward proxy configured: %s", *forward)
		}

		s, err := pingtunnel.NewServer(*icmpListen, authCode, *maxconn, *conntt, cryptoConfig, forwardConfig, *congestion)
		if err != nil {
			loggo.Error("ERROR: %s", err.Error())
			return 0
		}
		loggo.Info("Server start")
		err = s.Run()
		if err != nil {
			loggo.Error("Run ERROR: %s", err.Error())
			return 0
		}
	} else if *typ == "client" {

		loggo.Info("type %s", *typ)
		loggo.Info("listen %s", *listen)
		loggo.Info("server %s", *server)
		loggo.Info("target %s", *target)

		c, err := pingtunnel.NewClient(*listen, *server, *target, *timeout, authCode, *icmpListen,
			*udp, *socks5, *maxconn, cryptoConfig, *sock5_user, *sock5_pass, *congestion)
		if err != nil {
			loggo.Error("ERROR: %s", err.Error())
			return 0
		}
		loggo.Info("Client Listen %s (%s) Server %s (%s) TargetPort %s ICMP Listen %s", c.Addr(), c.IPAddr(),
			c.ServerAddr(), c.ServerIPAddr(), c.TargetAddr(), c.ICMPAddr())
		err = c.Run()
		if err != nil {
			loggo.Error("Run ERROR: %s", err.Error())
			return 0
		}
	} else {
		return 0
	}

	if *profile > 0 {
		go http.ListenAndServe("127.0.0.1:"+strconv.Itoa(*profile), nil)
	}

	for {
		time.Sleep(time.Hour)
	}
}
