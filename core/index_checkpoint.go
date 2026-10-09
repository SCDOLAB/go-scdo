/**
* @file
* @copyright defined in scdo/LICENSE
 */

package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/core/types"
)

const (
	indexCheckpointFile = "indexCheckpoint.json"

	// indexVerifyEvery is how often a running node records that the
	// height-to-hash index is intact. The record is not a chain snapshot:
	// the blocks stay in the database. Startup uses it only to skip
	// re-reading heights it already checked.
	indexVerifyEvery = 8192
)

// indexCheckpoint is the last height whose canonical height-to-hash index
// was checked. Clean is true only after a graceful shutdown wrote it.
type indexCheckpoint struct {
	VerifiedHeight uint64 `json:"verifiedHeight"`
	Clean          bool   `json:"clean"`
}

func indexCheckpointPath(recoveryFile string) string {
	if recoveryFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(recoveryFile), indexCheckpointFile)
}

// indexScanFloor is the height recoverHeightIndices stops at.
// A clean checkpoint skips blocks at or below VerifiedHeight. A missing
// file or an unclean shutdown checks all the way back to the fork height.
func indexScanFloor(cp indexCheckpoint, head uint64) uint64 {
	floor := uint64(common.SecondForkHeight)
	if !cp.Clean || cp.VerifiedHeight == 0 || cp.VerifiedHeight > head {
		return floor
	}
	if cp.VerifiedHeight > floor {
		return cp.VerifiedHeight
	}
	return floor
}

func loadIndexCheckpoint(path string) indexCheckpoint {
	if path == "" {
		return indexCheckpoint{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return indexCheckpoint{}
	}
	var cp indexCheckpoint
	if err = json.Unmarshal(data, &cp); err != nil {
		return indexCheckpoint{}
	}
	return cp
}

// committedHeadHeight is the canonical block the chain has committed.
// The checkpoint must not record anything above it.
func (bc *Blockchain) committedHeadHeight() (uint64, bool) {
	if bc == nil {
		return 0, false
	}
	loaded := bc.currentBlock.Load()
	if loaded == nil {
		return 0, false
	}
	block, ok := loaded.(*types.Block)
	if !ok || block == nil || block.Header == nil {
		return 0, false
	}
	return block.Header.Height, true
}

func (bc *Blockchain) noteIndexVerified(height uint64) {
	if bc == nil || bc.indexFile == "" || height == 0 {
		return
	}
	if head, ok := bc.committedHeadHeight(); ok && height > head {
		height = head
	}
	if height == 0 {
		return
	}
	bc.indexMu.Lock()
	defer bc.indexMu.Unlock()
	if bc.indexVerified > height {
		bc.indexVerified = height
		bc.indexClamped = time.Now()
		bc.writeIndexCheckpointLocked(indexCheckpoint{VerifiedHeight: height, Clean: false})
		return
	}
	if height <= bc.indexVerified {
		return
	}
	if bc.indexVerified > 0 && height-bc.indexVerified < indexVerifyEvery {
		return
	}
	bc.indexVerified = height
	bc.writeIndexCheckpointLocked(indexCheckpoint{VerifiedHeight: height, Clean: false})
}

// noteHeadLower drops a checkpoint that a rewind left above the committed head.
func (bc *Blockchain) noteHeadLower(head uint64) {
	if bc == nil || bc.indexFile == "" || head == 0 {
		return
	}
	bc.indexMu.Lock()
	defer bc.indexMu.Unlock()
	if bc.indexVerified <= head {
		return
	}
	bc.indexVerified = head
	// The file follows the head, but not on every block of a long reverse.
	// markIndexClean writes the final head before the databases close.
	if !bc.indexClamped.IsZero() && time.Since(bc.indexClamped) < time.Second {
		return
	}
	bc.indexClamped = time.Now()
	bc.writeIndexCheckpointLocked(indexCheckpoint{VerifiedHeight: head, Clean: false})
}

func (bc *Blockchain) markIndexClean() {
	if bc == nil || bc.indexFile == "" {
		return
	}
	height := bc.indexVerified
	if head, ok := bc.committedHeadHeight(); ok {
		height = head
	}
	bc.indexMu.Lock()
	defer bc.indexMu.Unlock()
	bc.indexVerified = height
	bc.indexClamped = time.Now()
	bc.writeIndexCheckpointLocked(indexCheckpoint{VerifiedHeight: height, Clean: true})
}

func (bc *Blockchain) writeIndexCheckpointLocked(cp indexCheckpoint) {
	if bc == nil || bc.indexFile == "" {
		return
	}
	data, err := json.MarshalIndent(cp, "", "\t")
	if err != nil {
		bc.log.Warn("failed to encode height index checkpoint: %v", err)
		return
	}
	durable := cp.Clean
	if err = writeFileAtomic(bc.indexFile, data, durable); err != nil {
		bc.log.Warn("failed to write height index checkpoint %s: %v", bc.indexFile, err)
	}
}

// writeFileAtomic replaces path via a temp file. durable fsyncs the temp
// file before the rename. The periodic checkpoint does not fsync.
func writeFileAtomic(path string, data []byte, durable bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil && durable {
		err = f.Sync()
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
	return nil
}
