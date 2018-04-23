package main

import (
	"flag"
	"log"
	"time"

	"github.com/henrylee2cn/goutil"
	tp "github.com/henrylee2cn/teleport"
	p2p "github.com/henrylee2cn/tp-p2p"
)

var (
	id    = flag.String("id", "peer-1", "peer id")
	toId  = flag.String("to", "peer-2", "connetct to the peer id")
	proxy = flag.String("proxy", "127.0.0.1:5000", "proxy address")
)

func main() {
	flag.Parse()
	var cfg = p2p.PeerConfig{
		PeerId:      *id,
		Proxy:       *proxy,
		PrintDetail: true,
		CountTime:   true,
	}
	peer := p2p.NewPeer(cfg)
	peer.RoutePush(new(chat))
	for {
		sess, rerr := peer.Tunnel(*toId)
		if rerr != nil {
			tp.Errorf("%v", rerr)
			tp.Infof("wait for %s...", *toId)
			time.Sleep(time.Second * 3)
		} else {
			tp.SetLoggerLevel("ERROR")
			say(sess, "hello "+*toId)
			break
		}
	}
	select {}
}

func say(sess tp.Session, words string) {
	log.Printf("I say: %s", words)
	sess.Push("/chat/say", &words, nil)
}

type chat struct {
	tp.PushCtx
}

func (c *chat) Say(cnt *string) *tp.Rerror {
	log.Printf("%q say: %s", c.Session().Id(), *cnt)
	time.Sleep(time.Second * 3)
	go say(c.Session(), goutil.URLRandomString(12))
	return nil
}
