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
