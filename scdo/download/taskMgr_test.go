/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package downloader

import (
	"bytes"
	"math/big"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/database"
	"github.com/scdoproject/go-scdo/log"

	"github.com/scdoproject/go-scdo/database/leveldb"
	"github.com/stretchr/testify/assert"
)

func Test_TaskMgr_NewPeerHeadInfo(t *testing.T) {
	p := newPeerHeadInfo()
	assert.Equal(t, p != nil, true)
	assert.Equal(t, len(p.headers), 0)
}

func Test_TaskMgr_NewTaskMgrAndRun(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()

	d := newTestDownloader(db)
	taskMgr := newTestTaskMgr(d, db)
	defer taskMgr.close()

	assert.Equal(t, taskMgr != nil, true)
	assert.Equal(t, taskMgr.log, d.log)
	assert.Equal(t, taskMgr.downloader, d)
	assert.Equal(t, taskMgr.fromNo, from)
	assert.Equal(t, taskMgr.toNo, to)
	assert.Equal(t, taskMgr.toNo, to)
	assert.Equal(t, taskMgr.curNo, from)
	assert.Equal(t, taskMgr.downloadedNum, uint64(0))
	assert.Equal(t, taskMgr.masterPeer, masterPeer)

	assert.Equal(t, len(taskMgr.peersHeaderMap), 0)
	assert.Equal(t, len(taskMgr.downloadInfoList), 0)

}

func Test_TaskMgr_GetWaitProcessingBlocks(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()

	d := newTestDownloader(db)
	taskMgr := newTestTaskMgr(d, db)
	defer taskMgr.onPeerQuit(masterPeer)
	defer taskMgr.close()

	// empty block
	di := taskMgr.getWaitProcessingBlocks()
	assert.Equal(t, len(di), 0)
	assert.Equal(t, taskMgr.curNo, uint64(0))
	assert.Equal(t, taskMgr.isDone(), false)

	// add one block that needs to be processed
	taskMgr.downloadInfoList = []*downloadInfo{newDownloadInfo(1, taskStatusWaitProcessing)}
	di = taskMgr.getWaitProcessingBlocks()
	assert.Equal(t, len(di), 1)
	assert.Equal(t, taskMgr.curNo, uint64(1))
	assert.Equal(t, taskMgr.isDone(), true)

	// add another one block that needs to be processed
	taskMgr.downloadInfoList = append(taskMgr.downloadInfoList, newDownloadInfo(2, taskStatusWaitProcessing))
	di = taskMgr.getWaitProcessingBlocks()
	assert.Equal(t, len(di), 1)
	assert.Equal(t, taskMgr.curNo, uint64(2))
	assert.Equal(t, taskMgr.isDone(), false)
}

func Test_TaskMgr_GetReqHeaderInfo(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()

	d := newTestDownloader(db)
	taskMgr := newTestTaskMgr(d, db)
	defer taskMgr.onPeerQuit(masterPeer)
	defer taskMgr.close()

	// case 1: init
	pc := testTaskMgrPeerConn("testPeerID")
	startNo, amount := taskMgr.getReqHeaderInfo(pc)
	assert.Equal(t, startNo, uint64(0))
	assert.Equal(t, amount, 1)

	// case 2: MaxHeaderFetch
	taskMgr.peersHeaderMap["testPeerID"] = newPeerHeadInfos(2)
	startNo, amount = taskMgr.getReqHeaderInfo(pc)
	assert.Equal(t, startNo, uint64(3))
	assert.Equal(t, amount, MaxHeaderFetch)

	// case 3: master peer
	pc = testTaskMgrPeerConn("masterPeer")
	startNo, amount = taskMgr.getReqHeaderInfo(pc)
	assert.Equal(t, startNo, uint64(0))
	assert.Equal(t, amount, 1)
}

