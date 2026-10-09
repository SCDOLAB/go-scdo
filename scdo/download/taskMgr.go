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

	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/log"
)

const (
	taskStatusIdle           = 0 // request task is not assigned
	taskStatusDownloading    = 1 // block is downloading
	taskStatusWaitProcessing = 2 // block is downloaded, needs to process
	taskStatusProcessed      = 3 // block is written to chain

	maxBlocksWaiting     = 4096 // headers kept ahead of the write cursor
	maxBlocksWaitingSlow = 64   // cap the queue when a block write is slower than 50 blk/s
	// bodySlowAfter returns a body request to the idle queue so a faster peer can take it.
	bodySlowAfter = 4 * time.Second

	// progressWindow is the only span used for the sync ETA.
	progressWindow = 3 * time.Minute
	// progressMinSpan ignores a short opening burst so the first fast
	// samples are not extrapolated across the rest of the chain.
	progressMinSpan = 45 * time.Second
	// slowBlockWrite is one block taking longer than 1/50s.
	slowBlockWrite = 20 * time.Millisecond

	// windowStallTimeout re-anchors a session that has stopped writing blocks.
	// The chain head is the source of truth: the window must start at head+1,
	// and a stuck cursor has to clear itself without a process restart.
	windowStallTimeout = 60 * time.Second
)

// heightSample is one sync-progress observation.
type heightSample struct {
	at     time.Time
	height uint64
}

var (
	errMasterHeadersNotMatch = errors.New("Master headers not match")
	errHeadInfoNotFound      = errors.New("Header info not found")
)

// downloadInfo header info for master peer
type downloadInfo struct {
	header     *types.BlockHeader
	block      *types.Block
	peerID     string
	status     int // block download status
	assignedAt time.Time
}

// peerHeadInfo header info for ordinary peer
type peerHeadInfo struct {
	headers map[uint64]*types.BlockHeader // block height => block header
	maxNo   uint64                        // max block height in headers
}

func newPeerHeadInfo() *peerHeadInfo {
	return &peerHeadInfo{
		headers: make(map[uint64]*types.BlockHeader),
	}
}

type taskMgr struct {
	downloader     *Downloader
	fromNo, toNo   uint64 // block number range [from, to]
	curNo          uint64 // the smallest block number need to recv
	downloadedNum  uint64
	recoverHeight  uint64
	recoverTD      *big.Int
	recoverBlocks  []*types.Block
	peersHeaderMap map[string]*peerHeadInfo // peer's header information
	// downloadInfoList is a sliding window of headers and bodies still in
	// flight. listBase is the block height of index 0. Processed entries are
	// dropped so a multi-million-block sync does not retain every block.
	downloadInfoList []*downloadInfo
	listBase         uint64

	masterPeer      string
	masterConn      *peerConn
	lock            sync.RWMutex
	quitCh          chan struct{}
	wg              sync.WaitGroup
	log             *log.ScdoLog
	startTime       time.Time
	procCh          chan struct{}
	lastProgress    time.Time
	lastWaitLog     time.Time
	progressSamples []heightSample
	smoothBPM       float64
	slowNote        []string

	// lastHead is the canonical height at lastChainAdvance. A stall is
	// "this height has not moved for windowStallTimeout".
	lastHead         uint64
	lastChainAdvance time.Time

	// Discards outside the window are one summary per minute, not one line per block.
	discardLogAt time.Time
	discardCount int
	discardMin   uint64
	discardMax   uint64
	discardLogs  int
}

const discardLogEvery = time.Minute

func newTaskMgr(d *Downloader, masterPeer string, conn *peerConn, from uint64, to uint64, localHeight uint64, localTD *big.Int, localBlocks []*types.Block) *taskMgr {
	t := &taskMgr{
		log:              d.log,
		downloader:       d,
		fromNo:           from,
		toNo:             to,
		curNo:            from,
		downloadedNum:    0,
		recoverHeight:    localHeight,
		recoverTD:        localTD,
		recoverBlocks:    localBlocks,
		masterPeer:       masterPeer,
		masterConn:       conn,
		startTime:        time.Now(),
		peersHeaderMap:   make(map[string]*peerHeadInfo),
		downloadInfoList: make([]*downloadInfo, 0, 64),
		listBase:         from,
		quitCh:           make(chan struct{}),
		procCh:           make(chan struct{}, 1),
		lastHead:         localHeight,
		lastChainAdvance: time.Now(),
	}
	t.wg.Add(1)
	go t.run()
	return t
}

