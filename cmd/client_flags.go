package main

import (
	"flag"
	"fmt"
	"io"
)

// clientRule holds the per-rule parameters of a client forwarding rule.
// A rule can come from the command line or one line of the config file.
type clientRule struct {
	listen              string
	target              string
	server              string
	timeout             int
	key                 int
	tcpmode             int
	tcpmodeBuffersize   int
	tcpmodeMaxwin       int
	tcpmodeResendTimems int
	tcpmodeCompress     int
	tcpmodeStat         int
	openSock5           int
	maxconn             int
	sock5User           string
	sock5Pass           string
}

// ClientFlags points at every flag a client needs, including process level
// ones (logging, profiling, encryption, filter...) which are only valid on
// the command line and are ignored inside config file rules.
type ClientFlags struct {
	listen              *string
	target              *string
	server              *string
	icmpListen          *string
	timeout             *int
	key                 *int
	encryption          *string
	encryptionKey       *string
	tcpmode             *int
	tcpmodeBuffersize   *int
	tcpmodeMaxwin       *int
	tcpmodeResendTimems *int
	tcpmodeCompress     *int
	tcpmodeStat         *int
	nolog               *int
	noprint             *int
	loglevel            *string
	openSock5           *int
	sock5User           *string
	sock5Pass           *string
	maxconn             *int
	maxProcessThread    *int
	maxProcessBuffer    *int
	profile             *int
	conntt              *int
	forward             *string
	s5filter            *string
	s5ftfile            *string
}

// builtinClientRule holds the original built-in flag defaults, used when no
// command line override is given (nil defaults).
var builtinClientRule = clientRule{
	timeout:             60,
	tcpmodeBuffersize:   1 * 1024 * 1024,
	tcpmodeMaxwin:       20000,
	tcpmodeResendTimems: 400,
}

// newClientFlagSet registers all client flags on fs. When defaults is not
// nil, its values are used as flag defaults, so config file rules inherit
// the command line values they don't set themselves; otherwise the built-in
// defaults (the original ones from main) are used.
func newClientFlagSet(fs *flag.FlagSet, defaults *clientRule) *ClientFlags {
	d := builtinClientRule
	if defaults != nil {
		d = *defaults
	}
	return &ClientFlags{
		listen:              fs.String("l", d.listen, "listen addr"),
		target:              fs.String("t", d.target, "target addr"),
		server:              fs.String("s", d.server, "server addr"),
		icmpListen:          fs.String("icmp_l", "0.0.0.0", "listen address for ICMP traffic"),
		timeout:             fs.Int("timeout", d.timeout, "conn timeout"),
		key:                 fs.Int("key", d.key, "key"),
		encryption:          fs.String("encrypt", "", "encryption mode: aes128, aes256, chacha20"),
		encryptionKey:       fs.String("encrypt-key", "", "encryption key (base64 or passphrase)"),
		tcpmode:             fs.Int("tcp", d.tcpmode, "tcp mode"),
		tcpmodeBuffersize:   fs.Int("tcp_bs", d.tcpmodeBuffersize, "tcp mode buffer size"),
		tcpmodeMaxwin:       fs.Int("tcp_mw", d.tcpmodeMaxwin, "tcp mode max win"),
		tcpmodeResendTimems: fs.Int("tcp_rst", d.tcpmodeResendTimems, "tcp mode resend time ms"),
		tcpmodeCompress:     fs.Int("tcp_gz", d.tcpmodeCompress, "tcp data compress"),
		tcpmodeStat:         fs.Int("tcp_stat", d.tcpmodeStat, "print tcp stat"),
		nolog:               fs.Int("nolog", 0, "write log file"),
		noprint:             fs.Int("noprint", 0, "print stdout"),
		loglevel:            fs.String("loglevel", "info", "log level"),
		openSock5:           fs.Int("sock5", d.openSock5, "sock5 mode"),
		sock5User:           fs.String("s5user", d.sock5User, "sock5 username"),
		sock5Pass:           fs.String("s5pass", d.sock5Pass, "sock5 password"),
		maxconn:             fs.Int("maxconn", d.maxconn, "max num of connections"),
		maxProcessThread:    fs.Int("maxprt", 100, "max process thread in server"),
		maxProcessBuffer:    fs.Int("maxprb", 1000, "max process thread's buffer in server"),
		profile:             fs.Int("profile", 0, "open profile"),
		conntt:              fs.Int("conntt", 1000, "the connect call's timeout"),
		forward:             fs.String("forward", "", "forward TCP traffic through proxy (socks5://host:port or http://host:port)"),
		s5filter:            fs.String("s5filter", "", "sock5 filter"),
		s5ftfile:            fs.String("s5ftfile", "GeoLite2-Country.mmdb", "sock5 filter file"),
	}
}

func (cf *ClientFlags) toRule() *clientRule {
	return &clientRule{
		listen:              *cf.listen,
		target:              *cf.target,
		server:              *cf.server,
		timeout:             *cf.timeout,
		key:                 *cf.key,
		tcpmode:             *cf.tcpmode,
		tcpmodeBuffersize:   *cf.tcpmodeBuffersize,
		tcpmodeMaxwin:       *cf.tcpmodeMaxwin,
		tcpmodeResendTimems: *cf.tcpmodeResendTimems,
		tcpmodeCompress:     *cf.tcpmodeCompress,
		tcpmodeStat:         *cf.tcpmodeStat,
		openSock5:           *cf.openSock5,
		maxconn:             *cf.maxconn,
		sock5User:           *cf.sock5User,
		sock5Pass:           *cf.sock5Pass,
	}
}

// parseClientRule parses one rule's argument list. Process level flags are
// accepted and ignored so a single command line can be pasted into the
// config file as-is.
func parseClientRule(args []string, defaults *clientRule) (*clientRule, error) {
	fs := flag.NewFlagSet("client-rule", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cf := newClientFlagSet(fs, defaults)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	return cf.toRule(), nil
}
