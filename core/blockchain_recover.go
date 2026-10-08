/**
* @file
* @copyright defined in scdo/LICENSE
 */

package core

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/common/errors"
	"github.com/scdoproject/go-scdo/core/store"
	"github.com/scdoproject/go-scdo/core/types"
	"github.com/scdoproject/go-scdo/log"
)

// recoveryFlushInterval is the fastest recoveryPoint.json is replaced while
// blocks are imported. The in-memory marker still changes on every block.
// A clean shutdown fsyncs the file once; the import path does not.
//
// Four shard processes each rewrite this file every 10s. That is 24 atomic
// renames a minute. The write does not fsync, but ext4's ordered journal
// still posts one flush per rename, which is the steady ~0.4 flush/s left
// after per-block fsync was removed. nodes.json is once a minute and is a
// small part of the same count. LevelDB NoSync stays on, so block import
// does not add an fsync per block; memtable dumps are bursty, not this flat
// rate. The 10s window is the crash rollback bound, so it stays.
var recoveryFlushInterval = 10 * time.Second

// recoveryNow is the clock for that interval. Tests advance it.
var recoveryNow = time.Now

// recoveryFsyncs and recoveryDiskWrites count durability work done for the
// recovery file. Import must leave recoveryFsyncs unchanged.
var (
	recoveryFsyncs     int64
	recoveryDiskWrites int64
)

var rpLog = log.GetLogger("recoveryPoint")

// recoveryPoint is used for blockchain recovery in case of program crashed when write a block.
type recoveryPoint struct {
	WritingBlockHash           common.Hash // block hash that was writing to blockchain.
	WritingBlockHeight         uint64      // block height that was writing to blockchain.
	PreviousCanonicalBlockHash common.Hash // overwritten block hash once the writing block is new HEAD in canonical chain.
	PreviousHeadBlockHash      common.Hash // current HEAD block hash when write a block.
	LargerHeight               uint64      // Record the larger height block that to be removed from canonical chain.
	StaleHash                  common.Hash // Record the stale block hash for overwrite in canonical chain.

	file string

	// disk tracks the last snapshot. The mutex is not part of the JSON file.
	disk *recoveryDisk
}

// recoveryDisk is the on-disk debounce state for one recovery file.
type recoveryDisk struct {
	mu    sync.Mutex
	last  time.Time
	dirty bool
}

// loadRecoveryPoint loads a recovery point from the given file
func loadRecoveryPoint(file string) (*recoveryPoint, error) {
	rp := &recoveryPoint{
		file: file,
		disk: &recoveryDisk{},
	}

	if len(file) == 0 || !common.FileOrFolderExists(file) {
		return rp, nil
	}

	bytes, err := ioutil.ReadFile(file)
	if err != nil {
		rpLog.Error("Failed to read bytes from recovery point file, %v", err.Error())
		return rp, errors.NewStackedErrorf(err, "failed to read recovery point file %v", file)
	}

	if err = json.Unmarshal(bytes, rp); err != nil {
		rpLog.Warn("Failed to unmarshal encoded JSON data to recovery point info, file = %v, error = %v", file, err.Error())
		rp.flush()
	}

	return rp, nil
}

// recover recovers the most recent chain info from the recovery point
func (rp *recoveryPoint) recover(bcStore store.BlockchainStore) error {
	saved := true

	// recover the previous HEAD block hash.
	if !rp.PreviousHeadBlockHash.IsEmpty() {
		if err := bcStore.PutHeadBlockHash(rp.PreviousHeadBlockHash); err != nil {
			rpLog.Error("Failed to recover HEAD block hash, hash = %v, error = %v", rp.PreviousCanonicalBlockHash.Hex(), err.Error())
			return errors.NewStackedErrorf(err, "failed to put HEAD block hash %v", rp.PreviousHeadBlockHash)
		}

		rp.PreviousHeadBlockHash = common.EmptyHash
		rpLog.Info("HEAD block hash recovered successfully")
	}

	// recover the previous block hash in canonical chain.
	if rp.WritingBlockHeight > 0 && !rp.PreviousCanonicalBlockHash.IsEmpty() {
		if err := bcStore.PutBlockHash(rp.WritingBlockHeight, rp.PreviousCanonicalBlockHash); err != nil {
			rpLog.Error("Failed to recover the block hash by height in canonical chain, height = %v, hash = %v, error = %v", rp.LargerHeight, rp.PreviousCanonicalBlockHash, err.Error())
			return errors.NewStackedErrorf(err, "failed to put block hash, height = %v, hash = %v", rp.WritingBlockHeight, rp.PreviousCanonicalBlockHash)
		}

		rp.PreviousCanonicalBlockHash = common.EmptyHash
		rpLog.Info("the block hash by height in canonical chain recovered successfully")
	}

	// delete the crashed block.
	if !rp.WritingBlockHash.IsEmpty() {
		if err := bcStore.DeleteBlock(rp.WritingBlockHash); err != nil {
			rpLog.Error("Failed to delete the crashed block, hash = %v, error = %v", rp.WritingBlockHash, err.Error())
		} else {
			rpLog.Info("the crashed block deleted successfully")
		}

		rp.WritingBlockHash = common.EmptyHash
		saved = false
	}

	// go on to delete larger height blocks from canonical chain.
	if saved && rp.LargerHeight > 0 {
		if err := DeleteLargerHeightBlocks(bcStore, rp.LargerHeight, nil); err != nil {
			rpLog.Error("Failed to delete the larger height blocks in canonical chain, height = %v, error = %v", rp.LargerHeight, err.Error())
		} else {
			rpLog.Info("the larger height blocks in canonical chain deleted successfully")
		}
	}

	rp.LargerHeight = 0

	// go on to overwrite stale blocks in canonical chain.
	if saved && !rp.StaleHash.IsEmpty() {
		if err := OverwriteStaleBlocks(bcStore, rp.StaleHash, nil); err != nil {
			rpLog.Error("Failed to overwrite the stale blocks in canonical chain, hash = %v, error = %v", rp.StaleHash, err.Error())
		} else {
			rpLog.Info("stale blocks in canonical chain overwrited successfully")
		}
	}

	rp.StaleHash = common.EmptyHash

	// The repaired marker is written once, durably, before new blocks arrive.
	rp.flush()

	return nil
}

