/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package downloader

import (
	"testing"
	"time"

	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/database/leveldb"
	"github.com/stretchr/testify/assert"
)

func TestDropPeerKeepsMasterAndSmallSets(t *testing.T) {
	stats := map[string]peerStat{
		"master": {strikes: 9},
		"slow":   {strikes: slowStrikeDrop},
		"fast":   {blocks: 1000, seconds: 1},
	}
	assert.Equal(t, false, dropPeer("master", stats, 4, true))
	assert.Equal(t, false, dropPeer("slow", stats, 2, false))
	assert.Equal(t, true, dropPeer("slow", stats, 3, false))
}

func TestDropPeerByRate(t *testing.T) {
	stats := map[string]peerStat{
		"fast": {blocks: 400, seconds: 1},
		"slow": {blocks: 64, seconds: 4},
	}
	assert.Equal(t, true, dropPeer("slow", stats, 3, false))
	assert.Equal(t, false, dropPeer("fast", stats, 3, false))
	stats["new"] = peerStat{blocks: 10, seconds: 1}
	assert.Equal(t, false, dropPeer("new", stats, 3, false))
}

func TestReclaimSlowBody(t *testing.T) {
	info := &downloadInfo{
		status:     taskStatusDownloading,
		peerID:     "slow",
		assignedAt: time.Now().Add(-bodySlowAfter - time.Second),
	}
	fresh := &downloadInfo{
		status:     taskStatusDownloading,
		peerID:     "fast",
		assignedAt: time.Now(),
	}
	tm := &taskMgr{downloadInfoList: []*downloadInfo{info, fresh}}
	tm.reclaimSlowLocked(time.Now())
	assert.Equal(t, taskStatusIdle, info.status)
	assert.Equal(t, "", info.peerID)
	assert.Equal(t, taskStatusDownloading, fresh.status)
	assert.Equal(t, []string{"slow"}, tm.slowNote)
}

func TestProgressReportsBlockRate(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()
	d := newTestDownloader(db)
	defer d.tm.close()

	d.syncStatus = statusFetching
	d.tm.smoothBPM = 120 * 60
	prog := d.Progress()
	assert.Equal(t, true, prog.Syncing)
	assert.Equal(t, uint64(2), prog.Highest)
	assert.InDelta(t, 120.0, prog.BlocksPerSec, 0.01)
	assert.NotEqual(t, "unknown", prog.ETA)
}

func TestConfirmationCounts(t *testing.T) {
	need, have := confirmationCounts("invalid debt because not enough confirmed block number, wanted is 120, actual is 89")
	assert.Equal(t, uint64(120), need)
	assert.Equal(t, uint64(89), have)
	need, have = confirmationCounts("no numbers here")
	assert.Equal(t, uint64(0), need)
	assert.Equal(t, uint64(0), have)
}

func TestNoteShardWaitReportsConfirmations(t *testing.T) {
	db, dispose := leveldb.NewTestDatabase()
	defer dispose()
	d := newTestDownloader(db)
	defer d.tm.close()

	err := errors.NewStackedErrorf(types.ErrNotEnoughConfirmations, "invalid debt because not enough confirmed block number, wanted is %d, actual is %d", 120, 89)
	d.noteShardWait(err)
	prog := d.Progress()
	assert.Equal(t, "source-shard-confirmations", prog.WaitingOn)
	assert.Equal(t, uint64(120), prog.ConfirmationsNeed)
	assert.Equal(t, uint64(89), prog.ConfirmationsHave)
	assert.Equal(t, "source shard has 89/120 confirmations; waiting, not rejecting the block", d.WaitDetail())

	d.noteShardWait(types.ErrHeaderNotReady)
	assert.Equal(t, "source-shard-header", d.Progress().WaitingOn)
	assert.Contains(t, d.WaitDetail(), "header is not synced yet")

	d.clearShardWait()
	assert.Equal(t, "", d.Progress().WaitingOn)
	assert.Equal(t, "", d.WaitDetail())
}
