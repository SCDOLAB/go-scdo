/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package leveldb

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

func TestSyncOptionsFitHDD(t *testing.T) {
	o := syncOptions()
	assert.True(t, o.NoSync, "per-table fsync must be off")
	assert.NotNil(t, o.Filter)
	assert.True(t, o.WriteBuffer >= 16*opt.MiB)
	assert.True(t, o.BlockCacheCapacity >= 32*opt.MiB)
	assert.True(t, o.CompactionTableSize >= 8*opt.MiB)
	assert.False(t, asyncWrite.Sync)
	assert.True(t, checkpointWrite.Sync)
}

// TestFsyncsPerImportedBlock counts sync batch commits across a run of
// block-sized writes. The import path must stay at 0. One checkpoint commit
// is the only call that sets Sync, and Options.NoSync still drops the journal
// fsync while the HDD profile is on.
func TestFsyncsPerImportedBlock(t *testing.T) {
	db, dispose := NewTestDatabase()
	defer dispose()

	const blocks = 200
	before := atomic.LoadUint64(&batchCommits)
	beforeSync := atomic.LoadUint64(&batchSyncCommits)
	body := make([]byte, 1024)
	for i := 0; i < blocks; i++ {
		batch := db.NewBatch().(*Batch)
		batch.Put([]byte(fmt.Sprintf("h-%d", i)), body)
		if err := batch.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	commits := atomic.LoadUint64(&batchCommits) - before
	syncs := atomic.LoadUint64(&batchSyncCommits) - beforeSync
	if commits != blocks {
		t.Fatalf("batch commits = %d, want %d", commits, blocks)
	}
	perBlock := float64(syncs) / float64(blocks)
	t.Logf("leveldb sync commits per imported block: %.4f", perBlock)
	if syncs != 0 || asyncWrite.Sync {
		t.Fatalf("import issued %.4f sync commits per block, want 0", perBlock)
	}

	cp := db.NewBatch().(*Batch)
	cp.Put([]byte("checkpoint"), body)
	if err := cp.CommitCheckpoint(); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadUint64(&batchSyncCommits)-beforeSync != 1 {
		t.Fatal("checkpoint commit was not counted separately from import")
	}
}

// TestBlockWriteRateHolds writes block-sized batches until the database is
// larger than the memtable, then checks the later window. The goal on a
// spinning disk is to stay above 50 blocks/s; this machine may be faster,
// and the numbers are printed either way.
func TestBlockWriteRateHolds(t *testing.T) {
	const (
		blocks   = 6000
		bodySize = 8 * 1024
		window   = 1500
	)
	tunedDir := t.TempDir()
	baseDir := t.TempDir()

	tuned, err := leveldb.OpenFile(tunedDir, syncOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer tuned.Close()
	base, err := leveldb.OpenFile(baseDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()

	tunedFirst := measureBlockWrites(t, tuned, 0, window, bodySize)
	baseFirst := measureBlockWrites(t, base, 0, window, bodySize)
	// Grow past the default 4MiB memtable so compaction is in the later window.
	measureBlockWrites(t, tuned, window, blocks-2*window, bodySize)
	measureBlockWrites(t, base, window, blocks-2*window, bodySize)
	tunedLast := measureBlockWrites(t, tuned, blocks-window, window, bodySize)
	baseLast := measureBlockWrites(t, base, blocks-window, window, bodySize)

	t.Logf("leveldb block writes with state reads: tuned first %.1f blk/s, tuned later %.1f blk/s; default first %.1f blk/s, default later %.1f blk/s",
		tunedFirst, tunedLast, baseFirst, baseLast)

	if tunedLast < 50 {
		t.Fatalf("tuned LevelDB later window is %.1f blk/s, want at least 50", tunedLast)
	}
}

func measureBlockWrites(t *testing.T, db *leveldb.DB, start, n, bodySize int) float64 {
	t.Helper()
	body := make([]byte, bodySize)
	state := make([]byte, 128)
	for i := range body {
		body[i] = byte(i)
	}
	began := time.Now()
	for i := start; i < start+n; i++ {
		// State application looks up trie nodes. Bloom filters should turn
		// most misses into one filter check instead of a random table read.
		if i > start+8 {
			for k := 0; k < 6; k++ {
				key := []byte(fmt.Sprintf("s-%08d-%02d", start+(i-start+k)%(i-start), k%8))
				_, _ = db.Get(key, nil)
			}
		}
		batch := new(leveldb.Batch)
		batch.Put([]byte(fmt.Sprintf("h-%08d", i)), body[:373])
		batch.Put([]byte(fmt.Sprintf("b-%08d", i)), body)
		for k := 0; k < 8; k++ {
			batch.Put([]byte(fmt.Sprintf("s-%08d-%02d", i, k)), state)
		}
		if err := db.Write(batch, asyncWrite); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(began)
	if elapsed <= 0 {
		return 0
	}
	return float64(n) / elapsed.Seconds()
}
