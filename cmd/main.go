package main

import (
	"flag"
	"fmt"
	"pingtunnel"
)

var usage = `
Usage:

    pingtunnel -type server -t TARGET_IP:4455

    pingtunnel -type client -l LOCAL_IP:4455 -t SERVER_IP

`

func main() {
	fmt.Println("start...")

	t := flag.String("type", "client", "client or server")
	listen := flag.String("l", ":4455", "listen addr")
	target := flag.String("t", ":443", "target addr")
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
	fmt.Printf("target %s\n", *target)

	if *t == "server" {
		s, err := pingtunnel.NewServer(*target)
		if err != nil {
			fmt.Printf("ERROR: %s\n", err.Error())
			return
		}
		fmt.Printf("Server Target %s (%s):\n", s.TargetAddr(), s.TargetIPAddr())
		s.Run()
	}
	if *t == "client" {
		c, err := pingtunnel.NewClient(*listen, *target)
		if err != nil {
			fmt.Printf("ERROR: %s\n", err.Error())
			return
		}
		fmt.Printf("Client Listen %s (%s) Target %s (%s):\n", c.Addr(), c.IPAddr(), c.TargetAddr(), c.TargetIPAddr())
		c.Run()
	}
}
