/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package downloader

import (
	"fmt"
	"math/big"
	rand2 "math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/consensus"
	"github.com/scdoproject/go-scdo/core"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/event"
	"github.com/scdoproject/go-scdo/log"
	"github.com/scdoproject/go-scdo/p2p"
)

const (
	// GetBlockHeadersMsg message type for getting block headers
	GetBlockHeadersMsg uint16 = 8
	// BlockHeadersMsg message type for delivering block headers
	BlockHeadersMsg uint16 = 9
	// GetBlocksMsg message type for getting blocks
	GetBlocksMsg uint16 = 10
	// BlocksPreMsg is sent before BlockMsg, containing block numbers of BlockMsg.
	BlocksPreMsg uint16 = 11
	// BlocksMsg message type for delivering blocks
	BlocksMsg uint16 = 12
)

// CodeToStr message code -> message string
func CodeToStr(code uint16) string {
	switch code {
	case GetBlockHeadersMsg:
		return "downloader.GetBlockHeadersMsg"
	case BlockHeadersMsg:
		return "downloader.BlockHeadersMsg"
	case GetBlocksMsg:
		return "downloader.GetBlocksMsg"
	case BlocksPreMsg:
		return "downloader.BlocksPreMsg"
	case BlocksMsg:
		return "downloader.BlocksMsg"
	default:
		return "unknown"
	}
}

var (
	// MaxBlockFetch amount of blocks to be fetched per retrieval request.
	// The serving peer still cuts the reply at MaxMessageLength.
	MaxBlockFetch = 64
	// MaxHeaderFetch amount of block headers to be fetched per retrieval request
	MaxHeaderFetch = 512
	// maxBodyInflight is how many block-body batches one peer may have in flight.
	// Header fetches run beside these, so download overlaps verify/write.
	maxBodyInflight = 2
	// bodyWaitTimeout frees a slow in-flight body request so another peer can take it.
	bodyWaitTimeout = 8 * time.Second

	// MaxForkAncestry maximum chain reorganisation
	MaxForkAncestry = 90000
	peerIdleTime    = time.Second // peer's wait time for next turn if no task now

	//MaxMessageLength maximum message length
	MaxMessageLength = 2 * 1024 * 1024
	statusNone       = 1 // no sync session
	statusPreparing  = 2 // sync session is preparing
	statusFetching   = 3 // sync session is downloading
	statusCleaning   = 4 // sync session is cleaning
)

var (
	errHashNotMatch          = errors.New("Hash not match")
	errInvalidAncestor       = errors.New("Ancestor is invalid")
	errInvalidPacketReceived = errors.New("Invalid packet received")

	// ErrIsSynchronising indicates downloader is synchronising
	ErrIsSynchronising = errors.New("Is synchronising")

	errMaxForkAncestor = errors.New("Can not find ancestor when reached MaxForkAncestry")
	errPeerNotFound    = errors.New("Peer not found")
	errSyncErr         = errors.New("Err occurs when syncing")
)

// Downloader sync block chain with remote peer
type Downloader struct {
	cancelCh   chan struct{}        // Cancel current synchronising session
	masterPeer string               // Identifier of the best peer
	peers      map[string]*peerConn // peers map. peerID=>peer

	syncStatus int
	// stopped is set by Terminate. Cancel only ends the current session;
	// a shutdown must also refuse the session that is about to start.
	stopped bool
	tm      *taskMgr

	scdo        ScdoBackend
	chain       *core.Blockchain
	sessionWG   sync.WaitGroup
	acceptPeers bool
	activeWG    *sync.WaitGroup
	log         *log.ScdoLog
	lock        sync.RWMutex
	writeNS     int64 // smoothed nanoseconds spent in the last block writes
	scores      map[string]*peerStat

	// Cross-shard dependency wait. Alephium keeps a block queued until its
	// group dependencies exist; QuarkChain will not spend a cross-shard
	// transfer until the source shard has buried it. Neither case is a bad block.
	waitReason string
	waitNeed   uint64
	waitHave   uint64
}

// BlockHeadersMsgBody represents a message struct for BlockHeadersMsg
type BlockHeadersMsgBody struct {
	Magic   uint32
	Headers []*types.BlockHeader
}

