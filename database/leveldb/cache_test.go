/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package leveldb

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/syndtr/goleveldb/leveldb/opt"
)

func TestAutoCacheMB(t *testing.T) {
	dir := t.TempDir()
	if got := AutoCacheMB(dir); got != InitialSyncCacheMB {
		t.Fatalf("empty dir cache = %d, want %d", got, InitialSyncCacheMB)
	}
	if got := AutoCacheMB(filepath.Join(dir, "missing")); got != InitialSyncCacheMB {
		t.Fatalf("missing dir cache = %d, want %d", got, InitialSyncCacheMB)
	}

	big := filepath.Join(dir, "grown")
	if err := os.MkdirAll(big, 0700); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024*1024)
	for i := 0; i < 65; i++ {
		if err := os.WriteFile(filepath.Join(big, fmt.Sprintf("f%02d", i)), buf, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// A grown chain stays on the same 64 MiB profile. The old 512 MiB
	// initial cache is what exhausts a phone running four shards.
	if got := AutoCacheMB(big); got != 64 {
		t.Fatalf("grown dir cache = %d, want 64", got)
	}
}

func TestSyncOptionsFitAPhone(t *testing.T) {
	opts := syncOptions()
	if opts.BlockCacheCapacity != 64*opt.MiB {
		t.Fatalf("block cache = %d", opts.BlockCacheCapacity)
	}
	if opts.WriteBuffer != 32*opt.MiB {
		t.Fatalf("write buffer = %d", opts.WriteBuffer)
	}
	if opts.CompactionTableSize != 8*opt.MiB {
		t.Fatalf("table size = %d", opts.CompactionTableSize)
	}
	if !opts.NoSync {
		t.Fatal("NoSync should be set")
	}
	if opts.Filter == nil {
		t.Fatal("bloom filter missing")
	}
}

func TestCacheOptionsScalesWriteBuffer(t *testing.T) {
	opts := cacheOptions(InitialSyncCacheMB)
	if opts.BlockCacheCapacity != InitialSyncCacheMB*opt.MiB {
		t.Fatalf("block cache = %d", opts.BlockCacheCapacity)
	}
	if opts.WriteBuffer != 32*opt.MiB {
		t.Fatalf("write buffer = %d", opts.WriteBuffer)
	}
	if !opts.NoSync || opts.Filter == nil {
		t.Fatal("scaled cache must keep bloom filters and NoSync")
	}
}

func BenchmarkBatchWrite(b *testing.B) {
	for _, mb := range []int{8, InitialSyncCacheMB} {
		b.Run(fmt.Sprintf("%dMB", mb), func(b *testing.B) {
			dir := b.TempDir()
			db, err := NewLevelDBWithCache(dir, mb)
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			payload := make([]byte, 4096)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				batch := db.NewBatch()
				for k := 0; k < 32; k++ {
					key := []byte(fmt.Sprintf("b-%d-%d", i, k))
					batch.Put(key, payload)
				}
				if err := batch.Commit(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestNewLevelDBWithCacheBatch(t *testing.T) {
	dir := t.TempDir()
	db, err := NewLevelDBWithCache(dir, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	batch := db.NewBatch()
	for i := 0; i < 100; i++ {
		batch.Put([]byte{byte(i)}, []byte("block"))
	}
	if err := batch.Commit(); err != nil {
		t.Fatal(err)
	}
	ok, err := db.Has([]byte{1})
	if err != nil || !ok {
		t.Fatalf("batch write missing, ok=%v err=%v", ok, err)
	}
}