func Test_TaskMgr_GetReqBlocks(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()

	d := newTestDownloader(db)
	taskMgr := newTestTaskMgr(d, db)
	defer taskMgr.onPeerQuit(masterPeer)
	defer taskMgr.close()

	pc := testTaskMgrPeerConn("testPeerID")
	startNo, amount := taskMgr.getReqBlocks(pc)
	assert.Equal(t, startNo, uint64(0))
	assert.Equal(t, amount, 0)

	taskMgr.peersHeaderMap["testPeerID"] = newPeerHeadInfos(3)
	taskMgr.downloadInfoList = []*downloadInfo{newDownloadInfo(1, taskStatusIdle), newDownloadInfo(2, taskStatusIdle), newDownloadInfo(3, taskStatusIdle)}
	taskMgr.curNo = 0
	startNo, amount = taskMgr.getReqBlocks(pc)
	assert.Equal(t, startNo, uint64(0))
	assert.Equal(t, amount, 0)
}

func Test_TaskMgr_DeliverHeaderMsg(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()

	d := newTestDownloader(db)
	taskMgr := newTestTaskMgr(d, db)
	defer taskMgr.onPeerQuit(masterPeer)
	defer taskMgr.close()

	// case 1: headers is nil
	err := taskMgr.deliverHeaderMsg(masterPeer, nil)
	assert.Equal(t, err, nil)

	// case 2: errMasterHeadersNotMatch
	err = taskMgr.deliverHeaderMsg(masterPeer, newTestBlockHeaders())
	assert.Equal(t, err, errMasterHeadersNotMatch)

	// case 3: errHeadInfoNotFound
	taskMgr.downloadInfoList = []*downloadInfo{newDownloadInfo(1, taskStatusIdle)}
	err = taskMgr.deliverHeaderMsg(masterPeer, newTestBlockHeaders())
	assert.Equal(t, err, errHeadInfoNotFound)

	// case 3: ok
	taskMgr.peersHeaderMap[masterPeer] = newPeerHeadInfos(1)
	taskMgr.downloadInfoList = []*downloadInfo{newDownloadInfo(1, taskStatusIdle)}
	err = taskMgr.deliverHeaderMsg(masterPeer, newTestBlockHeaders())
	assert.Equal(t, err, nil)
}

func Test_TaskMgr_DeliverBlockMsg(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()

	d := newTestDownloader(db)
	taskMgr := newTestTaskMgr(d, db)
	defer taskMgr.onPeerQuit(masterPeer)
	defer taskMgr.close()

	// case 1: not block
	taskMgr.deliverBlockMsg(masterPeer, nil)
	assert.Equal(t, taskMgr.downloadedNum, uint64(0))

	// case: headInfo.peerID != peerID
	taskMgr.downloadInfoList = []*downloadInfo{newDownloadInfo(0, taskStatusIdle), newDownloadInfo(1, taskStatusIdle)}
	taskMgr.deliverBlockMsg(masterPeer, []*types.Block{taskMgr.downloadInfoList[0].block})
	assert.Equal(t, taskMgr.downloadInfoList[0].status, taskStatusIdle)
	assert.Equal(t, taskMgr.downloadInfoList[1].status, taskStatusIdle)
	assert.Equal(t, taskMgr.downloadedNum, uint64(0))

	// case 3: ok
	taskMgr.downloadInfoList[0].block.Header.Height = 0
	taskMgr.downloadInfoList[1].block.Header.Height = 1
	taskMgr.downloadInfoList[0].header = taskMgr.downloadInfoList[0].block.Header
	taskMgr.downloadInfoList[1].header = taskMgr.downloadInfoList[1].block.Header
	taskMgr.deliverBlockMsg("peerID", []*types.Block{taskMgr.downloadInfoList[0].block, taskMgr.downloadInfoList[1].block})
	assert.Equal(t, taskMgr.downloadInfoList[0].status, taskStatusWaitProcessing)
	assert.Equal(t, taskMgr.downloadInfoList[1].status, taskStatusWaitProcessing)
	assert.Equal(t, taskMgr.downloadedNum, uint64(2))
}

