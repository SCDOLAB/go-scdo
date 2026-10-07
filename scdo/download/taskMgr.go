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

	maxBlocksWaiting = 1024 // max blocks waiting to download

	// progressWindow is the span of the sync-rate moving average.
	progressWindow = 3 * time.Minute
	// progressAlpha is the weight of the newest window rate in the EMA.
	progressAlpha = 0.25
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
	header *types.BlockHeader
	block  *types.Block
	peerID string
	status int // block download status
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
	downloader       *Downloader
	fromNo, toNo     uint64 // block number range [from, to]
	curNo            uint64 // the smallest block number need to recv
	downloadedNum    uint64
	recoverHeight    uint64
	recoverTD        *big.Int
	recoverBlocks    []*types.Block
	peersHeaderMap   map[string]*peerHeadInfo // peer's header information
	downloadInfoList []*downloadInfo          // download process info

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
}

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
		downloadInfoList: make([]*downloadInfo, 0, to-from+1),
		quitCh:           make(chan struct{}),
		procCh:           make(chan struct{}, 1),
	}
	t.wg.Add(1)
	go t.run()
	return t
}

func (t *taskMgr) run() {
	defer t.wg.Done()

loopOut:
	for {
		results := t.getWaitProcessingBlocks()
		waiting := t.downloader.processBlocks(results, t.fromNo-1, t.recoverHeight, t.recoverTD, t.recoverBlocks, t.masterConn)
		if waiting {
			t.rewindUnprocessed()
			if time.Since(t.lastWaitLog) > 10*time.Second {
				t.log.Info("source shard header or confirmations are not ready; leaving blocks queued and retrying")
				t.lastWaitLog = time.Now()
			}
			select {
			case <-time.After(2 * time.Second):
			case <-t.quitCh:
				break loopOut
			}
			continue
		}
		t.logProgress()

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
	for i, info := range t.downloadInfoList {
		if info.status != taskStatusProcessed {
			t.curNo = t.fromNo + uint64(i)
			return
		}
	}
	t.curNo = t.fromNo + uint64(len(t.downloadInfoList))
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
	t.log.Info("sync progress: height %d / %d, %.1f blocks/min, ETA %s", written, to, t.smoothBPM, eta)
}

// smoothSyncRate returns blocks/min over the recent window, blended with prevSmooth
// so a speed change does not replace the printed ETA in one step. Samples older
// than the window are dropped, except the sample that anchors the left edge.
func smoothSyncRate(samples []heightSample, prevSmooth float64, window time.Duration) (float64, []heightSample) {
	if len(samples) == 0 {
		return prevSmooth, samples
	}
	cutoff := samples[len(samples)-1].at.Add(-window)
	start := 0
	for start+1 < len(samples) && !samples[start+1].at.After(cutoff) {
		start++
	}
	kept := make([]heightSample, len(samples)-start)
	copy(kept, samples[start:])

	smooth := prevSmooth
	if len(kept) >= 2 {
		dt := kept[len(kept)-1].at.Sub(kept[0].at).Minutes()
		if dt > 0 {
			dh := float64(kept[len(kept)-1].height) - float64(kept[0].height)
			if dh < 0 {
				dh = 0
			}
			windowBPM := dh / dt
			if prevSmooth > 0 {
				smooth = progressAlpha*windowBPM + (1-progressAlpha)*prevSmooth
			} else {
				smooth = windowBPM
			}
		}
	}
	return smooth, kept
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

	startPos := int(t.curNo - t.fromNo)
	num := 0
	for (startPos+num < len(t.downloadInfoList)) && (t.downloadInfoList[startPos+num].status == taskStatusWaitProcessing) {
		num = num + 1
	}

	t.curNo = t.curNo + uint64(num)
	results := t.downloadInfoList[startPos : startPos+num]

	return results
}

func (t *taskMgr) close() {
	select {
	case <-t.quitCh:
	default:
		close(t.quitCh)
	}
	t.wg.Wait()
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
		startNo = t.fromNo + uint64(len(t.downloadInfoList))
		if startNo-t.curNo > maxBlocksWaiting {
			return 0, 0
		}
	} else {
		startNo = headInfo.maxNo + 1
		if len(headInfo.headers) == 0 {
			headInfo.maxNo = 0
			startNo = t.curNo
		}
	}

	if startNo == t.toNo+1 || startNo-t.curNo >= uint64(MaxHeaderFetch) {
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

	headInfo, ok := t.peersHeaderMap[conn.peerID]
	if !ok || len(headInfo.headers) == 0 {
		return 0, 0
	}

	var startNo uint64
	var amount int
	// find the first block that not requested yet and exists in conn
	for _, masterHead := range t.downloadInfoList[t.curNo-t.fromNo:] {
		if masterHead.status != taskStatusIdle {
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
		amount = 1
		break
	}

	if amount == 0 {
		return 0, 0
	}

	for _, masterHead := range t.downloadInfoList[startNo+1-t.fromNo:] {
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

	for _, masterHead := range t.downloadInfoList[t.curNo-t.fromNo:] {
		if masterHead.status == taskStatusDownloading && masterHead.peerID == peerID {
			masterHead.peerID = ""
			masterHead.status = taskStatusIdle
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
		lastNo := t.fromNo + uint64(len(t.downloadInfoList))
		t.log.Debug("masterPeer deliverHeaderMsg. lastNo=%d fromNo:%d header.height:%d", lastNo, t.fromNo, headers[0].Height)
		if lastNo != headers[0].Height {
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
			if headInfo.peerID != peerID {
				continue
			}

			if headInfo.status == taskStatusDownloading {
				headInfo.status = taskStatusIdle
				headInfo.peerID = ""
			}
		}
		return
	}

	toHeight := uint64(0)

	for _, b := range blocks {
		idx := int(b.Header.Height - t.fromNo)
		if idx < 0 || idx >= len(t.downloadInfoList) {
			t.log.Warn("discard block height %d outside download range starting at %d (len %d)", b.Header.Height, t.fromNo, len(t.downloadInfoList))
			continue
		}
		headInfo := t.downloadInfoList[idx]
		if headInfo.peerID != peerID {
			t.log.Info("Received block from different peer, discard this block. headInfo.peerID=%s, peerID=%s", headInfo.peerID, peerID)
			continue
		}

		headInfo.block = b
		headInfo.status = taskStatusWaitProcessing
		t.downloadedNum++
		toHeight = b.Header.Height
	}

	if toHeight == t.toNo {
		t.notify()
		return
	}

	maxLen := len(t.downloadInfoList)
	for cur := toHeight + 1; cur <= t.toNo; cur++ {
		idx := int(cur - t.fromNo)
		if idx >= maxLen {
			break
		}

		headInfo := t.downloadInfoList[idx]
		if headInfo.peerID != peerID {
			break
		}

		headInfo.status = taskStatusIdle
	}
	t.notify()
}