func (t *taskMgr) run() {
	defer t.wg.Done()

loopOut:
	for {
		if t.downloader != nil && t.downloader.isStopped() {
			break
		}
		// The canonical head wins over the in-memory cursor. A discarded
		// body or a cursor that stepped past an unwritten height used to
		// leave the window one past the block the chain still needs.
		t.reanchorWindow(time.Now())
		results := t.getWaitProcessingBlocks()
		waiting := t.downloader.processBlocks(results, t.fromNo-1, t.recoverHeight, t.recoverTD, t.recoverBlocks, t.masterConn)
		// getWaitProcessingBlocks moves curNo before the write. Pull it
		// back to the first block that is still not in the chain, including
		// a hard write error that does not end the session.
		t.rewindUnprocessed()
		if waiting {
			if time.Since(t.lastWaitLog) > 10*time.Second {
				detail := t.downloader.WaitDetail()
				if detail == "" {
					detail = "source shard header or confirmations are not ready; waiting, not rejecting the block"
				}
				t.log.Info("%s; leaving blocks queued and retrying", detail)
				t.lastWaitLog = time.Now()
			}
			select {
			case <-time.After(2 * time.Second):
			case <-t.quitCh:
				break loopOut
			}
			continue
		}
		// Drop blocks already written, including a processed prefix left
		// behind when a later debt is still waiting on another shard.
		t.releaseProcessed()
		t.maybeFlushDiscard(time.Now())
		t.logProgress()
		if t.isDone() {
			t.downloader.Cancel()
		}

		select {
		case <-t.procCh:
		case <-time.After(200 * time.Millisecond):
		case <-t.quitCh:
			break loopOut
		}
	}
}

// rewindUnprocessed moves curNo back to the first block that is not written yet.
// getWaitProcessingBlocks advances curNo before the write, so a deferred debt
// validation must not skip those blocks.
func (t *taskMgr) rewindUnprocessed() {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.pullCursorToUnwrittenLocked()
	if len(t.downloadInfoList) == 0 {
		return
	}
	// The cursor walked off the end of an unwritten list. Park it on the
	// first height that still needs a body.
	if t.curNo > t.listBase+uint64(len(t.downloadInfoList)) {
		t.curNo = t.listBase
		t.pullCursorToUnwrittenLocked()
	}
}

// headHeight is the canonical chain height, when this session has a chain.
func (t *taskMgr) headHeight() (uint64, bool) {
	if t == nil || t.downloader == nil || t.downloader.chain == nil {
		return 0, false
	}
	block := t.downloader.chain.CurrentBlock()
	if block == nil || block.Header == nil {
		return 0, false
	}
	return block.Header.Height, true
}

// reanchorWindow keeps the download window on the next canonical block.
// listBase and curNo both move back to head+1 when the window has stepped
// past the chain, and a session with no new canonical block for
// windowStallTimeout drops its in-flight headers and bodies and asks again.
func (t *taskMgr) reanchorWindow(now time.Time) {
	head, ok := t.headHeight()
	if !ok {
		return
	}
	anchor := head + 1
	shardWait := t.downloader != nil && t.downloader.WaitDetail() != ""
	t.lock.Lock()
	defer t.lock.Unlock()
	// A test chain, or a session whose peer range does not cover the head,
	// must not have its cursor rewritten.
	if anchor < t.fromNo || anchor > t.toNo+1 {
		return
	}
	stalled := false
	if t.lastChainAdvance.IsZero() || head != t.lastHead {
		t.lastHead = head
		t.lastChainAdvance = now
	} else if now.Sub(t.lastChainAdvance) >= windowStallTimeout {
		stalled = !shardWait
		if shardWait {
			// The block is queued on another shard. Re-arm the timer so a
			// dependency wait does not wipe the window every minute.
			t.lastChainAdvance = now
		}
	}
	t.repairWindowLocked(anchor, stalled, now)
}