// BlocksMsgBody represents a message struct for BlocksMsg
type BlocksMsgBody struct {
	Magic  uint32
	Blocks []*types.Block
}

// ScdoBackend wraps all methods required for downloader.
type ScdoBackend interface {
	TxPool() *core.TransactionPool
	DebtPool() *core.DebtPool
}

// NewDownloader create Downloader
func NewDownloader(chain *core.Blockchain, scdo ScdoBackend) *Downloader {
	d := &Downloader{
		cancelCh:   make(chan struct{}),
		peers:      make(map[string]*peerConn),
		scdo:       scdo,
		chain:      chain,
		syncStatus: statusNone,
		scores:     make(map[string]*peerStat),
	}

	d.log = log.GetLogger("download")
	rand2.Seed(time.Now().UnixNano())
	return d
}

func (d *Downloader) IsSyncStatusNone() bool {
	d.lock.Lock()

	if d.syncStatus != statusNone {
		d.lock.Unlock()
		return false
	} else {
		d.lock.Unlock()
		return true
	}
}

func (d *Downloader) getReadableStatus() string {
	var status string

	switch d.syncStatus {
	case statusNone:
		status = "NotSyncing"
	case statusPreparing:
		status = "Preparing"
	case statusFetching:
		status = "Downloading"
	case statusCleaning:
		status = "Cleaning"
	}

	return status
}

// Progress returns the sync snapshot for scdo_syncing.
// While a session is running, current/highest/blk/s/ETA come from the
// smoothed writer. Otherwise the local chain height is both ends.
func (d *Downloader) Progress() SyncProgress {
	d.lock.RLock()
	status := d.syncStatus
	tm := d.tm
	peers := len(d.peers)
	chain := d.chain
	waitReason := d.waitReason
	waitNeed := d.waitNeed
	waitHave := d.waitHave
	d.lock.RUnlock()

	prog := SyncProgress{
		Peers:             peers,
		ETA:               "0s",
		WaitingOn:         waitReason,
		ConfirmationsNeed: waitNeed,
		ConfirmationsHave: waitHave,
	}
	if status == statusFetching && tm != nil {
		cur, high, bps, eta := tm.progressSnapshot()
		prog.Syncing = cur < high
		prog.Current = cur
		prog.Highest = high
		prog.BlocksPerSec = bps
		if eta != "" {
			prog.ETA = eta
		}
		return prog
	}
	if chain != nil && chain.CurrentBlock() != nil {
		height := chain.CurrentBlock().Header.Height
		prog.Current = height
		prog.Highest = height
	}
	return prog
}

func (d *Downloader) recordDelivery(peerID string, blocks int, elapsed time.Duration) {
	if blocks <= 0 || elapsed <= 0 {
		return
	}
	d.lock.Lock()
	defer d.lock.Unlock()
	st := d.score(peerID)
	st.blocks += uint64(blocks)
	st.seconds += elapsed.Seconds()
}

func (d *Downloader) penalize(peerID string, isMaster bool) {
	d.lock.Lock()
	st := d.score(peerID)
	st.strikes++
	copied := make(map[string]peerStat, len(d.scores))
	for id, s := range d.scores {
		copied[id] = *s
	}
	peerCount := len(d.peers)
	conn := d.peers[peerID]
	drop := dropPeer(peerID, copied, peerCount, isMaster)
	d.lock.Unlock()
	if drop && conn != nil {
		d.log.Info("disconnecting slow peer %s", peerID)
		conn.peer.DisconnectPeer("slow peer")
	}
}

func (d *Downloader) score(peerID string) *peerStat {
	st := d.scores[peerID]
	if st == nil {
		st = &peerStat{}
		d.scores[peerID] = st
	}
	return st
}

// getSyncInfo gets sync information of the current session.
func (d *Downloader) getSyncInfo(info *SyncInfo) {
	d.lock.RLock()
	defer d.lock.RUnlock()

	info.Status = d.getReadableStatus()
	if d.syncStatus != statusFetching {
		return
	}

	info.Duration = fmt.Sprintf("%.2f", time.Now().Sub(d.tm.startTime).Seconds())
	info.StartNum = d.tm.fromNo
	info.Amount = d.tm.toNo - d.tm.fromNo + 1
	info.Downloaded = d.tm.downloadedNum
}

