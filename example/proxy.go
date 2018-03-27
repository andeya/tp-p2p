package main

import (
	"github.com/henrylee2cn/cfgo"
	"github.com/henrylee2cn/teleport/tunnel"
)

func main() {
	var cfg tunnel.ProxyConfig
	cfgo.MustReg("tunnel_proxy", &cfg)
	tunnel.Proxy(cfg)
}