// repairWindowLocked applies anchor (canonical head+1). stalled clears the
// in-flight window even when the base is already on that height.
func (t *taskMgr) repairWindowLocked(anchor uint64, stalled bool, now time.Time) {
	if stalled || t.listBase > anchor {
		if t.log != nil {
			t.log.Warn("re-anchor download window to height %d (window was %d, cursor %d, chain %d)", anchor, t.listBase, t.curNo, anchor-1)
		}
		for _, info := range t.downloadInfoList {
			if info == nil {
				continue
			}
			info.header = nil
			info.block = nil
		}
		t.downloadInfoList = nil
		t.listBase = anchor
		t.curNo = anchor
		t.peersHeaderMap = make(map[string]*peerHeadInfo)
		if anchor < t.fromNo {
			t.fromNo = anchor
		}
		t.lastHead = anchor - 1
		t.lastChainAdvance = now
		return
	}
	if t.listBase < anchor {
		t.dropBeforeAnchorLocked(anchor)
	}
	if t.curNo < anchor {
		t.curNo = anchor
	}
	t.pullCursorToUnwrittenLocked()
}

// dropBeforeAnchorLocked forgets headers below the next canonical block.
func (t *taskMgr) dropBeforeAnchorLocked(anchor uint64) {
	if anchor <= t.listBase {
		return
	}
	skip := anchor - t.listBase
	if skip > uint64(len(t.downloadInfoList)) {
		skip = uint64(len(t.downloadInfoList))
	}
	n := int(skip)
	for i := 0; i < n; i++ {
		if info := t.downloadInfoList[i]; info != nil {
			info.header = nil
			info.block = nil
			t.downloadInfoList[i] = nil
		}
	}
	t.listBase += uint64(n)
	if n > 0 {
		remain := t.downloadInfoList[n:]
		fresh := make([]*downloadInfo, len(remain))
		copy(fresh, remain)
		t.downloadInfoList = fresh
	}
	if t.listBase < anchor {
		t.listBase = anchor
	}
	if t.curNo < t.listBase {
		t.curNo = t.listBase
	}
	t.dropHeadersBeforeLocked(t.listBase)
}

// pullCursorToUnwrittenLocked sets curNo to the first height in the window
// that has not been written. Heights below curNo are otherwise never requested.
func (t *taskMgr) pullCursorToUnwrittenLocked() {
	for i, info := range t.downloadInfoList {
		if info != nil && info.status != taskStatusProcessed {
			t.curNo = t.listBase + uint64(i)
			return
		}
	}
	t.curNo = t.listBase + uint64(len(t.downloadInfoList))
}

// releaseProcessed drops a prefix of blocks that are already in the chain
// and forgets their header and body so the GC can take the RLP objects.
func (t *taskMgr) releaseProcessed() {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.releaseProcessedLocked()
}

func (t *taskMgr) releaseProcessedLocked() {
	if t.curNo < t.listBase {
		return
	}
	limit := int(t.curNo - t.listBase)
	if limit > len(t.downloadInfoList) {
		limit = len(t.downloadInfoList)
	}
	n := 0
	for n < limit {
		info := t.downloadInfoList[n]
		if info == nil || info.status != taskStatusProcessed {
			break
		}
		info.header = nil
		info.block = nil
		t.downloadInfoList[n] = nil
		n++
	}
	if n == 0 {
		return
	}
	t.listBase += uint64(n)
	remain := t.downloadInfoList[n:]
	// Copy into a new array. Reslicing would keep the old backing store,
	// and that store is what held every block since the session started.
	fresh := make([]*downloadInfo, len(remain))
	copy(fresh, remain)
	t.downloadInfoList = fresh
	t.dropHeadersBeforeLocked(t.curNo)
}