// Synchronise try to sync with remote peer.
func (d *Downloader) Synchronise(id string, head common.Hash) error {
	// Make sure only one routine can pass at once
	d.lock.Lock()

	if d.stopped {
		d.lock.Unlock()
		return errReceivedQuitMsg
	}
	if d.syncStatus != statusNone {
		d.lock.Unlock()
		return ErrIsSynchronising
	}

	d.syncStatus = statusPreparing
	d.cancelCh = make(chan struct{})
	d.masterPeer = id
	p, ok := d.peers[id]
	if !ok {
		close(d.cancelCh)
		d.syncStatus = statusNone
		d.lock.Unlock()
		return errPeerNotFound
	}
	// Count this session before releasing the lock. Terminate sets stopped
	// under the same lock and then waits, so it cannot miss a session that
	// is about to start, and it cannot return while this one is still running.
	d.sessionWG.Add(1)
	d.lock.Unlock()
	defer d.sessionWG.Done()

	err := d.doSynchronise(p, head)

	d.lock.Lock()
	d.syncStatus = statusNone
	//d.sessionWG.Wait()
	d.cancelCh = nil
	d.lock.Unlock()

	return err
}

// td *big.Int, localTD *big.Int
func (d *Downloader) doSynchronise(conn *peerConn, head common.Hash) (err error) {
	d.log.Debug("Downloader.doSynchronise start, masterID: %s", d.masterPeer)
	event.BlockDownloaderEventManager.Fire(event.DownloaderStartEvent)
	defer func() {
		if err != nil {
			d.log.Info("download end with failed, err=%s", err)
			event.BlockDownloaderEventManager.Fire(event.DownloaderFailedEvent)
		} else {
			d.log.Debug("download end success")
			event.BlockDownloaderEventManager.Fire(event.DownloaderDoneEvent)
		}
	}()

	latest, err := d.fetchHeight(conn)
	if err != nil {
		conn.peer.DisconnectPeer("peerDownload anormaly")
		return err
	}
	height := latest.Height

	ancestor, err := d.findCommonAncestorHeight(conn, height)
	if err != nil {
		conn.peer.DisconnectPeer("peerDownload anormaly")
		return err
	}

	localHeight := d.chain.CurrentBlock().Header.Height
	d.log.Info("syncing shard chain: local height %d, peer target %d. SCDO Classic starts at fork genesis height %d (full sync of blocks after the fork, not a snapshot)", localHeight, height, common.ScdoForkHeight)
	d.log.Debug("Downloader.doSynchronise start task manager from height=%d, target height=%d master=%s", ancestor, height, d.masterPeer)
	tm := newTaskMgr(d, d.masterPeer, conn, ancestor+1, height, localHeight, nil, nil)
	d.tm = tm

	d.lock.Lock()
	d.syncStatus = statusFetching
	d.acceptPeers = true
	sessionWG := new(sync.WaitGroup)
	d.activeWG = sessionWG
	for _, pc := range d.peers {
		sessionWG.Add(1)
		go d.peerDownload(pc, tm, sessionWG)
	}
	d.lock.Unlock()

	sessionWG.Wait()
	d.lock.Lock()
	d.acceptPeers = false
	d.lock.Unlock()
	sessionWG.Wait()

	d.lock.Lock()
	d.syncStatus = statusCleaning
	d.lock.Unlock()
	tm.close()
	d.tm = nil
	d.log.Debug("Downloader.doSynchronise quit!")

	if tm.isDone() {
		return nil
	}

	return errSyncErr
}

// fetchHeight gets the latest head of peer
func (d *Downloader) fetchHeight(conn *peerConn) (*types.BlockHeader, error) {
	head, _ := conn.peer.Head()

	magic := rand2.Uint32()
	go conn.peer.RequestHeadersByHashOrNumber(magic, head, 0, 1, false)

	msg, err := conn.waitMsg(magic, BlockHeadersMsg, d.cancelCh)
	if err != nil {
		return nil, err
	}

	return verifyBlockHeadersMsg(msg, head)
}

