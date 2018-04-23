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
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/henrylee2cn/cfgo"
	tp "github.com/henrylee2cn/teleport"
	"github.com/henrylee2cn/teleport/socket"
	heartbeat "github.com/henrylee2cn/tp-ext/plugin-heartbeat"
	"github.com/libp2p/go-reuseport"
)

// PeerConfig p2p tunel config
type PeerConfig struct {
	PeerId             string        `yaml:"peer_id" ini:"peer_id" comment:"p2p tunel config"`
	Proxy              string        `yaml:"proxy"   ini:"proxy"   comment:"p2p tunel config"`
	DefaultDialTimeout time.Duration `yaml:"default_dial_timeout" ini:"default_dial_timeout" comment:"Default maximum duration for dialing; for client role; ns,µs,ms,s,m,h"`
	DefaultBodyCodec   string        `yaml:"default_body_codec"   ini:"default_body_codec"   comment:"Default body codec type id"`
	DefaultSessionAge  time.Duration `yaml:"default_session_age"  ini:"default_session_age"  comment:"Default session max age, if less than or equal to 0, no time limit; ns,µs,ms,s,m,h"`
	DefaultContextAge  time.Duration `yaml:"default_context_age"  ini:"default_context_age"  comment:"Default PULL or PUSH context max age, if less than or equal to 0, no time limit; ns,µs,ms,s,m,h"`
	SlowCometDuration  time.Duration `yaml:"slow_comet_duration"  ini:"slow_comet_duration"  comment:"Slow operation alarm threshold; ns,µs,ms,s ..."`
	PrintDetail        bool          `yaml:"print_detail"         ini:"print_detail"         comment:"Is print body and metadata or not"`
	CountTime          bool          `yaml:"count_time"           ini:"count_time"           comment:"Is count cost time or not"`
}

var _ cfgo.Config = new(PeerConfig)

// Reload Bi-directionally synchronizes config between YAML file and memory.
func (p *PeerConfig) Reload(bind cfgo.BindFunc) error {
	err := bind()
	if err != nil {
		return err
	}
	return p.check()
}

func (p *PeerConfig) check() error {
	if p.PeerId == "" {
		return errors.New("peer id is empty!")
	}
	if p.Proxy == "" {
		return errors.New("tunnel proxy is empty!")
	}
	return nil
}

// Peer the communication peer which is server or client role
type Peer interface {
	tp.EarlyPeer
	Tunnel(peerId string) (tp.Session, *tp.Rerror)
	// Dial connects with the peer of the destination address.
	Dial(addr string, protoFunc ...socket.ProtoFunc) (tp.Session, *tp.Rerror)
	// DialContext connects with the peer of the destination address, using the provided context.
	DialContext(ctx context.Context, addr string, protoFunc ...socket.ProtoFunc) (tp.Session, *tp.Rerror)
	// ServeConn serves the connection and returns a session.
	ServeConn(conn net.Conn, protoFunc ...socket.ProtoFunc) (tp.Session, error)
}

type tunnelPeer struct {
	peerId string
	tp.Peer
	proxyAddr     string
	proxyPeer     tp.Peer
	proxySession  tp.Session
	closeCh       chan struct{}
	tunneling     map[string]chan *tp.Rerror
	tunnelingLock sync.Mutex
	mu            sync.RWMutex
}