func (t *taskMgr) dropHeadersBeforeLocked(height uint64) {
	for _, headInfo := range t.peersHeaderMap {
		if headInfo == nil {
			continue
		}
		for no := range headInfo.headers {
			if no < height {
				delete(headInfo.headers, no)
			}
		}
	}
}

// pos is the index of height in downloadInfoList, or -1 when it is below the window.
func (t *taskMgr) pos(height uint64) int {
	if height < t.listBase {
		return -1
	}
	return int(height - t.listBase)
}

// pendingFrom returns the window starting at height.
func (t *taskMgr) pendingFrom(height uint64) []*downloadInfo {
	p := t.pos(height)
	if p < 0 {
		p = 0
	}
	if p >= len(t.downloadInfoList) {
		return nil
	}
	return t.downloadInfoList[p:]
}

func (t *taskMgr) logProgress() {
	if time.Since(t.lastProgress) < 15*time.Second {
		return
	}
	t.lock.RLock()
	cur := t.curNo
	to := t.toNo
	from := t.fromNo
	t.lock.RUnlock()
	if cur <= from {
		return
	}
	now := time.Now()
	t.lastProgress = now
	written := cur - 1
	t.progressSamples = append(t.progressSamples, heightSample{at: now, height: written})
	t.smoothBPM, t.progressSamples = smoothSyncRate(t.progressSamples, t.smoothBPM, progressWindow)
	eta := "unknown"
	if t.smoothBPM > 0 && to >= written {
		mins := float64(to-written) / t.smoothBPM
		eta = (time.Duration(mins * float64(time.Minute))).Truncate(time.Second).String()
	}
	peers := 0
	t.downloader.lock.RLock()
	peers = len(t.downloader.peers)
	t.downloader.lock.RUnlock()
	t.log.Info("sync progress: height %d / %d, %.1f blocks/min (%.1f blk/s), ETA %s, peers %d", written, to, t.smoothBPM, t.smoothBPM/60, eta, peers)
}

func (t *taskMgr) progressSnapshot() (current, highest uint64, blocksPerSec float64, eta string) {
	t.lock.RLock()
	defer t.lock.RUnlock()
	highest = t.toNo
	if t.curNo > 0 {
		current = t.curNo - 1
	}
	blocksPerSec = t.smoothBPM / 60
	eta = "unknown"
	if t.smoothBPM > 0 && highest >= current {
		mins := float64(highest-current) / t.smoothBPM
		eta = (time.Duration(mins * float64(time.Minute))).Truncate(time.Second).String()
	}
	return current, highest, blocksPerSec, eta
}

// smoothSyncRate returns blocks/min measured only inside the recent window.
// prevSmooth is ignored: an early burst must not keep pulling the ETA after
// the window has moved on. A span shorter than progressMinSpan returns 0 so
// the first samples are not extrapolated.
func smoothSyncRate(samples []heightSample, prevSmooth float64, window time.Duration) (float64, []heightSample) {
	if len(samples) == 0 {
		return 0, samples
	}
	cutoff := samples[len(samples)-1].at.Add(-window)
	start := 0
	for start+1 < len(samples) && !samples[start+1].at.After(cutoff) {
		start++
	}
	kept := make([]heightSample, len(samples)-start)
	copy(kept, samples[start:])

	if len(kept) < 2 {
		return 0, kept
	}
	dt := kept[len(kept)-1].at.Sub(kept[0].at)
	if dt < progressMinSpan {
		return 0, kept
	}
	minutes := dt.Minutes()
	if minutes <= 0 {
		return 0, kept
	}
	dh := float64(kept[len(kept)-1].height) - float64(kept[0].height)
	if dh < 0 {
		dh = 0
	}
	return dh / minutes, kept
}

// waitingLimit is how far ahead of the last written block the downloader may
// fetch. A slow disk keeps a short queue so compaction is not buried under
// a thousand buffered blocks.
func waitingLimit(writeNS int64) uint64 {
	if writeNS > int64(slowBlockWrite) {
		return maxBlocksWaitingSlow
	}
	return maxBlocksWaiting
}

