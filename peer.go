// Copyright 2018 HenryLee. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package p2p

import (
	"errors"
	"sync"
	"time"

	tp "github.com/henrylee2cn/teleport"
	"github.com/henrylee2cn/teleport/plugin"
	heartbeat "github.com/henrylee2cn/tp-ext/plugin-heartbeat"
	"github.com/libp2p/go-reuseport"
)

// TunnelPeerConfig p2p tunel config
type TunnelPeerConfig struct {
	PeerId             string        `yaml:"peer_id" ini:"peer_id" comment:"p2p tunel config"`
	Proxy              string        `yaml:"proxy"   ini:"proxy"   comment:"p2p tunel config"`
	DefaultDialTimeout time.Duration `yaml:"default_dial_timeout" ini:"default_dial_timeout" comment:"Default maximum duration for dialing; for client role; ns,µs,ms,s,m,h"`
	DefaultBodyCodec   string        `yaml:"default_body_codec"   ini:"default_body_codec"   comment:"Default body codec type id"`
	DefaultSessionAge  time.Duration `yaml:"default_session_age"  ini:"default_session_age"  comment:"Default session max age, if less than or equal to 0, no time limit; ns,µs,ms,s,m,h"`
	DefaultContextAge  time.Duration `yaml:"default_context_age"  ini:"default_context_age"  comment:"Default PULL or PUSH context max age, if less than or equal to 0, no time limit; ns,µs,ms,s,m,h"`
	SlowCometDuration  time.Duration `yaml:"slow_comet_duration"  ini:"slow_comet_duration"  comment:"Slow operation alarm threshold; ns,µs,ms,s ..."`
	PrintBody          bool          `yaml:"print_body"           ini:"print_body"           comment:"Is print body or not"`
	CountTime          bool          `yaml:"count_time"           ini:"count_time"           comment:"Is count cost time or not"`
}

var _ cfgo.Config = new(TunnelPeerConfig)

// Reload Bi-directionally synchronizes config between YAML file and memory.
func (p *TunnelPeerConfig) Reload(bind cfgo.BindFunc) error {
	err := bind()
	if err != nil {
		return err
	}
	return p.check()
}

func (p *TunnelPeerConfig) check() error {
	if p.PeerId == "" {
		return errors.New("peer id is empty!")
	}
	if p.Proxy == "" {
		return errors.New("tunnel proxy is empty!")
	}
	return nil
}

// TunnelPeer the communication peer which is server or client role
type TunnelPeer interface {
	tp.EarlyPeer
	Tunnel(peerId string) (Session, *Rerror)
	// Dial connects with the peer of the destination address.
	Dial(addr string, protoFunc ...socket.ProtoFunc) (Session, *Rerror)
	// DialContext connects with the peer of the destination address, using the provided context.
	DialContext(ctx context.Context, addr string, protoFunc ...socket.ProtoFunc) (Session, *Rerror)
	// ServeConn serves the connection and returns a session.
	ServeConn(conn net.Conn, protoFunc ...socket.ProtoFunc) (Session, error)
}

type tunnelPeer struct {
	peerId string
	tp.Peer
	proxyAddr    string
	proxyPeer    tp.Peer
	proxySession tp.Session
	closeCh      chan struct{}
	mu           sync.Mutex
}

func NewTunnelPeer(cfg TunnelPeerConfig, plugin ...Plugin) TunnelPeer {
	t := &tunnelPeer{
		peerId:       cfg.PeerId,
		proxyAddr:    cfg.Proxy,
		proxySession: sess,
		proxyPeer: tp.NewPeer(
			tp.PeerConfig{},
			heartbeat.NewPing(10),
		),
	}
	t.proxyPeer.RoutePull(new(notify))

	rerr := t.connectProxy()
	if rerr != nil {
		tp.Fatalf("NewTunnelPeer: %v", rerr)
	}

	go func() {
		ticker := time.NewTicker(time.Second * 10)
		for {
			select {
			case <-closeCh:
				ticker.Stop()
				return
			case <-ticker.C:
				if !t.proxySession.Health() {
					rerr := t.connectProxy()
					if rerr != nil {
						tp.Warnf("connect proxy: %v", rerr)
					}
				}
			}
		}
	}()

	t.Peer = tp.NewPeer(tp.PeerConfig{
		DefaultDialTimeout: cfg.DefaultDialTimeout,
		DefaultBodyCodec:   cfg.DefaultBodyCodec,
		DefaultSessionAge:  cfg.DefaultSessionAge,
		DefaultContextAge:  cfg.DefaultContextAge,
		SlowCometDuration:  cfg.SlowCometDuration,
		PrintBody:          cfg.PrintBody,
		CountTime:          cfg.CountTime,
	}, append(
		[]tp.Plugin{
			heartbeat.NewPong(),
			heartbeat.NewPing(12),
		},
		plugin...,
	)...)

	return t
}

func (t *tunnelPeer) connectProxy() (rerr *Rerror) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := 0; i < 3; i++ {
		t.proxySession, rerr = t.proxyPeer.Dial(t.proxyAddr)
		if rerr == nil {
			t.proxySession.Swap().Store("tunnelPeer", t)
			return t.proxySession.Pull("/p2p/online", &OnlineArgs{PeerId: t.peerId}, nil).Rerror()
		}
	}
	return rerr
}

func (t *tunnelPeer) Tunnel(peerId string) (Session, *Rerror) {
	reply := new(TunnelIps)
	rerr := t.proxySession.Pull(
		"/p2p/apply",
		&ApplyArgs{t.peerId, peerId},
		reply,
	).Rerror()
	if rerr != nil {
		return nil, rerr
	}

}

type notify struct {
	tp.PullCtx
}

func (n *notify) Apply(args *ForwardArgs) (*struct{}, *tp.Rerror) {
	// filter
	// ...

	// reply in a new connection
	tunnelPeer, _ := n.Swap().Load("tunnelPeer")
	go tunnelPeer.(*tunnelPeer).replyTunnel(args)
	return nil, nil
}

func (t *tunnelPeer) replyTunnel(args *ForwardArgs) {
	conn, err := reuseport.Dial("tcp", "", t.proxyAddr)
	if err != nil {
		return
	}
	sess, err := t.proxyPeer.ServeConn(conn)
	if err != nil {
		return
	}
	reply := new(TunnelIps)
	rerr := sess.Pull("/p2p/reply", &ReplyArgs{TunnelId: args.TunnelId}, reply).Rerror()
	if rerr != nil {
		return
	}
	lis, err := reuseport.Listen("tcp", reply.PeerIp1)
	if err != nil {
		return
	}
	go t.Peer.ServeListener(lis)

}

type newAuthPlugin struct{}

func (d *newAuthPlugin) Name() string {
	return "auth"
}

func (t *newAuthPlugin) PostDial(sess tp.PreSession) *tp.Rerror {

	return nil
}

func (t *newAuthPlugin) PostAccept(sess tp.PreSession) *tp.Rerror {

	return nil
}