func NewPeer(cfg PeerConfig, plugin ...tp.Plugin) Peer {
	t := &tunnelPeer{
		peerId:    cfg.PeerId,
		proxyAddr: cfg.Proxy,
		proxyPeer: tp.NewPeer(
			tp.PeerConfig{},
			heartbeat.NewPing(10),
		),
		closeCh:   make(chan struct{}),
		tunneling: make(map[string]chan *tp.Rerror, 100),
	}
	t.proxyPeer.RoutePull(new(notify))

	rerr := t.connectProxy()
	if rerr != nil {
		tp.Fatalf("NewPeer: %v", rerr)
	}

	go func() {
		ticker := time.NewTicker(time.Second * 10)
		for {
			select {
			case <-t.closeCh:
				ticker.Stop()
				return
			case <-ticker.C:
				if !t.getProxySession().Health() {
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
		PrintDetail:        cfg.PrintDetail,
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
func (t *tunnelPeer) getProxySession() tp.Session {
	t.mu.RLock()
	sess := t.proxySession
	t.mu.RUnlock()
	return sess
}
func (t *tunnelPeer) connectProxy() (rerr *tp.Rerror) {
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

func (t *tunnelPeer) Tunnel(peerId string) (sess tp.Session, rerr *tp.Rerror) {
	sess, ok := t.Peer.GetSession(peerId)
	if ok {
		return sess, nil
	}
	t.tunnelingLock.Lock()
	if ch, ok := t.tunneling[peerId]; ok {
		t.tunnelingLock.Unlock()
		sess, _ = t.Peer.GetSession(peerId)
		return sess, <-ch
	}
	ch := make(chan *tp.Rerror, 1)
	t.tunneling[peerId] = ch
	t.tunnelingLock.Unlock()

	defer func() {
		t.tunnelingLock.Lock()
		ch <- rerr
		close(ch)
		delete(t.tunneling, peerId)
		t.tunnelingLock.Unlock()
	}()

	conn, err := reuseport.Dial("tcp", "", t.proxyAddr)
	if err != nil {
		return nil, tp.NewRerror(tp.CodeDialFailed, tp.CodeText(tp.CodeDialFailed), err.Error())
	}
	localAddr := conn.LocalAddr().String()
	sess, _ = t.proxyPeer.ServeConn(conn)
	defer sess.Close()
	reply := new(TunnelAddrs)
	rerr = sess.Pull(
		"/p2p/apply",
		&ApplyArgs{t.peerId, peerId},
		reply,
	).Rerror()
	if rerr != nil {
		return nil, rerr
	}
	sess, err = t.doTunnel(localAddr, peerId, reply)
	if err != nil {
		return nil, tp.NewRerror(tp.CodeDialFailed, tp.CodeText(tp.CodeDialFailed), err.Error())
	}
	return sess, nil
}

type notify struct {
	tp.PullCtx
}

func (n *notify) Apply(args *ForwardArgs) (*struct{}, *tp.Rerror) {
	p, _ := n.Swap().Load("tunnelPeer")
	peer := p.(*tunnelPeer)

	// filter
	peer.tunnelingLock.Lock()
	if _, ok := peer.tunneling[args.FromPeerId]; ok {
		peer.tunnelingLock.Unlock()
		return nil, rerrTunneling
	}
	ch := make(chan *tp.Rerror, 1)
	peer.tunneling[args.FromPeerId] = ch
	peer.tunnelingLock.Unlock()
	// reply in a new connection
	go peer.replyTunnel(ch, args)
	return nil, nil
}

func (t *tunnelPeer) replyTunnel(ch chan *tp.Rerror, args *ForwardArgs) {
	var rerr *tp.Rerror
	defer func() {
		t.tunnelingLock.Lock()
		ch <- rerr
		close(ch)
		delete(t.tunneling, args.FromPeerId)
		t.tunnelingLock.Unlock()
	}()
	conn, err := reuseport.Dial("tcp", "", t.proxyAddr)
	if err != nil {
		return
	}
	localAddr := conn.LocalAddr().String()
	sess, _ := t.proxyPeer.ServeConn(conn)
	defer sess.Close()
	reply := new(TunnelAddrs)
	rerr = sess.Pull("/p2p/reply", &ReplyArgs{TunnelId: args.TunnelId}, reply).Rerror()
	if rerr != nil {
		return
	}
	_, err = t.doTunnel(localAddr, args.FromPeerId, reply)
	if err != nil {
		rerr = tp.NewRerror(tp.CodeDialFailed, tp.CodeText(tp.CodeDialFailed), err.Error())
	}
}

func (t *tunnelPeer) doTunnel(localAddr, remotePeerId string, addrs *TunnelAddrs) (tp.Session, error) {
	tp.Debugf("local: %s, localNat: %s, remoteNat: %s", localAddr, addrs.LocalAddr, addrs.RemoteAddr)
	addrs.LocalAddr = localAddr
	lis, err := reuseport.Listen("tcp", addrs.LocalAddr)
	if err != nil {
		return nil, err
	}
	defer lis.Close()

	connCh := make(chan net.Conn, 1)

	go func() {
		var closeCh = t.closeCh
		for {
			conn, e := lis.Accept()
			if e != nil {
				select {
				case <-closeCh:
					return
				default:
				}
				continue
			}
			select {
			case connCh <- conn:
			default:
			}
			return
		}
	}()
	var closeCh = make(chan struct{})
	defer close(closeCh)
	go func() {
		for i := 0; i < 10; i++ {
			select {
			case <-closeCh:
				return
			default:
			}
			conn, err := reuseport.Dial("tcp", addrs.LocalAddr, addrs.RemoteAddr)
			if err == nil {
				select {
				case connCh <- conn:
				default:
				}
				return
			}
			tp.Debugf("%v", err)
			time.Sleep(time.Millisecond * 200)
		}
	}()

	select {
	case conn := <-connCh:
		sess, err := t.Peer.ServeConn(conn)
		if err != nil {
			return nil, err
		}
		sess.SetId(remotePeerId)
		// TODO: check tunnel id
		tp.Infof("tunnel to peer: %s", remotePeerId)
		return sess, nil
	case <-time.After(time.Second * 5):
		return nil, errors.New("Tunnel Timeout")
	}
}
