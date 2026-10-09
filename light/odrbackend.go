/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package light

import (
	rand2 "math/rand"
	"strings"
	"sync"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/core/store"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/p2p"
)

var (
	// ErrNoMorePeers means this shard's light client has nobody to ask yet.
	ErrNoMorePeers   = errors.New("No peers found")
	errServiceQuited = errors.New("Service has quited")
)

type odrBackend struct {
	lock       sync.Mutex
	msgCh      chan *p2p.Message
	quitCh     chan struct{}
	requestMap map[uint32]chan odrResponse
	wg         sync.WaitGroup
	peers      *peerSet
	bcStore    store.BlockchainStore // used to validate the retrieved ODR object.
	log        *log.ScdoLog

	shard uint
}

func newOdrBackend(bcStore store.BlockchainStore, shard uint) *odrBackend {
	o := &odrBackend{
		msgCh:      make(chan *p2p.Message),
		requestMap: make(map[uint32]chan odrResponse),
		quitCh:     make(chan struct{}),
		bcStore:    bcStore,
		log:        log.GetLogger("odrBackend"),
		shard:      shard,
	}
	rand2.Seed(time.Now().UnixNano())
	return o
}

func (o *odrBackend) start(peers *peerSet) {
	o.peers = peers
	o.wg.Add(1)
	go o.run()
}

func (o *odrBackend) run() {
	defer o.wg.Done()
loopOut:
	for {
		select {
		case msg := <-o.msgCh:
			o.handleResponse(msg)
		case <-o.quitCh:
			break loopOut
		}
	}
}

func (o *odrBackend) handleResponse(msg *p2p.Message) {
	factory, ok := odrResponseFactories[msg.Code]
	if !ok {
		return
	}

	response := factory()
	if err := common.Deserialize(msg.Payload, response); err != nil {
		o.log.Error("Failed to deserialize ODR response, code = %s, error = %s", codeToStr(msg.Code), err)
		return
	}

	o.lock.Lock()
	defer o.lock.Unlock()

	if reqCh, ok := o.requestMap[response.getRequestID()]; ok {
		// Keep the request until retrieve finishes. The first peer is often a
		// shard that has not stored this tx yet; a later peer must still be able
		// to answer. The channel is buffered to the peer count, so this send
		// does not block while o.lock is held.
		select {
		case reqCh <- response:
		default:
			o.log.Debug("drop ODR response, request buffer full, reqID=%d", response.getRequestID())
		}
	}
}

func (o *odrBackend) getReqInfo(filter peerFilter) (uint32, chan odrResponse, []*peer, error) {
	if o.peers == nil {
		return 0, nil, nil, ErrNoMorePeers
	}
	peerL := o.peers.choosePeers(filter)
	if len(peerL) == 0 {
		return 0, nil, nil, ErrNoMorePeers
	}

	reqID := rand2.Uint32()
	ch := make(chan odrResponse, len(peerL))

	o.lock.Lock()
	if o.requestMap[reqID] != nil {
		panic("reqid conflict")
	}

	o.requestMap[reqID] = ch
	o.lock.Unlock()
	return reqID, ch, peerL, nil
}

// retrieve retrieves the requested ODR object from remote peer.
func (o *odrBackend) retrieve(request odrRequest) (odrResponse, error) {
	return o.retrieveWithFilter(request, peerFilter{})
}

// retrieve retrieves the requested ODR object from remote peer with specified peer filter.
func (o *odrBackend) retrieveWithFilter(request odrRequest, filter peerFilter) (odrResponse, error) {
	reqID, ch, peerL, err := o.getReqInfo(filter)
	if err != nil {
		// No peer on the source shard is the same wait as a header that has
		// not been synced yet. Callers that write a block leave it queued.
		if err == ErrNoMorePeers {
			return nil, errors.NewStackedError(types.ErrHeaderNotReady, ErrNoMorePeers.Error())
		}
		return nil, err
	}
	defer func() {
		o.lock.Lock()
		delete(o.requestMap, reqID)
		close(ch)
		o.lock.Unlock()
	}()

	request.setRequestID(reqID)
	code, payload := request.code(), common.SerializePanic(request)
	for _, p := range peerL {
		o.log.Debug("peer send request, code = %s, payloadSizeBytes = %v", codeToStr(code), len(payload))
		if err = p2p.SendMessage(p.rw, code, payload); err != nil {
			o.log.Info("Failed to send message with peer %s", p.peerStrID)
			return nil, errors.NewStackedErrorf(err, "failed to send P2P message")
		}
	}

	timeout := time.NewTimer(msgWaitTimeout)
	defer timeout.Stop()

	resp, err := o.collectResponses(ch, len(peerL), reqID, timeout)
	if err != nil {
		return nil, err
	}
	if err = resp.validate(request, o.bcStore); err != nil {
		return nil, errors.NewStackedError(err, "failed to validate ODR response")
	}
	return resp, nil
}

// collectResponses waits until a peer returns the object, every peer reports
// that it does not have the object, or the request times out.
// A "leveldb: not found" answer is that peer missing the tx, not an invalid
// debt. The next peer is given a chance to answer. When nobody has it, the
// error is ErrHeaderNotReady so block sync leaves the block queued.
func (o *odrBackend) collectResponses(ch chan odrResponse, nPeers int, reqID uint32, timeout *time.Timer) (odrResponse, error) {
	if nPeers < 1 {
		nPeers = 1
	}
	var lastMiss error
	got := 0
	for {
		select {
		case resp := <-ch:
			if resp == nil {
				continue
			}
			got++
			if err := resp.getError(); err != nil {
				if odrPeerMiss(err) && got < nPeers {
					lastMiss = err
					continue
				}
				if odrPeerMiss(err) {
					return nil, errors.NewStackedError(types.ErrHeaderNotReady, err.Error())
				}
				return nil, errors.NewStackedError(err, "failed to handle ODR request on server side")
			}
			return resp, nil
		case <-o.quitCh:
			return nil, errServiceQuited
		case <-timeout.C:
			if lastMiss != nil {
				return nil, errors.NewStackedErrorf(types.ErrHeaderNotReady, "source shard data is not on the peers that answered (%s); wait for msg reqid=%d timeout", lastMiss.Error(), reqID)
			}
			return nil, errors.NewStackedErrorf(types.ErrHeaderNotReady, "wait for msg reqid=%d timeout", reqID)
		}
	}
}

// odrPeerMiss is true when the answering node has no tx/receipt/debt for the
// hash. That node may still be syncing. It is not a failed proof.
func odrPeerMiss(err error) bool {
	return err != nil && strings.Contains(err.Error(), "leveldb: not found")
}

// deliver hands a response to the ODR loop. After close it returns
// errServiceQuited. The message channel stays open so a late handler cannot
// panic by sending on a closed channel.
func (o *odrBackend) deliver(msg *p2p.Message) error {
	if o == nil || msg == nil {
		return errServiceQuited
	}
	select {
	case <-o.quitCh:
		return errServiceQuited
	default:
	}
	select {
	case <-o.quitCh:
		return errServiceQuited
	case o.msgCh <- msg:
		return nil
	}
}

func (o *odrBackend) close() {
	select {
	case <-o.quitCh:
	default:
		close(o.quitCh)
	}

	o.wg.Wait()
}