func (t *taskMgr) notify() {
	select {
	case t.procCh <- struct{}{}:
	default:
	}
}

func (t *taskMgr) getWaitProcessingBlocks() []*downloadInfo {
	t.lock.Lock()
	defer t.lock.Unlock()

	startPos := 0
	if t.curNo > t.listBase {
		startPos = int(t.curNo - t.listBase)
	}
	if startPos > len(t.downloadInfoList) {
		startPos = len(t.downloadInfoList)
	}
	num := 0
	for startPos+num < len(t.downloadInfoList) {
		info := t.downloadInfoList[startPos+num]
		if info == nil || info.status != taskStatusWaitProcessing {
			break
		}
		num = num + 1
	}

	t.curNo = t.curNo + uint64(num)
	results := t.downloadInfoList[startPos : startPos+num]

	return results
}

func (t *taskMgr) signalQuit() {
	if t == nil {
		return
	}
	select {
	case <-t.quitCh:
	default:
		close(t.quitCh)
	}
}

func (t *taskMgr) close() {
	t.maybeFlushDiscard(time.Now().Add(discardLogEvery))
	t.signalQuit()
	t.wg.Wait()
}

// noteDiscardLocked counts a body that fell outside the window.
// The per-block line is debug. A warning with the count and height range
// is written at most once a minute.
func (t *taskMgr) noteDiscardLocked(height uint64, now time.Time) {
	if t.log != nil {
		t.log.Debug("discard block height %d outside download window starting at %d (len %d)", height, t.listBase, len(t.downloadInfoList))
	}
	if t.discardCount == 0 {
		t.discardMin = height
		t.discardMax = height
		t.discardLogAt = now
	} else {
		if height < t.discardMin {
			t.discardMin = height
		}
		if height > t.discardMax {
			t.discardMax = height
		}
	}
	t.discardCount++
	if t.discardCount > 1 && now.Sub(t.discardLogAt) >= discardLogEvery {
		t.flushDiscardLocked()
	}
}

func (t *taskMgr) maybeFlushDiscard(now time.Time) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.discardCount == 0 || now.Sub(t.discardLogAt) < discardLogEvery {
		return
	}
	t.flushDiscardLocked()
}

func (t *taskMgr) flushDiscardLocked() {
	if t.discardCount == 0 {
		return
	}
	if t.log != nil {
		t.log.Warn("discarded %d blocks outside the download window, heights %d-%d, window starts at %d", t.discardCount, t.discardMin, t.discardMax, t.listBase)
	}
	t.discardLogs++
	t.discardCount = 0
	t.discardMin = 0
	t.discardMax = 0
}

// getReqHeaderInfo gets header request information, returns the start block number and amount of headers.
func (t *taskMgr) getReqHeaderInfo(conn *peerConn) (uint64, int) {
	t.lock.Lock()
	defer t.lock.Unlock()

	headInfo, ok := t.peersHeaderMap[conn.peerID]
	if !ok {
		headInfo = newPeerHeadInfo()
		t.peersHeaderMap[conn.peerID] = headInfo
		t.log.Debug("getReqHeaderInfo. create headInfo for peer: %s", conn.peerID)
	}

	// try remove headers that already downloaded
	for no := range headInfo.headers {
		if no < t.curNo {
			delete(headInfo.headers, no)
		}
	}

	var startNo uint64
	if conn.peerID == t.masterPeer {
		startNo = t.listBase + uint64(len(t.downloadInfoList))
		// A cursor past the window end used to underflow this subtraction
		// and look like a full window, so no headers were ever requested again.
		if startNo > t.curNo && startNo-t.curNo > t.downloader.waitingBlocks() {
			return 0, 0
		}
	} else {
		startNo = headInfo.maxNo + 1
		if len(headInfo.headers) == 0 {
			headInfo.maxNo = 0
			startNo = t.curNo
		}
	}

	if startNo == t.toNo+1 || (startNo > t.curNo && startNo-t.curNo >= uint64(MaxHeaderFetch)) {
		// do not need to recv headers now.
		return 0, 0
	}

	amount := MaxHeaderFetch
	if uint64(MaxHeaderFetch) > (t.toNo + 1 - startNo) {
		amount = int(t.toNo - startNo + 1)
	}
	return startNo, amount
}