func verifyBlockHeadersMsg(msg interface{}, head common.Hash) (*types.BlockHeader, error) {
	headers := msg.([]*types.BlockHeader)
	if len(headers) < 1 {
		return nil, errInvalidPacketReceived
	}

	if headers[0].Hash() != head {
		return nil, errHashNotMatch
	}

	return headers[0], nil
}

// findCommonAncestorHeight finds the common ancestor height
func (d *Downloader) findCommonAncestorHeight(conn *peerConn, height uint64) (uint64, error) {
	// Get the top height
	block := d.chain.CurrentBlock()
	localHeight := block.Header.Height

	top := getTop(localHeight, height)
	if top == 0 {
		return top, nil
	}

	// Compare the peer and local block head hash and return the ancestor height
	var cmpCount uint64
	maxFetchAncestry := getMaxFetchAncestry(top)
	for {
		if d.cancelled() {
			return 0, errReceivedQuitMsg
		}
		localTop := top - uint64(cmpCount)

		fetchCount := getFetchCount(maxFetchAncestry, cmpCount)
		if fetchCount == 0 {
			return 0, errMaxForkAncestor
		}

		// Get peer block headers
		headers, err := d.getPeerBlockHeaders(conn, localTop, fetchCount)
		if err != nil {
			return 0, err
		}

		cmpCount += uint64(len(headers))

		// Is ancenstor found
		found, cmpHeight, err := d.isAncenstorFound(headers)
		if err != nil {
			return 0, err
		}
		if found {
			return cmpHeight, nil
		}
	}
}

func getTop(localHeight, height uint64) uint64 {
	var top uint64

	if localHeight <= height {
		top = localHeight
	} else {
		top = height
	}

	return top
}

// getMaxFetchAncestry gets maximum chain reorganisation
func getMaxFetchAncestry(top uint64) uint64 {
	var maxFetchAncestry uint64

	if top >= uint64(MaxForkAncestry) {
		maxFetchAncestry = uint64(MaxForkAncestry)
	} else {
		maxFetchAncestry = top + 1
	}

	return maxFetchAncestry
}

func getFetchCount(maxFetchAncestry, cmpCount uint64) uint64 {
	var fetchCount uint64

	if (maxFetchAncestry - cmpCount) >= uint64(MaxHeaderFetch) {
		fetchCount = uint64(MaxHeaderFetch)
	} else {
		fetchCount = maxFetchAncestry - cmpCount
	}

	return fetchCount
}

func (d *Downloader) getPeerBlockHeaders(conn *peerConn, localTop, fetchCount uint64) ([]*types.BlockHeader, error) {
	magic := rand2.Uint32()
	go conn.peer.RequestHeadersByHashOrNumber(magic, common.EmptyHash, localTop, int(fetchCount), true)

	msg, err := conn.waitMsg(magic, BlockHeadersMsg, d.cancelCh)
	if err != nil {
		return nil, err
	}

	headers := msg.([]*types.BlockHeader)
	if len(headers) == 0 {
		return nil, errInvalidAncestor
	}

	return headers, nil
}

func (d *Downloader) isAncenstorFound(headers []*types.BlockHeader) (bool, uint64, error) {
	for i := 0; i < len(headers); i++ {
		cmpHeight := headers[i].Height
		localHash, err := d.chain.GetStore().GetBlockHash(cmpHeight)
		if err != nil {
			return false, 0, err
		}

		if localHash == headers[i].Hash() {
			return true, cmpHeight, nil
		}
	}

	return false, 0, nil
}

// RegisterPeer add peer to download routine
func (d *Downloader) RegisterPeer(peerID string, peer Peer) {
	d.lock.Lock()
	defer d.lock.Unlock()

	newConn := newPeerConn(peer, peerID, d.log)
	d.peers[peerID] = newConn

	if d.acceptPeers && d.tm != nil && d.activeWG != nil {
		d.activeWG.Add(1)
		go d.peerDownload(newConn, d.tm, d.activeWG)
	}
}