var (
	masterPeer = "masterPeer"
	from       = uint64(0)
	to         = uint64(0)
)

func newTestTaskMgr(d *Downloader, db database.Database) *taskMgr {
	taskMgr := newTaskMgr(d, masterPeer, nil, from, to, uint64(0), nil, nil)

	return taskMgr
}

func newDownloadInfo(height uint64, status int) *downloadInfo {
	return &downloadInfo{
		header: newTestBlockHeaderWithHeight(height),
		block:  newTestBlocks()[0],
		peerID: "peerID",
		status: status,
	}
}

func testTaskMgrPeerConn(peerID string) *peerConn {
	var peer TestDownloadPeer
	pc := newPeerConn(peer, peerID, nil)

	return pc
}

func newPeerHeadInfos(num int) *peerHeadInfo {
	p := newPeerHeadInfo()

	for i := 0; i < num; i++ {
		p.headers[uint64(i)] = newTestBlockHeaderWithHeight(uint64(i + 1))
	}
	p.maxNo = uint64(num)

	return p
}

func TestSmoothSyncRateUsesRecentWindowOnly(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	// A 15s opening burst must not become the ETA, even if a previous
	// average was very high.
	short := []heightSample{
		{at: base, height: 1000},
		{at: base.Add(15 * time.Second), height: 5000},
	}
	rate, kept := smoothSyncRate(short, 9999, progressWindow)
	assert.Equal(t, float64(0), rate)
	assert.Equal(t, 2, len(kept))

	// Once the window is long enough, the rate is that window only.
	minute := []heightSample{
		{at: base, height: 0},
		{at: base.Add(time.Minute), height: 60},
	}
	rate, _ = smoothSyncRate(minute, 9999, progressWindow)
	assert.InDelta(t, 60, rate, 0.01)
}

func TestWaitingLimitShrinksOnSlowDisk(t *testing.T) {
	assert.Equal(t, uint64(maxBlocksWaiting), waitingLimit(int64(5*time.Millisecond)))
	assert.Equal(t, uint64(maxBlocksWaitingSlow), waitingLimit(int64(slowBlockWrite)+1))
	assert.Equal(t, uint64(maxBlocksWaiting), (*Downloader)(nil).waitingBlocks())
}

func TestSmoothSyncRateDropsOldSamples(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	samples := []heightSample{
		{at: base, height: 0},
		{at: base.Add(7 * time.Minute), height: 100},
		{at: base.Add(10 * time.Minute), height: 1900},
	}
	_, kept := smoothSyncRate(samples, 0, progressWindow)
	assert.Equal(t, 2, len(kept))
	assert.Equal(t, uint64(100), kept[0].height)
	assert.Equal(t, uint64(1900), kept[1].height)
}

func TestIsShardDataNotReady(t *testing.T) {
	confirmations := errors.NewStackedErrorf(types.ErrNotEnoughConfirmations, "invalid debt because not enough confirmed block number, wanted is %d, actual is %d", 120, 89)
	wrapped := errors.NewStackedError(confirmations, "failed to validate debt via verifier")
	assert.Equal(t, true, isShardDataNotReady(wrapped))
	assert.Equal(t, true, isShardDataNotReady(errors.NewStackedError(types.ErrHeaderNotReady, "leveldb: not found")))
	noPeers := errors.NewStackedError(types.ErrHeaderNotReady, "No peers found")
	assert.Equal(t, true, isShardDataNotReady(errors.NewStackedError(noPeers, "failed to get tx")))
	assert.Equal(t, false, isShardDataNotReady(errors.New("invalid parent hash")))
}

func TestTaskMgrDoesNotPreallocateSyncRange(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()

	d := newTestDownloader(db)
	tm := newTaskMgr(d, masterPeer, nil, 1, 6_000_000, 0, nil, nil)
	defer tm.close()

	if cap(tm.downloadInfoList) > maxBlocksWaiting {
		t.Fatalf("download window cap %d reserves the whole sync range", cap(tm.downloadInfoList))
	}
}

