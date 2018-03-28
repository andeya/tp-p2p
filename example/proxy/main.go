package main

import (
	"flag"
	p2p "github.com/henrylee2cn/tp-p2p"
)

var (
	addr = flag.String("addr", "0.0.0.0:5000", "listen address")
)

func main() {
	flag.Parse()
	var cfg = p2p.ProxyConfig{
		ListenAddress: *addr,
		CountTime:     true,
	}
	p2p.Proxy(cfg)
}