// UnRegisterPeer remove peer from download routine
func (d *Downloader) UnRegisterPeer(peerID string) {
	d.lock.Lock()
	defer d.lock.Unlock()

	if peerConn, ok := d.peers[peerID]; ok {
		peerConn.close()
		delete(d.peers, peerID)
	}
}

// DeliverMsg called by scdoprotocol to deliver received msg from network
func (d *Downloader) DeliverMsg(peerID string, msg *p2p.Message) {
	d.lock.Lock()
	peerConn, ok := d.peers[peerID]
	d.lock.Unlock()

	if ok {
		peerConn.deliverMsg(msg.Code, msg)
	}
}

// Cancel cancels current session.
func (d *Downloader) Cancel() {
	d.lock.Lock()
	defer d.lock.Unlock()
	d.closeCancelLocked()
}

// Terminate ends the current session and refuses any session that starts later.
// Shutdown calls this so a stuck sync cannot hold the process open.
func (d *Downloader) Terminate() {
	d.lock.Lock()
	d.stopped = true
	d.acceptPeers = false
	d.closeCancelLocked()
	peers := make([]*peerConn, 0, len(d.peers))
	for _, p := range d.peers {
		peers = append(peers, p)
	}
	tm := d.tm
	d.lock.Unlock()
	for _, p := range peers {
		if p == nil {
			continue
		}
		p.stopSession()
		p.close()
	}
	if tm != nil {
		tm.signalQuit()
	}
	d.sessionWG.Wait()
}

func (d *Downloader) closeCancelLocked() {
	if d.log != nil {
		d.log.Debug("Downloader.Cancel called")
	}
	if d.cancelCh == nil {
		return
	}
	select {
	case <-d.cancelCh:
	default:
		close(d.cancelCh)
	}
}

// cancelled reports that the current session was asked to stop.
func (d *Downloader) isStopped() bool {
	if d == nil {
		return false
	}
	d.lock.RLock()
	stopped := d.stopped
	d.lock.RUnlock()
	return stopped
}

func (d *Downloader) cancelled() bool {
	if d == nil {
		return false
	}
	d.lock.RLock()
	stopped := d.stopped
	ch := d.cancelCh
	d.lock.RUnlock()
	if stopped {
		return true
	}
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// peerDownload runs a header fetcher and a body fetcher for one peer.
// Headers stay ahead of bodies, and several peers download bodies at once.
// Body batches stay in flight while earlier blocks are verified and written.
func (d *Downloader) peerDownload(conn *peerConn, tm *taskMgr, sessionWG *sync.WaitGroup) {
	defer sessionWG.Done()

	d.log.Debug("Downloader.peerDownload start. peerID=%s masterID=%s", conn.peerID, d.masterPeer)
	isMaster := conn.peerID == d.masterPeer
	peerID := conn.peerID
	conn.bindSession()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := d.fetchHeaders(conn, tm); err != nil && !tm.isDone() {
			d.log.Debug("header fetch stopped peer=%s err=%s", conn.peerID, err)
			conn.stopSession()
			if isMaster {
				d.Cancel()
			}
		}
	}()
	go func() {
		defer wg.Done()
		if err := d.fetchBodies(conn, tm); err != nil && !tm.isDone() {
			d.log.Debug("body fetch stopped peer=%s err=%s", conn.peerID, err)
			conn.stopSession()
			if isMaster {
				d.Cancel()
			}
		}
	}()
	wg.Wait()

	tm.onPeerQuit(peerID)
	if isMaster || tm.isDone() {
		d.Cancel()
	}
	d.log.Debug("Downloader.peerDownload end. peerID=%s masterID=%s", conn.peerID, d.masterPeer)
}

