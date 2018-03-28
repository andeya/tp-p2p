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
	"sync"
	"time"

	"github.com/henrylee2cn/cfgo"
	"github.com/henrylee2cn/goutil"
	tp "github.com/henrylee2cn/teleport"
	"github.com/henrylee2cn/tp-ext/plugin-heartbeat"
)

// ProxyConfig proxy peer config
// Note:
//  yaml tag is used for github.com/henrylee2cn/cfgo
//  ini tag is used for github.com/henrylee2cn/ini
type ProxyConfig struct {
	ListenAddress     string        `yaml:"listen_address"      ini:"listen_address"      comment:"Listen address; for server role"`
	SlowCometDuration time.Duration `yaml:"slow_comet_duration" ini:"slow_comet_duration" comment:"Slow operation alarm threshold; ns,µs,ms,s ..."`
	CountTime         bool          `yaml:"count_time"          ini:"count_time"          comment:"Is count cost time or not"`
}

var _ cfgo.Config = new(ProxyConfig)

// Reload Bi-directionally synchronizes config between YAML file and memory.
func (p *ProxyConfig) Reload(bind cfgo.BindFunc) error {
	return bind()
}

func Proxy(cfg ProxyConfig) {
	srv := tp.NewPeer(tp.PeerConfig{
		CountTime:         cfg.CountTime,
		ListenAddress:     cfg.ListenAddress,
		SlowCometDuration: cfg.SlowCometDuration,
	},
		heartbeat.NewPong(),
	)
	srv.RoutePull(new(p2p))
	srv.ListenAndServe()
}

var (
	rerrNotOnline   = tp.NewRerror(10001, "Not Online", "")
	rerrWaitTimeout = tp.NewRerror(10002, "Wait for the response from the peer to timeout", "")
	rerrCanceled    = tp.NewRerror(10003, "Peer wait timeout, canceled apply", "")
	rerrTunneling   = tp.NewRerror(10004, "Tunneling", "")
)

var cache = struct {
	infos map[string]*TunnelInfo
	mu    sync.RWMutex
}{
	infos: make(map[string]*TunnelInfo),
}

type (
	p2p struct {
		tp.PullCtx
	}
	TunnelInfo struct {
		ch       chan struct{}
		fromAddr string
		toAddr   string
	}
)

func (p *p2p) Online(args *OnlineArgs) (*struct{}, *tp.Rerror) {
	if oldSess, ok := p.Session().Peer().GetSession(args.PeerId); ok {
		oldSess.Close()
	}
	p.Session().SetId(args.PeerId)
	return nil, nil
}

func (p *p2p) Apply(args *ApplyArgs) (*TunnelAddrs, *tp.Rerror) {
	sess2, ok := p.Session().Peer().GetSession(args.ToPeerId)
	if !ok {
		return nil, rerrNotOnline
	}

	tunnelId := args.FromPeerId + args.ToPeerId + goutil.URLRandomString(8)
	fromPeerIp := p.RealIp()

	info := &TunnelInfo{
		ch:       make(chan struct{}),
		fromAddr: fromPeerIp,
	}

	cache.mu.Lock()
	cache.infos[tunnelId] = info
	cache.mu.Unlock()
	defer func() {
		close(info.ch)
		cache.mu.Lock()
		delete(cache.infos, tunnelId)
		cache.mu.Unlock()
	}()

	rerr := sess2.Pull("/notify/apply", ForwardArgs{
		TunnelId:   tunnelId,
		FromPeerId: args.FromPeerId,
	}, nil).Rerror()

	if rerr != nil {
		return nil, rerr
	}

	select {
	case <-time.After(time.Second * 2):
		return nil, rerrWaitTimeout
	case <-info.ch:
		return &TunnelAddrs{
			TunnelId:   tunnelId,
			LocalAddr:  info.fromAddr,
			RemoteAddr: info.toAddr,
		}, nil
	}
}

func (p *p2p) Reply(args *ReplyArgs) (*TunnelAddrs, *tp.Rerror) {
	cache.mu.RLock()
	info, ok := cache.infos[args.TunnelId]
	cache.mu.RUnlock()
	if !ok {
		return nil, rerrCanceled
	}
	info.toAddr = p.RealIp()
	select {
	case info.ch <- struct{}{}:
		return &TunnelAddrs{
			TunnelId:   args.TunnelId,
			LocalAddr:  info.toAddr,
			RemoteAddr: info.fromAddr,
		}, nil
	default:
		return nil, rerrCanceled
	}
}