// getReqBlocks get block request information, returns the start block number and amount of blocks.
// should set masterHead.isDownloading = false, if send request msg error or download finished.
func (t *taskMgr) getReqBlocks(conn *peerConn) (uint64, int) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.reclaimSlowLocked(time.Now())

	headInfo, ok := t.peersHeaderMap[conn.peerID]
	if !ok || len(headInfo.headers) == 0 {
		return 0, 0
	}

	var startNo uint64
	var amount int
	// find the first block that not requested yet and exists in conn
	for _, masterHead := range t.pendingFrom(t.curNo) {
		if masterHead == nil || masterHead.status != taskStatusIdle {
			continue
		}
		curHeight := masterHead.header.Height
		peerHead, ok := headInfo.headers[curHeight]
		if !ok || peerHead.Hash() != masterHead.header.Hash() {
			continue
		}

		startNo = masterHead.header.Height
		masterHead.status = taskStatusDownloading
		masterHead.peerID = conn.peerID
		masterHead.assignedAt = time.Now()
		amount = 1
		break
	}

	if amount == 0 {
		return 0, 0
	}

	for _, masterHead := range t.pendingFrom(startNo + 1) {
		if masterHead == nil {
			break
		}
		if masterHead.status == taskStatusIdle {
			peerHead, ok := headInfo.headers[startNo+uint64(amount)]
			// if block is not found in headers or hash not match, then breaks the loop
			if !ok || peerHead.Hash() != masterHead.header.Hash() {
				break
			}

			if amount < MaxBlockFetch {
				amount++
				masterHead.status = taskStatusDownloading
				masterHead.peerID = conn.peerID
				masterHead.assignedAt = time.Now()
			} else {
				break
			}
			continue
		}
		break
	}

	return startNo, amount
}

// isDone returns if all blocks are downloaded
func (t *taskMgr) isDone() bool {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.log.Debug("task is done check cur:%d, target:%d", t.curNo, t.toNo)
	return t.curNo == t.toNo+1
}

// onPeerQuit needs to remove tasks assigned to peer
func (t *taskMgr) onPeerQuit(peerID string) {
	t.lock.Lock()
	defer t.lock.Unlock()

	for _, masterHead := range t.pendingFrom(t.curNo) {
		if masterHead != nil && masterHead.status == taskStatusDownloading && masterHead.peerID == peerID {
			masterHead.peerID = ""
			masterHead.status = taskStatusIdle
			masterHead.assignedAt = time.Time{}
		}
	}
}

// deliverHeaderMsg received header msg from peer.
func (t *taskMgr) deliverHeaderMsg(peerID string, headers []*types.BlockHeader) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	if len(headers) == 0 {
		t.log.Debug("get block header msg with empty header info")
		return nil
	}

	if peerID == t.masterPeer {
		lastNo := t.listBase + uint64(len(t.downloadInfoList))
		t.log.Debug("masterPeer deliverHeaderMsg. lastNo=%d fromNo:%d header.height:%d", lastNo, t.fromNo, headers[0].Height)
		if lastNo != headers[0].Height {
			// A body or header requested before the window moved is not a
			// bad peer. Drop it and let the fetcher ask from the new base.
			// Aborting here cancelled the session and the next request never
			// recovered while the cursor sat past the chain head.
			if headers[0].Height < t.listBase {
				t.log.Debug("ignore master headers at %d, window starts at %d", headers[0].Height, t.listBase)
				return nil
			}
			return errMasterHeadersNotMatch
		}
		for _, h := range headers {
			t.downloadInfoList = append(t.downloadInfoList, &downloadInfo{
				header: h,
				status: taskStatusIdle,
			})
		}
	}

	headInfo, ok := t.peersHeaderMap[peerID]
	if !ok {
		return errHeadInfoNotFound
	}

	for _, h := range headers {
		headInfo.headers[h.Height] = h
		if headInfo.maxNo < h.Height {
			headInfo.maxNo = h.Height
		}
	}

	return nil
}