func (d *Downloader) fetchHeaders(conn *peerConn, tm *taskMgr) error {
	for !tm.isDone() {
		if d.sessionStopped(conn) {
			return errReceivedQuitMsg
		}
		startNo, amount := tm.getReqHeaderInfo(conn)
		if amount <= 0 {
			if err := d.idlePeer(conn, tm); err != nil {
				return err
			}
			continue
		}
		magic := rand2.Uint32()
		d.log.Debug("request header by number. start=%d, amount=%d, magic=%d, id=%s", startNo, amount, magic, conn.peerID)
		go conn.peer.RequestHeadersByHashOrNumber(magic, common.Hash{}, startNo, amount, false)

		msg, err := conn.waitMsg(magic, BlockHeadersMsg, d.cancelCh)
		if err != nil {
			d.log.Debug("peerDownload waitMsg BlockHeadersMsg err! err=%s, magic=%d, id=%s", err, magic, conn.peerID)
			d.penalize(conn.peerID, conn.peerID == d.masterPeer)
			return err
		}
		headers := msg.([]*types.BlockHeader)
		if err = tm.deliverHeaderMsg(conn.peerID, headers); err != nil {
			d.log.Warn("peerDownload deliverHeaderMsg err! %s", err)
			return err
		}
	}
	return nil
}

func (d *Downloader) fetchBodies(conn *peerConn, tm *taskMgr) error {
	inflight := make(chan struct{}, maxBodyInflight)
	var wg sync.WaitGroup
	defer wg.Wait()

	for !tm.isDone() {
		if d.sessionStopped(conn) {
			return errReceivedQuitMsg
		}
		for _, id := range tm.drainSlow() {
			d.penalize(id, id == d.masterPeer)
		}
		stop := conn.sessionStopCh()
		select {
		case inflight <- struct{}{}:
		case <-d.cancelCh:
			return errReceivedQuitMsg
		case <-conn.quitCh:
			return errPeerQuit
		case <-stop:
			return errReceivedQuitMsg
		}
		startNo, amount := tm.getReqBlocks(conn)
		if amount <= 0 {
			<-inflight
			if err := d.idlePeer(conn, tm); err != nil {
				return err
			}
			continue
		}
		wg.Add(1)
		go func(startNo uint64, amount int) {
			defer wg.Done()
			defer func() { <-inflight }()
			d.fetchOneBody(conn, tm, startNo, amount)
		}(startNo, amount)
	}
	return nil
}

func (d *Downloader) fetchOneBody(conn *peerConn, tm *taskMgr, startNo uint64, amount int) {
	magic := rand2.Uint32()
	d.log.Debug("request block by number. start=%d, amount=%d, magic=%d, id=%s", startNo, amount, magic, conn.peerID)
	started := time.Now()
	go conn.peer.RequestBlocksByHashOrNumber(magic, common.Hash{}, startNo, amount)

	msg, err := conn.waitMsgTimeout(magic, BlocksMsg, d.cancelCh, bodyWaitTimeout)
	if err != nil {
		d.log.Debug("peerDownload waitMsg BlocksMsg err! err=%s", err)
		if !tm.isDone() {
			d.penalize(conn.peerID, conn.peerID == d.masterPeer)
		}
		return
	}
	blocks := msg.([]*types.Block)
	tm.deliverBlockMsg(conn.peerID, blocks)
	if len(blocks) > 0 {
		d.recordDelivery(conn.peerID, len(blocks), time.Since(started))
	}
}

func (d *Downloader) sessionStopped(conn *peerConn) bool {
	stop := conn.sessionStopCh()
	select {
	case <-d.cancelCh:
		return true
	case <-conn.quitCh:
		return true
	case <-stop:
		return true
	default:
		return false
	}
}

func (d *Downloader) idlePeer(conn *peerConn, tm *taskMgr) error {
	stop := conn.sessionStopCh()
	select {
	case <-d.cancelCh:
		if !tm.isDone() {
			conn.peer.DisconnectPeer("peerDownload anormaly")
		}
		return errReceivedQuitMsg
	case <-conn.quitCh:
		return errPeerQuit
	case <-stop:
		return errReceivedQuitMsg
	case <-time.After(peerIdleTime):
		return nil
	}
}

