/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package downloader

import (
	"errors"
	"math/big"
	"sync"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/p2p"
)

// MsgWaitTimeout this timeout should not be happened, but we need to handle it in case of such errors.
const MsgWaitTimeout = time.Second * 25
const maxLoopAllowed = 100

var (
	errReceivedQuitMsg = errors.New("Received quit msg")
	errPeerQuit        = errors.New("Peer quit")
)

// Peer define some interfaces that request peer data
type Peer interface {
	Head() (common.Hash, *big.Int)
	RequestHeadersByHashOrNumber(magic uint32, origin common.Hash, num uint64, amount int, reverse bool) error
	RequestBlocksByHashOrNumber(magic uint32, origin common.Hash, num uint64, amount int) error
	GetPeerRequestInfo() (uint32, common.Hash, uint64, int)
	DisconnectPeer(reason string)
}

type peerConn struct {
	peerID         string
	peer           Peer
	waitingMsgMap  map[uint32]chan *p2p.Message // magic => response, so header and body requests can be in flight together
	lockForWaiting sync.RWMutex
	sessionStop    chan struct{}

	log    *log.ScdoLog
	quitCh chan struct{}
}

func newPeerConn(p Peer, peerID string, log *log.ScdoLog) *peerConn {
	return &peerConn{
		peerID:        peerID,
		peer:          p,
		waitingMsgMap: make(map[uint32]chan *p2p.Message),
		log:           log,
		quitCh:        make(chan struct{}),
	}
}

func (p *peerConn) close() {
	if p == nil {
		return
	}
	p.lockForWaiting.Lock()
	defer p.lockForWaiting.Unlock()
	if p.quitCh == nil {
		return
	}
	select {
	case <-p.quitCh:
	default:
		close(p.quitCh)
	}
}

// bindSession installs a stop channel for this download session.
// Closing it unblocks in-flight header and body waits together.
func (p *peerConn) bindSession() chan struct{} {
	ch := make(chan struct{})
	p.lockForWaiting.Lock()
	p.sessionStop = ch
	p.lockForWaiting.Unlock()
	return ch
}

func (p *peerConn) sessionStopCh() <-chan struct{} {
	p.lockForWaiting.RLock()
	defer p.lockForWaiting.RUnlock()
	return p.sessionStop
}

func (p *peerConn) stopSession() {
	p.lockForWaiting.Lock()
	defer p.lockForWaiting.Unlock()
	if p.sessionStop == nil {
		return
	}
	select {
	case <-p.sessionStop:
	default:
		close(p.sessionStop)
	}
}

func (p *peerConn) waitMsg(magic uint32, msgCode uint16, cancelCh chan struct{}) (interface{}, error) {
	return p.waitMsgTimeout(magic, msgCode, cancelCh, MsgWaitTimeout)
}

func (p *peerConn) waitMsgTimeout(magic uint32, msgCode uint16, cancelCh chan struct{}, timeout time.Duration) (ret interface{}, err error) {
	rcvCh := make(chan *p2p.Message, 1)
	p.lockForWaiting.Lock()
	p.waitingMsgMap[magic] = rcvCh
	stopCh := p.sessionStop
	p.lockForWaiting.Unlock()
	defer func() {
		p.lockForWaiting.Lock()
		if p.waitingMsgMap[magic] == rcvCh {
			delete(p.waitingMsgMap, magic)
		}
		p.lockForWaiting.Unlock()
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	loopCount := 0
Again:

	select {

	case <-p.quitCh:
		err = errPeerQuit
	case <-cancelCh:
		err = errReceivedQuitMsg
	case <-stopCh:
		err = errReceivedQuitMsg
	case msg := <-rcvCh:
		switch msgCode {
		case BlockHeadersMsg:
			var reqMsg BlockHeadersMsgBody
			if err := common.Deserialize(msg.Payload, &reqMsg); err != nil {
				loopCount++
				if loopCount > maxLoopAllowed {
					break Again
				}
				goto Again
			}
			if reqMsg.Magic != magic {
				p.log.Debug("Downloader.waitMsg  BlockHeadersMsg MAGIC_NOT_MATCH msg=%s, magic=%d, pid=%s", CodeToStr(msgCode), magic, p.peerID)
				loopCount++
				if loopCount > maxLoopAllowed {
					break Again
				}
				goto Again
			}
			ret = reqMsg.Headers
		case BlocksMsg:
			var reqMsg BlocksMsgBody
			if err := common.Deserialize(msg.Payload, &reqMsg); err != nil {
				loopCount++
				if loopCount > maxLoopAllowed {
					break Again
				}
				goto Again
			}
			if reqMsg.Magic != magic {
				p.log.Debug("Downloader.waitMsg  BlocksMsg MAGIC_NOT_MATCH msg=%s pid=%s", CodeToStr(msgCode), p.peerID)
				loopCount++
				if loopCount > maxLoopAllowed {
					break Again
				}
				goto Again
			}

			ret = reqMsg.Blocks
		}
	case <-timer.C:
		p.log.Debug("Downloader.waitMsg  timeout msg=%s pid=%s", CodeToStr(msgCode), p.peerID)
		err = errReceivedQuitMsg
	}

	return ret, err
}

func (p *peerConn) deliverMsg(msgCode uint16, msg *p2p.Message) {
	defer func() {
		if recover() != nil {
			p.log.Info("peerConn.deliverMsg PANIC msg=%s pid=%s", CodeToStr(msgCode), p.peerID)
		}
	}()

	magic, ok := messageMagic(msgCode, msg)
	if !ok {
		return
	}
	p.lockForWaiting.Lock()
	ch, waiting := p.waitingMsgMap[magic]
	p.lockForWaiting.Unlock()
	if !waiting {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}

func messageMagic(msgCode uint16, msg *p2p.Message) (uint32, bool) {
	switch msgCode {
	case BlockHeadersMsg:
		var body BlockHeadersMsgBody
		if err := common.Deserialize(msg.Payload, &body); err != nil {
			return 0, false
		}
		return body.Magic, true
	case BlocksMsg:
		var body BlocksMsgBody
		if err := common.Deserialize(msg.Payload, &body); err != nil {
			return 0, false
		}
		return body.Magic, true
	default:
		return 0, false
	}
}
