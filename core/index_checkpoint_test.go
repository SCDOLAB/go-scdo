/**
* @file
* @copyright defined in scdo/LICENSE
 */

package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scdoproject/go-scdo/common"
	"github.com/scdoproject/go-scdo/core/types"
)

func TestIndexScanFloor(t *testing.T) {
	head := uint64(common.SecondForkHeight) + 100000
	fork := uint64(common.SecondForkHeight)

	if got := indexScanFloor(indexCheckpoint{}, head); got != fork {
		t.Fatalf("missing checkpoint floor %d, want fork %d", got, fork)
	}
	if got := indexScanFloor(indexCheckpoint{VerifiedHeight: head - 10, Clean: false}, head); got != fork {
		t.Fatalf("unclean checkpoint floor %d, want full scan from fork %d", got, fork)
	}
	if got := indexScanFloor(indexCheckpoint{VerifiedHeight: head - 10, Clean: true}, head); got != head-10 {
		t.Fatalf("clean checkpoint floor %d, want %d", got, head-10)
	}
	if got := indexScanFloor(indexCheckpoint{VerifiedHeight: head + 5, Clean: true}, head); got != fork {
		t.Fatalf("checkpoint above head floor %d, want fork %d", got, fork)
	}
	// A chain that has not reached the fork still does not scan below the fork.
	if got := indexScanFloor(indexCheckpoint{VerifiedHeight: 10, Clean: true}, 20); got != fork {
		t.Fatalf("short chain floor %d, want fork %d", got, fork)
	}
}

func TestHeightIndexStopBoundsUncleanScan(t *testing.T) {
	fork := uint64(common.SecondForkHeight)
	head := fork + 6_000_000

	// Unclean or missing checkpoint: do not walk millions of blocks.
	stop, limited := heightIndexStop(head, fork)
	if !limited || stop != head-recoverDepth {
		t.Fatalf("unclean stop %d limited %v, want %d", stop, limited, head-recoverDepth)
	}

	// Clean checkpoint inside the recent window still wins.
	stop, limited = heightIndexStop(head, head-10)
	if limited || stop != head-10 {
		t.Fatalf("clean checkpoint stop %d limited %v, want %d", stop, limited, head-10)
	}

	// Fresh sync still within 20_000 of fork genesis is not shortened.
	fresh := fork + 1000
	stop, limited = heightIndexStop(fresh, fork)
	if limited || stop != fork {
		t.Fatalf("fresh sync stop %d limited %v, want fork %d", stop, limited, fork)
	}

	// A chain exactly at the cap is not shortened.
	stop, limited = heightIndexStop(fork+recoverDepth, fork)
	if limited || stop != fork {
		t.Fatalf("boundary stop %d limited %v, want fork %d", stop, limited, fork)
	}
}

func TestIndexCheckpointRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, indexCheckpointFile)
	bc := &Blockchain{indexFile: path, log: rpLog}
	bc.indexMu.Lock()
	bc.writeIndexCheckpointLocked(indexCheckpoint{VerifiedHeight: 3210000, Clean: true})
	bc.indexMu.Unlock()

	got := loadIndexCheckpoint(path)
	if !got.Clean || got.VerifiedHeight != 3210000 {
		t.Fatalf("loaded %+v", got)
	}

	bc.indexVerified = 3210000
	bc.markIndexClean()
	got = loadIndexCheckpoint(path)
	if !got.Clean || got.VerifiedHeight != 3210000 {
		t.Fatalf("clean shutdown checkpoint %+v", got)
	}

	os.Remove(path)
	if loadIndexCheckpoint(path).Clean || loadIndexCheckpoint(path).VerifiedHeight != 0 {
		t.Fatal("missing file should scan from the fork")
	}
}

// Shard1 on a420ba8 wrote verifiedHeight 3943820 while the canonical head
// stayed 3943308. 3943820 is one header batch (512) above that head and lands
// on an 8192 checkpoint boundary.
func TestCheckpointNeverExceedsCanonicalHead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, indexCheckpointFile)
	bc := &Blockchain{indexFile: path, log: rpLog}
	const head = uint64(3943308)
	const ahead = uint64(3943820)
	bc.currentBlock.Store(&types.Block{Header: &types.BlockHeader{Height: head}})

	bc.noteIndexVerified(ahead)
	got := loadIndexCheckpoint(path)
	if got.VerifiedHeight > head {
		t.Fatalf("noteIndexVerified wrote %d, committed head is %d", got.VerifiedHeight, head)
	}
	if got.VerifiedHeight != head {
		t.Fatalf("noteIndexVerified wrote %d, want the committed head %d", got.VerifiedHeight, head)
	}

	bc.indexVerified = ahead
	bc.UpdateCurrentBlock(&types.Block{Header: &types.BlockHeader{Height: head}})
	got = loadIndexCheckpoint(path)
	if got.VerifiedHeight > head {
		t.Fatalf("after the head moved back, checkpoint is %d", got.VerifiedHeight)
	}

	bc.indexVerified = ahead
	bc.markIndexClean()
	got = loadIndexCheckpoint(path)
	if !got.Clean || got.VerifiedHeight != head {
		t.Fatalf("clean shutdown checkpoint %+v, committed head %d", got, head)
	}
}

func TestCleanCheckpointStaysCleanWhileShutdownWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, indexCheckpointFile)
	const head = uint64(9274822)
	bc := &Blockchain{indexFile: path, log: rpLog}
	bc.currentBlock.Store(&types.Block{Header: &types.BlockHeader{Height: head}})
	bc.indexVerified = head - 100000
	bc.markIndexClean()

	bc.UpdateCurrentBlock(&types.Block{Header: &types.BlockHeader{Height: head - 10}})
	got := loadIndexCheckpoint(path)
	if !got.Clean || got.VerifiedHeight != head-10 {
		t.Fatalf("rewind during shutdown checkpoint %+v", got)
	}

	bc.currentBlock.Store(&types.Block{Header: &types.BlockHeader{Height: head}})
	bc.indexMu.Lock()
	bc.indexVerified = 0
	bc.indexMu.Unlock()
	bc.noteIndexVerified(head + indexVerifyEvery)
	got = loadIndexCheckpoint(path)
	if !got.Clean || got.VerifiedHeight != head-10 {
		t.Fatalf("later verification cleared the clean checkpoint: %+v", got)
	}
}