// processBlocks writes blocks to the blockchain.
// waiting is true when a cross-shard debt needs a source-shard header or more
// confirmations on that shard. The task manager leaves those blocks queued and retries.
func (d *Downloader) processBlocks(headInfos []*downloadInfo, ancestor uint64, localHeight uint64, localTD *big.Int, localBlocks []*types.Block, conn *peerConn) (waiting bool) {
	if d.cancelled() {
		return false
	}
	if len(headInfos) > 0 {
		d.log.Debug(" [%d] blocks will be processed into local database", len(headInfos))
	}
	for _, h := range headInfos {
		if d.cancelled() {
			return false
		}
		d.log.Debug("got block message and save it. height=%d, hash=%s", h.block.Header.Height, h.block.HeaderHash.Hex())
		// writeblock
		var txPool *core.Pool
		if pool := d.scdo.TxPool(); pool != nil {
			txPool = pool.Pool
		}
		started := time.Now()
		err := d.chain.WriteBlock(h.block, txPool)
		d.noteBlockWrite(time.Since(started))

		if err != nil && !errors.IsOrContains(err, core.ErrBlockAlreadyExists) {
			if isShardDataNotReady(err) {
				d.noteShardWait(err)
				d.log.Debug("queue block height=%d until the source shard is ready: %s", h.block.Header.Height, err)
				return true
			}
			d.clearShardWait()
			d.log.Error("failed to write block err=%s", err)
			// recover local blocks if localTotalDifficulty is larger than the synchronized total difficulty
			// if writeblock fails in the middle (the whole process not successfully completed), then we need to consider write back our localblocks
			// if localblock totaldifficult is larger than the break point's one. It means this sync attempt should be abonded
			// get local block

			if conn != nil && (errors.IsOrContains(err, consensus.ErrBlockNonceInvalid) || errors.IsOrContains(err, consensus.ErrBlockDifficultInvalid)) {
				conn.peer.DisconnectPeer("peerDownload anormaly")
			}
			d.Cancel()
			break
		}

		h.status = taskStatusProcessed
		d.clearShardWait()
	}
	return false
}

// waitingBlocks is how many headers may sit ahead of the last written block.
func (d *Downloader) waitingBlocks() uint64 {
	if d == nil {
		return maxBlocksWaiting
	}
	return waitingLimit(atomic.LoadInt64(&d.writeNS))
}

// noteBlockWrite keeps a smoothed write time so the header queue can shrink
// when the disk cannot keep up with the download.
func (d *Downloader) noteBlockWrite(elapsed time.Duration) {
	sample := elapsed.Nanoseconds()
	if sample < 0 {
		sample = 0
	}
	prev := atomic.LoadInt64(&d.writeNS)
	var next int64
	if prev == 0 {
		next = sample
	} else {
		next = (prev*3 + sample) / 4
	}
	atomic.StoreInt64(&d.writeNS, next)
}

// noteShardWait records why a block is queued. The numbers come from the
// verifier message "wanted is N, actual is M" and are not a new consensus rule.
func (d *Downloader) noteShardWait(err error) {
	reason := "source-shard-header"
	var need, have uint64
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if errors.IsOrContains(err, types.ErrNotEnoughConfirmations) {
		reason = "source-shard-confirmations"
		need, have = confirmationCounts(msg)
	} else if strings.Contains(msg, "failed to get tx") || strings.Contains(msg, "wait for msg reqid=") {
		reason = "source-shard-tx"
	}
	d.lock.Lock()
	d.waitReason = reason
	d.waitNeed = need
	d.waitHave = have
	d.lock.Unlock()
}

func (d *Downloader) clearShardWait() {
	d.lock.Lock()
	d.waitReason = ""
	d.waitNeed = 0
	d.waitHave = 0
	d.lock.Unlock()
}

// WaitDetail is the operator-facing line for a queued cross-shard dependency.
func (d *Downloader) WaitDetail() string {
	d.lock.RLock()
	defer d.lock.RUnlock()
	switch d.waitReason {
	case "source-shard-confirmations":
		if d.waitNeed > 0 {
			return fmt.Sprintf("source shard has %d/%d confirmations; waiting, not rejecting the block", d.waitHave, d.waitNeed)
		}
		return "source shard confirmations are not ready; waiting, not rejecting the block"
	case "source-shard-header":
		return "source shard header is not synced yet; waiting, not rejecting the block"
	case "source-shard-tx":
		return "source shard transaction is not available from peers yet; waiting, not rejecting the block"
	default:
		return ""
	}
}