// deliverBlockMsg received blocks msg from peer.
func (t *taskMgr) deliverBlockMsg(peerID string, blocks []*types.Block) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if len(blocks) == 0 {
		// An empty batch is not fatal. Return those heights to the idle queue
		// so another peer can serve them instead of aborting the sync.
		for _, headInfo := range t.downloadInfoList {
			if headInfo == nil || headInfo.peerID != peerID {
				continue
			}

			if headInfo.status == taskStatusDownloading {
				headInfo.status = taskStatusIdle
				headInfo.peerID = ""
				headInfo.assignedAt = time.Time{}
			}
		}
		return
	}

	toHeight := uint64(0)
	accepted := false

	for _, b := range blocks {
		idx := t.pos(b.Header.Height)
		if idx < 0 || idx >= len(t.downloadInfoList) {
			t.noteDiscardLocked(b.Header.Height, time.Now())
			continue
		}
		headInfo := t.downloadInfoList[idx]
		if headInfo.peerID != peerID {
			t.log.Debug("Received block from different peer, discard this block. headInfo.peerID=%s, peerID=%s", headInfo.peerID, peerID)
			continue
		}
		if headInfo.header != nil && b.Header != nil {
			got := b.Header.Hash()
			want := headInfo.header.Hash()
			if !got.Equal(want) {
				headInfo.status = taskStatusIdle
				headInfo.peerID = ""
				headInfo.assignedAt = time.Time{}
				t.slowNote = append(t.slowNote, peerID)
				continue
			}
		}

		headInfo.block = b
		headInfo.status = taskStatusWaitProcessing
		headInfo.assignedAt = time.Time{}
		t.downloadedNum++
		toHeight = b.Header.Height
		accepted = true
	}

	if !accepted {
		// Every block in the batch was outside the window. Those heights
		// were already assigned; put them back so the next needed block
		// is requested instead of sitting in "downloading" forever.
		t.releasePeerDownloadsLocked(peerID)
		return
	}

	if toHeight == t.toNo {
		t.notify()
		return
	}

	maxLen := len(t.downloadInfoList)
	for cur := toHeight + 1; cur <= t.toNo; cur++ {
		idx := t.pos(cur)
		if idx < 0 || idx >= maxLen {
			break
		}

		headInfo := t.downloadInfoList[idx]
		if headInfo == nil {
			break
		}
		if headInfo.peerID != peerID {
			break
		}
		// A block already written or queued for write must stay that way.
		// Demoting it to idle left a hole below curNo, and every later
		// delivery of that height was discarded as outside the window.
		if headInfo.status != taskStatusDownloading {
			break
		}

		headInfo.status = taskStatusIdle
		headInfo.assignedAt = time.Time{}
	}
	t.notify()
}

// releasePeerDownloadsLocked returns this peer's in-flight bodies to the idle queue.
func (t *taskMgr) releasePeerDownloadsLocked(peerID string) {
	for _, headInfo := range t.downloadInfoList {
		if headInfo == nil || headInfo.peerID != peerID || headInfo.status != taskStatusDownloading {
			continue
		}
		headInfo.status = taskStatusIdle
		headInfo.peerID = ""
		headInfo.assignedAt = time.Time{}
	}
}

func (t *taskMgr) reclaimSlowLocked(now time.Time) {
	for _, info := range t.downloadInfoList {
		if info == nil || info.status != taskStatusDownloading || info.assignedAt.IsZero() {
			continue
		}
		if now.Sub(info.assignedAt) < bodySlowAfter {
			continue
		}
		if info.peerID != "" {
			t.slowNote = append(t.slowNote, info.peerID)
		}
		info.status = taskStatusIdle
		info.peerID = ""
		info.assignedAt = time.Time{}
	}
}

func (t *taskMgr) drainSlow() []string {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.reclaimSlowLocked(time.Now())
	out := t.slowNote
	t.slowNote = nil
	return out
}