func TestSyncReleasesProcessedBlockMemory(t *testing.T) {
	const (
		n       = 2500
		payload = 16 * 1024
		batch   = 100
	)
	lg := log.GetLogger("download-mem")
	d := &Downloader{log: lg, peers: make(map[string]*peerConn)}
	tm := &taskMgr{
		log:              lg,
		downloader:       d,
		fromNo:           1,
		toNo:             uint64(n),
		curNo:            1,
		listBase:         1,
		masterPeer:       masterPeer,
		peersHeaderMap:   map[string]*peerHeadInfo{masterPeer: newPeerHeadInfo()},
		downloadInfoList: make([]*downloadInfo, 0, 32),
		quitCh:           make(chan struct{}),
		procCh:           make(chan struct{}, 1),
	}

	body := bytes.Repeat([]byte("B"), payload)
	before := heapAlloc()
	for base := 1; base <= n; base += batch {
		count := batch
		if base+count-1 > n {
			count = n - base + 1
		}
		headers := make([]*types.BlockHeader, 0, count)
		blocks := make([]*types.Block, 0, count)
		for i := 0; i < count; i++ {
			height := uint64(base + i)
			extra := append([]byte(nil), body...)
			header := &types.BlockHeader{
				Height:          height,
				ExtraData:       extra,
				Difficulty:      big.NewInt(1),
				CreateTimestamp: big.NewInt(1),
			}
			headers = append(headers, header)
			blocks = append(blocks, &types.Block{Header: header})
		}
		if err := tm.deliverHeaderMsg(masterPeer, headers); err != nil {
			t.Fatal(err)
		}
		for _, info := range tm.downloadInfoList {
			if info != nil && info.status == taskStatusIdle {
				info.peerID = masterPeer
			}
		}
		tm.deliverBlockMsg(masterPeer, blocks)
		got := tm.getWaitProcessingBlocks()
		if len(got) != count {
			t.Fatalf("batch at %d processed %d, want %d", base, len(got), count)
		}
		for _, info := range got {
			info.status = taskStatusProcessed
		}
		tm.releaseProcessed()
		if len(tm.downloadInfoList) > maxBlocksWaiting {
			t.Fatalf("download window grew to %d entries", len(tm.downloadInfoList))
		}
		if cap(tm.downloadInfoList) > maxBlocksWaiting {
			t.Fatalf("download window cap %d", cap(tm.downloadInfoList))
		}
	}
	if len(tm.downloadInfoList) != 0 {
		t.Fatalf("retained %d processed entries", len(tm.downloadInfoList))
	}
	for _, info := range tm.peersHeaderMap {
		if len(info.headers) != 0 {
			t.Fatalf("retained %d peer headers", len(info.headers))
		}
	}

	after := heapAlloc()
	// 2500 blocks * 16 KiB is about 40 MiB if the window keeps them.
	// Releasing the window must leave only a small residual.
	const limit = uint64(8 << 20)
	if after > before && after-before > limit {
		t.Fatalf("heap grew %d bytes after releasing %d blocks of %d bytes", after-before, n, payload)
	}
}