func confirmationCounts(msg string) (need, have uint64) {
	const wanted = "wanted is "
	const actual = "actual is "
	if i := strings.Index(msg, wanted); i >= 0 {
		fmt.Sscanf(msg[i:], "wanted is %d", &need)
	}
	if i := strings.Index(msg, actual); i >= 0 {
		fmt.Sscanf(msg[i:], "actual is %d", &have)
	}
	return need, have
}

// isShardDataNotReady reports whether a block write failed only because the
// source shard has not caught up (missing header, no peer to ask, a peer that
// has not stored the tx yet, an ODR timeout, or fewer than the required
// confirmations). Those blocks stay queued. A real validation failure does not match.
func isShardDataNotReady(err error) bool {
	return types.ShardDataNotReady(err)
}

// reverse the chain back to the common ancestor of local node and peer
// TODO: keep the blocks of local node after the common ancestor
func (d *Downloader) reverseBCstore(ancestor uint64) (uint64, *big.Int, []*types.Block, error) {
	localCurBlock := d.chain.CurrentBlock()
	localHeight := localCurBlock.Header.Height
	curHeight := localHeight
	localBlocks := make([]*types.Block, 0)
	bcStore := d.chain.GetStore()
	var localTD *big.Int
	var errTD error
	if localTD, errTD = bcStore.GetBlockTotalDifficulty(localCurBlock.HeaderHash); errTD != nil {
		return localHeight, localTD, localBlocks, errTD
	}
	for curHeight > ancestor {
		hash, err := bcStore.GetBlockHash(curHeight)
		d.log.Debug("reverse curHeight: %d, hash: %v", curHeight, hash)
		if err != nil {
			return localHeight, localTD, localBlocks, errors.NewStackedErrorf(err, "failed to get block hash by height %v", curHeight)
		}

		block, err := bcStore.GetBlock(hash)
		if err != nil {
			return localHeight, localTD, localBlocks, errors.NewStackedErrorf(err, "failed to get block by hash %v", hash)
		}

		// delete blockleaves
		d.chain.RemoveBlockLeaves(hash)

		// use last block as the temporary chain head
		err = d.updateHeadInfo(curHeight - 1)
		if err != nil {
			return localHeight, localTD, localBlocks, errors.NewStackedErrorf(err, "failed to update head info while reversing the chain")
		}

		// save the local blocks
		localBlocks = append([]*types.Block{block}, localBlocks...)

		// reinject the block objects to the pool
		d.scdo.TxPool().HandleChainReversed(block)
		d.scdo.DebtPool().HandleChainReversed(block)

		if err = bcStore.DeleteBlock(hash); err != nil {
			return localHeight, localTD, localBlocks, errors.NewStackedErrorf(err, "failed to delete block %v", block.HeaderHash)
		}

		// delete the block hash in canonical chain.
		_, err = bcStore.DeleteBlockHash(curHeight)
		if err != nil {
			return localHeight, localTD, localBlocks, errors.NewStackedErrorf(err, "failed to delete block hash by height %v", curHeight)
		}

		curHeight--
	}
	return localHeight, localTD, localBlocks, nil

}

func (d *Downloader) updateHeadInfo(height uint64) error {
	bcStore := d.chain.GetStore()

	// use the ancestor as currentBlock
	curHash, err := bcStore.GetBlockHash(height)
	if err != nil {
		return err
	}

	curBlock, err := bcStore.GetBlock(curHash)
	if err != nil {
		return err
	}

	// update head block hash
	err = bcStore.PutHeadBlockHash(curHash)
	if err != nil {
		return err
	}

	// update blockLeaves
	var currentTd *big.Int
	if currentTd, err = bcStore.GetBlockTotalDifficulty(curBlock.HeaderHash); err != nil {
		return err
	}
	blockIndex := core.NewBlockIndex(curBlock.HeaderHash, curBlock.Header.Height, currentTd)
	d.chain.AddBlockLeaves(blockIndex)
	d.chain.UpdateCurrentBlock(curBlock)
	d.log.Debug("update current block: %d, hash: %v", curBlock.Header.Height, curBlock.HeaderHash)

	return nil
}
