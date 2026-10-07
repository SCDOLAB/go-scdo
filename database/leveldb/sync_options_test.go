/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package leveldb

import (
	"fmt"
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
