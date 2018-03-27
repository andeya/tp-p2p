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

	tp "github.com/henrylee2cn/teleport"
	"github.com/henrylee2cn/teleport/plugin"
	heartbeat "github.com/henrylee2cn/tp-ext/plugin-heartbeat"
)

// TunnelPeerConfig p2p tunel config
type TunnelPeerConfig struct {
	PeerID             string        `yaml:"peer_id" ini:"peer_id" comment:"p2p tunel config"`
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
	if p.PeerID == "" {
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
	Tunnel() (Session, *Rerror)
	// Dial connects with the peer of the destination address.
	Dial(addr string, protoFunc ...socket.ProtoFunc) (Session, *Rerror)
	// DialContext connects with the peer of the destination address, using the provided context.
	DialContext(ctx context.Context, addr string, protoFunc ...socket.ProtoFunc) (Session, *Rerror)
	// ServeConn serves the connection and returns a session.
	ServeConn(conn net.Conn, protoFunc ...socket.ProtoFunc) (Session, error)
}

type tunnelPeer struct {
	tp.Peer
	proxySession tp.Session
}

func NewTunnelPeer(cfg TunnelPeerConfig, plugin ...Plugin) TunnelPeer {
	cli := tp.NewPeer(tp.PeerConfig{
		RedialTimes: 1024,
	},
		heartbeat.NewPong(),
		plugin.LaunchAuth(func() { return cfg.PeerID }),
	)
	cli.RoutePull(new(notify))
	sess, rerr := cli.Dial(cfg.ProxyTunnel)
	if rerr != nil {
		tp.Fatalf("NewTunnelPeer: %v", rerr)
	}

	plugin = append(
		[]tp.Plugin{
			heartbeat.NewPong(),
			heartbeat.NewPing(12),
		},
		plugin...,
	)

	peer := tp.NewPeer(tp.PeerConfig{
		DefaultDialTimeout: cfg.DefaultDialTimeout,
		DefaultBodyCodec:   cfg.DefaultBodyCodec,
		DefaultSessionAge:  cfg.DefaultSessionAge,
		DefaultContextAge:  cfg.DefaultContextAge,
		SlowCometDuration:  cfg.SlowCometDuration,
		PrintBody:          cfg.PrintBody,
		CountTime:          cfg.CountTime,
	}, plugin...)

	return &tunnelPeer{
		Peer:         peer,
		proxySession: sess,
	}
}

type notify struct {
	tp.PullCtx
}

func (t *notify) Apply(args *ForwardArgs) (*struct{}, *tp.Rerror) {
	// filter
	// ...

	// reply in a new connection

	t.Peer().ServeConn(conn)
	return nil, nil
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
