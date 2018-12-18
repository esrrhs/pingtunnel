package main

import (
	"flag"
	"fmt"
	"pingtunnel"
)

var usage = `
Usage:

    pingtunnel -type server

    pingtunnel -type client -l LOCAL_IP:4455 -s SERVER_IP -t 4455

`

func main() {
	fmt.Println("start...")

	t := flag.String("type", "client", "client or server")
	listen := flag.String("l", ":4455", "listen addr")
	target := flag.Int("t", 4455, "target port")
	server := flag.String("s", "127.0.0.1", "server addr")
	flag.Usage = func() {
		fmt.Printf(usage)
	}

	flag.Parse()

	if flag.NArg() != 0 {
		flag.Usage()
		return
	}

	fmt.Printf("type %s\n", *t)
	fmt.Printf("listen %s\n", *listen)
	fmt.Printf("server %s\n", *server)
	fmt.Printf("target port %d\n", *target)

	if *t == "server" {
		s, err := pingtunnel.NewServer()
		if err != nil {
			fmt.Printf("ERROR: %s\n", err.Error())
			return
		}
		fmt.Printf("Server start\n")
		s.Run()
	}
	if *t == "client" {
		c, err := pingtunnel.NewClient(*listen, *server, *target)
		if err != nil {
			fmt.Printf("ERROR: %s\n", err.Error())
			return
		}
		fmt.Printf("Client Listen %s (%s) Server %s (%s) TargetPort %d:\n", c.Addr(), c.IPAddr(),
			c.ServerAddr(), c.ServerIPAddr(), c.TargetPort())
		c.Run()
	}
}