// serialize records the current marker and rewrites the file when the
// flush interval has elapsed. It does not fsync.
func (rp *recoveryPoint) serialize() {
	_ = rp.withDisk(false, nil)
}

// flush rewrites the file and fsyncs it. Clean shutdown and the one-time
// repair at startup use this. Block import does not.
func (rp *recoveryPoint) flush() {
	_ = rp.withDisk(true, nil)
}

func (rp *recoveryPoint) withDisk(durable bool, fn func() error) error {
	if rp.disk == nil {
		rp.disk = &recoveryDisk{}
	}
	rp.disk.mu.Lock()
	defer rp.disk.mu.Unlock()
	if fn != nil {
		if err := fn(); err != nil {
			return err
		}
	}
	rp.persistLocked(durable)
	return nil
}

func (rp *recoveryPoint) persistLocked(durable bool) {
	// An empty path disables the file. Tests use that to skip the mechanism.
	if rp == nil || len(rp.file) == 0 {
		return
	}
	rp.disk.dirty = true
	if !durable && !rp.disk.last.IsZero() && recoveryFlushInterval > 0 && recoveryNow().Sub(rp.disk.last) < recoveryFlushInterval {
		return
	}

	encoded, err := json.MarshalIndent(rp, "", "\t")
	if err != nil {
		rpLog.Warn("Failed to marshal recovery point info to JSON data, error = %v", err.Error())
		return
	}
	if err := writeAtomic(rp.file, encoded, durable); err != nil {
		rpLog.Warn("Failed to write recovery point JSON data to file, file = %v, error = %v", rp.file, err.Error())
		return
	}
	rp.disk.dirty = false
	rp.disk.last = recoveryNow()
}

// writeAtomic replaces path by writing a temp file and renaming it.
// doSync fsyncs that temp file first. Import passes false so a kill still
// cannot observe a torn JSON file, and a spinning disk is not stalled once
// per block. Shutdown passes true.
func writeAtomic(path string, data []byte, doSync bool) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil && doSync {
		err = f.Sync()
		if err == nil {
			atomic.AddInt64(&recoveryFsyncs, 1)
		}
	}
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	atomic.AddInt64(&recoveryDiskWrites, 1)
	return nil
}

// onPutBlockStart is used before putting a block in storage; it stores the previous block info
func (rp *recoveryPoint) onPutBlockStart(block *types.Block, bcStore store.BlockchainStore, isHead bool) error {
	return rp.withDisk(false, func() error {
		rp.WritingBlockHash = block.HeaderHash
		rp.WritingBlockHeight = block.Header.Height

		// the block of specified height may not exist in canonical chain.
		if hash, err := bcStore.GetBlockHash(rp.WritingBlockHeight); err == nil {
			rp.PreviousCanonicalBlockHash = hash
		} else {
			rp.PreviousCanonicalBlockHash = common.EmptyHash
		}

		// HEAD block hash must exist
		hash, err := bcStore.GetHeadBlockHash()
		if err != nil {
			rpLog.Error("Failed to get HEAD block hash onPutBlockStart, %v", err.Error())
			return errors.NewStackedError(err, "failed to get HEAD block hash")
		}

		rp.PreviousHeadBlockHash = hash

		if isHead {
			rp.LargerHeight = block.Header.Height + 1
			rp.StaleHash = block.Header.PreviousBlockHash
		} else {
			rp.LargerHeight = 0
			rp.StaleHash = common.EmptyHash
		}
		return nil
	})
}

// onPutBlockEnd is used after putting a block in storage; it resets some data structures
func (rp *recoveryPoint) onPutBlockEnd() {
	_ = rp.withDisk(false, func() error {
		rp.PreviousHeadBlockHash = common.EmptyHash
		rp.WritingBlockHeight = 0
		rp.PreviousCanonicalBlockHash = common.EmptyHash
		rp.WritingBlockHash = common.EmptyHash
		return nil
	})
}

// onDeleteLargerHeightBlocks sets the LargerHeight
func (rp *recoveryPoint) onDeleteLargerHeightBlocks(height uint64) {
	_ = rp.withDisk(false, func() error {
		rp.LargerHeight = height
		return nil
	})
}

// onOverwriteStaleBlocks sets the StaleHash
func (rp *recoveryPoint) onOverwriteStaleBlocks(hash common.Hash) {
	_ = rp.withDisk(false, func() error {
		rp.StaleHash = hash
		return nil
	})
}