func heapAlloc() uint64 {
	runtime.GC()
	debug.FreeOSMemory()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

func TestWindowReanchorsWhenItPassesTheChainHead(t *testing.T) {
	tm := newWindowTask(110, 110, 1000)
	tm.downloadInfoList = []*downloadInfo{newDownloadInfo(110, taskStatusIdle)}
	tm.repairWindowLocked(101, false, time.Now())
	if tm.listBase != 101 || tm.curNo != 101 {
		t.Fatalf("window base %d cursor %d, want 101", tm.listBase, tm.curNo)
	}
	if len(tm.downloadInfoList) != 0 {
		t.Fatalf("headers past the chain were kept (%d)", len(tm.downloadInfoList))
	}
	if tm.pos(100) >= 0 {
		t.Fatal("chain head is still inside the window")
	}
}

func TestCursorReturnsToTheUnwrittenBlock(t *testing.T) {
	tm := newWindowTask(101, 105, 1000)
	tm.downloadInfoList = []*downloadInfo{
		newDownloadInfo(101, taskStatusIdle),
		newDownloadInfo(102, taskStatusIdle),
	}
	tm.repairWindowLocked(101, false, time.Now())
	if tm.listBase != 101 {
		t.Fatalf("window base %d, want 101", tm.listBase)
	}
	if tm.curNo != 101 {
		t.Fatalf("cursor %d stayed past the unwritten block", tm.curNo)
	}
	if len(tm.downloadInfoList) != 2 {
		t.Fatalf("in-range headers were dropped (%d)", len(tm.downloadInfoList))
	}
}

func TestStallResetsTheWindowToHeadPlusOne(t *testing.T) {
	tm := newWindowTask(101, 104, 1000)
	tm.masterPeer = masterPeer
	tm.downloader = &Downloader{log: tm.log, peers: map[string]*peerConn{}}
	tm.downloadInfoList = []*downloadInfo{newDownloadInfo(101, taskStatusDownloading)}
	tm.peersHeaderMap = map[string]*peerHeadInfo{masterPeer: newPeerHeadInfo()}
	tm.repairWindowLocked(101, true, time.Now())
	if tm.listBase != 101 || tm.curNo != 101 || len(tm.downloadInfoList) != 0 {
		t.Fatalf("stall left window base %d cursor %d len %d", tm.listBase, tm.curNo, len(tm.downloadInfoList))
	}
	tm.peersHeaderMap = map[string]*peerHeadInfo{masterPeer: newPeerHeadInfo()}
	header := newTestBlockHeaderWithHeight(101)
	if err := tm.deliverHeaderMsg(masterPeer, []*types.BlockHeader{header}); err != nil {
		t.Fatal(err)
	}
	if tm.pos(101) != 0 || len(tm.downloadInfoList) != 1 {
		t.Fatalf("head+1 is outside the window after re-anchor, pos %d len %d", tm.pos(101), len(tm.downloadInfoList))
	}
	startNo, amount := tm.getReqHeaderInfo(testTaskMgrPeerConn(masterPeer))
	if startNo != 102 || amount <= 0 {
		t.Fatalf("next header request %d x %d, want start 102", startNo, amount)
	}
}

func TestMasterHeaderBelowTheWindowIsIgnored(t *testing.T) {
	tm := newWindowTask(3584143, 3584143, 9000000)
	err := tm.deliverHeaderMsg(masterPeer, []*types.BlockHeader{newTestBlockHeaderWithHeight(3584142)})
	if err != nil {
		t.Fatalf("stale header aborted the session: %v", err)
	}
	if len(tm.downloadInfoList) != 0 {
		t.Fatal("stale header was appended")
	}
}

func newWindowTask(base, cur, to uint64) *taskMgr {
	return &taskMgr{
		log:              log.GetLogger("download-window"),
		fromNo:           base,
		toNo:             to,
		curNo:            cur,
		listBase:         base,
		masterPeer:       masterPeer,
		peersHeaderMap:   map[string]*peerHeadInfo{},
		downloadInfoList: nil,
		quitCh:           make(chan struct{}),
		procCh:           make(chan struct{}, 1),
		lastChainAdvance: time.Now(),
	}
}

func newTestBlockHeaderWithHeight(height uint64) *types.BlockHeader {
	return &types.BlockHeader{
		PreviousBlockHash: common.StringToHash("PreviousBlockHash"),
		Creator:           common.EmptyAddress,
		StateHash:         common.StringToHash("StateHash"),
		TxHash:            common.StringToHash("TxHash"),
		Difficulty:        big.NewInt(1),
		Height:            height,
		CreateTimestamp:   big.NewInt(time.Now().Unix()),
		Witness:           common.CopyBytes([]byte("witness")),
		ExtraData:         common.CopyBytes([]byte("ExtraData")),
	}
}
